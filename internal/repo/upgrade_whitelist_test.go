package repo

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestUpgradeWhitelist 覆盖可升级白名单的读写语义：
// 默认关闭（沿用同服务器全量候选）；开启后空白名单表示"不允许升级到任何产品"；
// 不能把自己列为升级目标。
func TestUpgradeWhitelist(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("no dsn")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	p := &Products{db: d}

	var a, b int64
	for _, name := range []string{"单元测试-白名单源", "单元测试-白名单目标"} {
		var id int64
		if err := d.QueryRowContext(ctx,
			`INSERT INTO products(type_id,name,stock) VALUES(NULL,$1,-1) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if a == 0 {
			a = id
		} else {
			b = id
		}
	}
	defer func() {
		if _, err := d.ExecContext(ctx, `DELETE FROM products WHERE id IN ($1,$2)`, a, b); err != nil {
			t.Errorf("清理测试产品失败: %v", err)
		}
	}()

	// 默认关闭：升级部署后不能让全站服务突然失去升降级入口。
	if on, err := p.UpgradeWhitelistEnabled(ctx, a); err != nil || on {
		t.Fatalf("新建产品应默认关闭白名单，实得 on=%v err=%v", on, err)
	}

	// 开启并选一个目标
	if err := p.SetUpgradeWhitelist(ctx, a, true, []int64{b}); err != nil {
		t.Fatalf("保存白名单失败: %v", err)
	}
	if on, err := p.UpgradeWhitelistEnabled(ctx, a); err != nil || !on {
		t.Fatalf("应已开启白名单，实得 on=%v err=%v", on, err)
	}
	ids, err := p.UpgradeTargetIDs(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if !ids[b] || len(ids) != 1 {
		t.Fatalf("白名单应只含目标产品 %d，实得 %v", b, ids)
	}

	// 重新保存应覆盖而非累加（先删后插）
	if err := p.SetUpgradeWhitelist(ctx, a, true, nil); err != nil {
		t.Fatalf("清空白名单失败: %v", err)
	}
	ids, err = p.UpgradeTargetIDs(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("清空后应无目标，实得 %v", ids)
	}

	// 不能把自己列为升级目标
	if err := p.SetUpgradeWhitelist(ctx, a, true, []int64{a, b}); err != nil {
		t.Fatalf("保存白名单失败: %v", err)
	}
	ids, _ = p.UpgradeTargetIDs(ctx, a)
	if ids[a] {
		t.Fatal("不能把产品自己列为升级目标")
	}
	if !ids[b] {
		t.Fatalf("目标产品 %d 应保留，实得 %v", b, ids)
	}
}

// TestSameServerUpgradeCandidates 候选列表必须排除自己、隐藏商品与上游已下架商品。
func TestSameServerUpgradeCandidates(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("no dsn")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	p := &Products{db: d}

	var sv int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO servers(name,provider,api_url,api_username,api_key) VALUES('单元测试-EP','easypanel','http://127.0.0.1:1','u','k') RETURNING id`).
		Scan(&sv); err != nil {
		t.Fatal(err)
	}
	mk := func(name string, hidden bool, offline string) int64 {
		var id int64
		if err := d.QueryRowContext(ctx,
			`INSERT INTO products(type_id,name,stock,server_id,hidden,upstream_offline_reason)
			 VALUES(NULL,$1,-1,$2,$3,$4) RETURNING id`, name, sv, hidden, offline).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	me := mk("候选-自己", false, "")
	ok := mk("候选-正常", false, "")
	hid := mk("候选-隐藏", true, "")
	off := mk("候选-下架", false, "unshelved")
	defer func() {
		if _, err := d.ExecContext(ctx,
			`DELETE FROM products WHERE id IN ($1,$2,$3,$4)`, me, ok, hid, off); err != nil {
			t.Errorf("清理测试产品失败: %v", err)
		}
		if _, err := d.ExecContext(ctx, `DELETE FROM servers WHERE id=$1`, sv); err != nil {
			t.Errorf("清理测试服务器失败: %v", err)
		}
	}()

	cands, err := p.SameServerUpgradeCandidates(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, c := range cands {
		got[c.ID] = true
	}
	if got[me] {
		t.Fatal("候选不应包含产品自己")
	}
	if !got[ok] {
		t.Fatal("候选应包含同服务器正常在售产品")
	}
	if got[hid] {
		t.Fatal("候选不应包含已隐藏产品")
	}
	if got[off] {
		t.Fatal("候选不应包含上游已下架产品")
	}
}
