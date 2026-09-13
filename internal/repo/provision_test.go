package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestDeleteCheckpointKeepsOthers 验证按 key 删除检查点不会波及其它键。
// 开通检查点与升级流程的 upgrade_<订单号>（done/refunded）同存 provision_data，
// 误删会让降级重跑并重复退款，故这里必须锁住"只删指定键"的语义。
func TestDeleteCheckpointKeepsOthers(t *testing.T) {
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

	// services.user_id / product_id 均为 NOT NULL + 外键，复用库里已有的行，避免造脏数据。
	var uid, pid int64
	if err := d.QueryRowContext(ctx, `SELECT id FROM users LIMIT 1`).Scan(&uid); err != nil {
		t.Skip("库中暂无用户，跳过")
	}
	if err := d.QueryRowContext(ctx, `SELECT id FROM products LIMIT 1`).Scan(&pid); err != nil {
		t.Skip("库中暂无产品，跳过")
	}

	var sid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,status,provision_data) VALUES($1,$2,0,$3::jsonb) RETURNING id`,
		uid, pid, `{"upgrade_999":"done","upstream_invoice_id":"9001","upstream_host_ids":"123"}`).
		Scan(&sid); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := d.ExecContext(ctx, `DELETE FROM services WHERE id=$1`, sid); err != nil {
			t.Errorf("清理测试服务失败: %v", err)
		}
	}()

	p := &ProvisionRepo{db: d}
	for _, k := range []string{"upstream_invoice_id", "upstream_host_ids"} {
		if err := p.DeleteCheckpoint(ctx, sid, k); err != nil {
			t.Fatalf("删除检查点 %s 失败: %v", k, err)
		}
	}

	var raw []byte
	if err := d.QueryRowContext(ctx, `SELECT provision_data FROM services WHERE id=$1`, sid).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"upstream_invoice_id", "upstream_host_ids"} {
		if _, ok := m[k]; ok {
			t.Fatalf("%s 应被清除，实得 %v", k, m)
		}
	}
	if m["upgrade_999"] != "done" {
		t.Fatalf("升级检查点不应被波及，实得 %v", m["upgrade_999"])
	}
}
