package repo

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// 限购校验的语句必须能执行：PostgreSQL 禁止聚合函数与行锁同用，
// 旧实现写成 `count(*) ... FOR UPDATE` 会直接报错，令凡配置了「每人限购」的活动一单都下不了。
// 本用例固定住这一点（无超限分支覆盖，超限时返回 ErrPromotionLimitExceeded）。
func TestCheckLimitPerUserRuns(t *testing.T) {
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
	p := NewPromotions(d)

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	// 不存在的活动/用户 → 计数为 0，未超限应放行。
	if err := p.CheckLimitPerUser(ctx, tx, 0, 0, 1); err != nil {
		t.Fatalf("未超限应放行，实得 %v", err)
	}
	// limit<=0 表示不限购，直接放行且不查库。
	if err := p.CheckLimitPerUser(ctx, tx, 0, 0, 0); err != nil {
		t.Fatalf("未配置限购应放行，实得 %v", err)
	}
}
