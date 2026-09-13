package repo

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestUpdatePriceAndStockSkippingZero 验证同步专用写入：上游为 0 的周期不覆盖本地现值。
func TestUpdatePriceAndStockSkippingZero(t *testing.T) {
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
	psID, err := p.DefaultPricesetID(ctx)
	if err != nil {
		t.Fatalf("读取默认价格组失败: %v", err)
	}
	var pid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock) VALUES(NULL,'单元测试-逐周期零价保护',-1) RETURNING id`).
		Scan(&pid); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := d.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, pid); err != nil {
			t.Errorf("清理测试产品失败: %v", err)
		}
	}()
	if _, err := d.ExecContext(ctx,
		`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,9,80,900)`,
		pid, psID); err != nil {
		t.Fatal(err)
	}
	// 上游只提供月付新价，季/年付为 0：只应覆盖月价，季/年价保留本地现值。
	if err := p.UpdatePriceAndStockSkippingZero(ctx, pid, 12, 0, 0, 5); err != nil {
		t.Fatal(err)
	}
	var monthly, quarterly, yearly string
	var stock int
	if err := d.QueryRowContext(ctx,
		`SELECT pp.monthly::text, pp.quarterly::text, pp.yearly::text, pr.stock
		   FROM product_prices pp JOIN products pr ON pr.id=pp.product_id
		  WHERE pp.product_id=$1 AND pp.priceset_id=$2`, pid, psID).
		Scan(&monthly, &quarterly, &yearly, &stock); err != nil {
		t.Fatal(err)
	}
	if monthly != "12.00" || quarterly != "80.00" || yearly != "900.00" || stock != 5 {
		t.Fatalf("逐周期零价保护失效: monthly=%s quarterly=%s yearly=%s stock=%d（期望 12.00/80.00/900.00/5）",
			monthly, quarterly, yearly, stock)
	}
}
