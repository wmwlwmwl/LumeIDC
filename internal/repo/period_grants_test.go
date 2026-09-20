package repo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Grant 只把 UNIQUE(invoice_id) 冲突当作"已授权"。
// 旧实现把任何错误都返回 ErrAlreadyGranted，真实故障（外键违规、连接中断、约束变更）
// 会被伪装成"该账单已授权续费"，调用方据此中止支付事务并给出误导性结论：
// 钱已收、周期没延长，而排查时看到的却是"重复续费"。
func TestPeriodGrantReportsRealFailures(t *testing.T) {
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
	p := NewPeriodGrants(d)

	t.Run("真实故障原样上报", func(t *testing.T) {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		// 不存在的服务与账单：外键立即违规。
		err = p.Grant(ctx, tx, -1, -1, "monthly")
		if err == nil {
			t.Fatal("外键违规应报错")
		}
		if errors.Is(err, ErrAlreadyGranted) {
			t.Fatalf("真实故障不得伪装成已授权: %v", err)
		}
	})

	t.Run("重复授权仍返回已授权", func(t *testing.T) {
		var invID, svcID int64
		if err := d.QueryRowContext(ctx,
			`SELECT (SELECT id FROM invoices LIMIT 1), (SELECT id FROM services LIMIT 1)`).Scan(&invID, &svcID); err != nil {
			t.Skip("库中暂无账单或服务，跳过")
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		// 首次可能成功，也可能该账单早已授权（共享测试库），两者都不算错。
		if err := p.Grant(ctx, tx, svcID, invID, "monthly"); err != nil && !errors.Is(err, ErrAlreadyGranted) {
			t.Fatalf("首次授权应成功或已授权，实得 %v", err)
		}
		if err := p.Grant(ctx, tx, svcID, invID, "monthly"); !errors.Is(err, ErrAlreadyGranted) {
			t.Fatalf("重复授权应返回 ErrAlreadyGranted，实得 %v", err)
		}
	})
}
