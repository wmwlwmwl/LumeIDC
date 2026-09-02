package handler

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"lumeidc/internal/repo"
)

func TestProductsListData(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip()
	}
	d, _ := sql.Open("pgx", dsn)
	defer d.Close()
	p := repo.NewProducts(d)
	list, err := p.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	types, _ := p.ListTypes(context.Background())
	t.Logf("products=%d types=%d", len(list), len(types))
	psID, err := p.DefaultPricesetID(context.Background())
	t.Logf("priceset=%d err=%v", psID, err)
	for _, pr := range list {
		price, err := p.Price(context.Background(), pr.ID, psID)
		t.Logf("prod %d price=%+v err=%v", pr.ID, price, err)
	}
}
