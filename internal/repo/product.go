package repo

import (
	"context"
	"database/sql"
	"errors"
)

type Product struct {
	ID            int64
	TypeID        sql.NullInt64
	ServerID      sql.NullInt64
	UpstreamPID   int64
	UpstreamCycle string
	Name          string
	Description   string
	Stock         int
	Hidden        bool
	ProfitType    int16   // 0百分比 1固定金额（对齐 ZJMF 上游利润方式）
	ProfitValue   float64 // 百分比或固定金额
}

type ProductType struct {
	ID          int64
	Name        string
	Description string
	Sort        int
}

type Products struct{ DB *sql.DB }

const productCols = `SELECT id,type_id,server_id,upstream_pid,upstream_cycle,name,description,stock,hidden,profit_type,profit_value`

func scanProduct(rows *sql.Rows, pr *Product) error {
	return rows.Scan(&pr.ID, &pr.TypeID, &pr.ServerID, &pr.UpstreamPID, &pr.UpstreamCycle,
		&pr.Name, &pr.Description, &pr.Stock, &pr.Hidden, &pr.ProfitType, &pr.ProfitValue)
}

func (p *Products) ListVisible(ctx context.Context) ([]Product, error) {
	rows, err := p.DB.QueryContext(ctx,
		productCols+` FROM products WHERE hidden=false ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Product
	for rows.Next() {
		var pr Product
		if err := scanProduct(rows, &pr); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// ListAll 后台用：包含隐藏产品
func (p *Products) ListAll(ctx context.Context) ([]Product, error) {
	rows, err := p.DB.QueryContext(ctx,
		productCols+` FROM products ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Product
	for rows.Next() {
		var pr Product
		if err := scanProduct(rows, &pr); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// ListByType returns visible products in a type.
func (p *Products) ListByType(ctx context.Context, typeID int64) ([]Product, error) {
	rows, err := p.DB.QueryContext(ctx,
		productCols+` FROM products WHERE hidden=false AND type_id=$1 ORDER BY id`, typeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Product
	for rows.Next() {
		var pr Product
		if err := scanProduct(rows, &pr); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

func (p *Products) Get(ctx context.Context, id int64) (*Product, error) {
	var pr Product
	err := p.DB.QueryRowContext(ctx,
		productCols+` FROM products WHERE id=$1`, id).
		Scan(&pr.ID, &pr.TypeID, &pr.ServerID, &pr.UpstreamPID, &pr.UpstreamCycle,
			&pr.Name, &pr.Description, &pr.Stock, &pr.Hidden)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &pr, err
}

func (p *Products) Create(ctx context.Context, typeID sql.NullInt64, name, description string, stock int) (int64, error) {
	var id int64
	err := p.DB.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,description,stock) VALUES($1,$2,$3,$4) RETURNING id`,
		typeID, name, description, stock).Scan(&id)
	return id, err
}

func (p *Products) Update(ctx context.Context, id int64, typeID sql.NullInt64, name, description string, stock int, hidden bool) error {
	_, err := p.DB.ExecContext(ctx,
		`UPDATE products SET type_id=$2,name=$3,description=$4,stock=$5,hidden=$6 WHERE id=$1`,
		id, typeID, name, description, stock, hidden)
	return err
}

// SetBinding 更新产品上游绑定。
func (p *Products) SetBinding(ctx context.Context, productID int64, serverID sql.NullInt64, upstreamPID int64) error {
	_, err := p.DB.ExecContext(ctx,
		`UPDATE products SET server_id=$2,upstream_pid=$3 WHERE id=$1`,
		productID, serverID, upstreamPID)
	return err
}

// SetProfit 设置产品利润方式（0百分比/1固定金额）与值。
func (p *Products) SetProfit(ctx context.Context, productID int64, profitType int16, profitValue float64) error {
	_, err := p.DB.ExecContext(ctx,
		`UPDATE products SET profit_type=$2,profit_value=$3 WHERE id=$1`,
		productID, profitType, profitValue)
	return err
}

func (p *Products) Delete(ctx context.Context, id int64) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, id)
	return err
}

// ---------- 分类管理 ----------

func (p *Products) ListTypes(ctx context.Context) ([]ProductType, error) {
	rows, err := p.DB.QueryContext(ctx,
		`SELECT id,name,description,sort FROM product_types ORDER BY sort,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductType
	for rows.Next() {
		var t ProductType
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.Sort); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Products) CreateType(ctx context.Context, name, description string, sort int) (int64, error) {
	var id int64
	err := p.DB.QueryRowContext(ctx,
		`INSERT INTO product_types(name,description,sort) VALUES($1,$2,$3) RETURNING id`,
		name, description, sort).Scan(&id)
	return id, err
}

func (p *Products) UpdateType(ctx context.Context, id int64, name, description string, sort int) error {
	_, err := p.DB.ExecContext(ctx,
		`UPDATE product_types SET name=$2,description=$3,sort=$4 WHERE id=$1`, id, name, description, sort)
	return err
}

func (p *Products) DeleteType(ctx context.Context, id int64) error {
	// 分类下有产品则拒绝删除，避免悬挂引用
	var n int
	if err := p.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM products WHERE type_id=$1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrTypeInUse
	}
	_, err := p.DB.ExecContext(ctx, `DELETE FROM product_types WHERE id=$1`, id)
	return err
}

var ErrTypeInUse = fixedErr("该分类下仍有产品，无法删除")

type fixedErr string

func (e fixedErr) Error() string { return string(e) }

// PriceRow is the per-cycle price for one product under a priceset.
type PriceRow struct {
	Monthly   string
	Quarterly string
	Yearly    string
}

func (p *Products) Price(ctx context.Context, productID, pricesetID int64) (*PriceRow, error) {
	var r PriceRow
	err := p.DB.QueryRowContext(ctx,
		`SELECT monthly,quarterly,yearly FROM product_prices WHERE product_id=$1 AND priceset_id=$2`,
		productID, pricesetID).Scan(&r.Monthly, &r.Quarterly, &r.Yearly)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

// DefaultPricesetID returns the lowest priceset id. ponytail: 一期单价格组简化，
// 多价格组/分组折扣二期改为按用户 group 关联。
func (p *Products) DefaultPricesetID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	if err := p.DB.QueryRowContext(ctx, `SELECT min(id) FROM pricesets`).Scan(&id); err != nil {
		return 0, err
	}
	if !id.Valid {
		return 0, ErrNotFound
	}
	return id.Int64, nil
}
