package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	moneypkg "lumeidc/internal/money"
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
	ParentID    int64 // 0=一级；仅支持两级（对齐 ZJMF 商品分组）
	Hidden      bool
}

type Products struct{ DB *sql.DB }

const productCols = `SELECT id,type_id,server_id,upstream_pid,upstream_cycle,name,description,stock,hidden,profit_type,profit_value`

func scanProduct(rows *sql.Rows, pr *Product) error {
	return rows.Scan(&pr.ID, &pr.TypeID, &pr.ServerID, &pr.UpstreamPID, &pr.UpstreamCycle,
		&pr.Name, &pr.Description, &pr.Stock, &pr.Hidden, &pr.ProfitType, &pr.ProfitValue)
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

// ListVisibleByTypes 前台按分类集合取可见产品（当前仅传单 ID；保留集合签名便于后续组合展示）。
func (p *Products) ListVisibleByTypes(ctx context.Context, typeIDs []int64) ([]Product, error) {
	if len(typeIDs) == 0 {
		return nil, nil
	}
	rows, err := p.DB.QueryContext(ctx,
		productCols+` FROM products WHERE hidden=false AND type_id = ANY($1) ORDER BY id`, typeIDs)
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

func (p *Products) IsSellable(ctx context.Context, id int64) (bool, error) {
	var ok bool
	err := p.DB.QueryRowContext(ctx, `
		SELECT p.hidden=false AND t.id IS NOT NULL AND t.hidden=false
			AND (t.parent_id=0 OR EXISTS (
				SELECT 1 FROM product_types parent WHERE parent.id=t.parent_id AND parent.hidden=false
			))
		FROM products p LEFT JOIN product_types t ON t.id=p.type_id WHERE p.id=$1`, id).Scan(&ok)
	return ok, err
}
func (p *Products) Get(ctx context.Context, id int64) (*Product, error) {
	var pr Product
	err := p.DB.QueryRowContext(ctx,
		productCols+` FROM products WHERE id=$1`, id).
		Scan(&pr.ID, &pr.TypeID, &pr.ServerID, &pr.UpstreamPID, &pr.UpstreamCycle,
			&pr.Name, &pr.Description, &pr.Stock, &pr.Hidden, &pr.ProfitType, &pr.ProfitValue)
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
		`SELECT id,name,description,sort,parent_id,hidden FROM product_types ORDER BY sort,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductType
	for rows.Next() {
		var t ProductType
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.Sort, &t.ParentID, &t.Hidden); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Products) CreateType(ctx context.Context, name, description string, sort int, parentID int64, hidden bool) (int64, error) {
	var id int64
	err := p.DB.QueryRowContext(ctx,
		`INSERT INTO product_types(name,description,sort,parent_id,hidden) VALUES($1,$2,$3,$4,$5) RETURNING id`,
		name, description, sort, parentID, hidden).Scan(&id)
	return id, err
}

func (p *Products) UpdateType(ctx context.Context, id int64, name, description string, sort int, parentID int64, hidden bool) error {
	_, err := p.DB.ExecContext(ctx,
		`UPDATE product_types SET name=$2,description=$3,sort=$4,parent_id=$5,hidden=$6 WHERE id=$1`,
		id, name, description, sort, parentID, hidden)
	return err
}

// TypeProductCounts 各分类直挂产品数（不含子分类）。
func (p *Products) TypeProductCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := p.DB.QueryContext(ctx,
		`SELECT type_id,count(*) FROM products WHERE type_id IS NOT NULL GROUP BY type_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// MoveTypeProducts 整组移动产品到目标分类，返回移动数量。
func (p *Products) MoveTypeProducts(ctx context.Context, from, to int64) (int64, error) {
	res, err := p.DB.ExecContext(ctx,
		`UPDATE products SET type_id=$2 WHERE type_id=$1`, from, to)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (p *Products) DeleteType(ctx context.Context, id int64) error {
	// 有子分类或有产品均拒绝删除（对齐 ZJMF 删除保护）
	var n int
	if err := p.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM product_types WHERE parent_id=$1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrTypeHasChildren
	}
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

var (
	ErrTypeInUse       = fixedErr("该分类下仍有产品，无法删除")
	ErrTypeHasChildren = fixedErr("该分类下有子分类，无法删除")
	ErrTypeNotFound    = fixedErr("分类不存在")
)

// FindType 在扁平分类表中按 ID 查找。
func FindType(types []ProductType, id int64) (ProductType, bool) {
	for _, t := range types {
		if t.ID == id {
			return t, true
		}
	}
	return ProductType{}, false
}

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

// BoundProduct 上游已绑定产品的同步所需字段。
type BoundProduct struct {
	ID          int64
	ServerID    int64
	UpstreamPID int64
}

// ListBound 返回所有绑定了上游（server_id + upstream_pid）的产品。
func (p *Products) ListBound(ctx context.Context) ([]BoundProduct, error) {
	rows, err := p.DB.QueryContext(ctx,
		`SELECT id,server_id,upstream_pid FROM products WHERE server_id IS NOT NULL AND upstream_pid > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BoundProduct
	for rows.Next() {
		var bp BoundProduct
		if err := rows.Scan(&bp.ID, &bp.ServerID, &bp.UpstreamPID); err != nil {
			continue
		}
		out = append(out, bp)
	}
	return out, rows.Err()
}

// UpdatePriceAndStock 更新产品价格与库存。
func (p *Products) UpdatePriceAndStock(ctx context.Context, productID int64, monthly, quarterly, yearly float64, stock int) error {
	if !moneypkg.FiniteNonNegative(monthly) || !moneypkg.FiniteNonNegative(quarterly) || !moneypkg.FiniteNonNegative(yearly) {
		return fmt.Errorf("商品价格无效")
	}
	psID, _ := p.DefaultPricesetID(ctx)
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (product_id,priceset_id) DO UPDATE SET monthly=$3,quarterly=$4,yearly=$5`,
		productID, psID, money(monthly), money(quarterly), money(yearly)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE products SET stock=$2 WHERE id=$1`, productID, stock); err != nil {
		return err
	}
	return tx.Commit()
}

func money(v float64) string { return fmt.Sprintf("%.2f", v) }
