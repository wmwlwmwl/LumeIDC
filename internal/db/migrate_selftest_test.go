package db

import (
	"context"
	"os"
	"testing"
)

// 需要真实 PG：设置 TEST_DATABASE_DSN 才运行
func TestMigrateCreatesTables(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_DATABASE_DSN，跳过")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := Migrate(context.Background(), d, Migrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('users','admin_users','products','services','orders','invoices')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("期望 6 张核心表，实际 %d", n)
	}
	var pricesets int
	if err := d.QueryRow(`SELECT count(*) FROM pricesets`).Scan(&pricesets); err != nil {
		t.Fatal(err)
	}
	if pricesets == 0 {
		t.Fatal("默认价格组未初始化")
	}
}
