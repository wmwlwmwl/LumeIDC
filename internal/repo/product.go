package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	moneypkg "lumeidc/internal/money"
)

type Product struct {
	ID               int64
	TypeID           sql.NullInt64
	ServerID         sql.NullInt64
	UpstreamPID      int64
	UpstreamCycle    string
	Name             string
	Description      string
	Stock            int
	Hidden           bool
	ProfitType       int16   // 0百分比 1固定金额（对齐 ZJMF 上游利润方式）
	ProfitValue      float64 // 百分比或固定金额
	RequiresIdentity bool    // 购买该产品是否必须已通过实名认证
}

// AdminProductRow 是后台产品列表一次查询所需的展示数据。
// 配置项在这里解析，避免列表模板为每个产品再次访问数据库。
type AdminProductRow struct {
	ID               int64
	Name             string
	TypeName         string
	ServerName       string
	UpstreamPID      int64
	Monthly          string
	Hidden           bool
	Options          []ConfigOption
	ProfitType       int16
	ProfitValue      float64
	RequiresIdentity bool
}

type ProductType struct {
	ID          int64
	Name        string
	Description string
	Sort        int
	ParentID    int64 // 0=一级；仅支持两级（对齐 ZJMF 商品分组）
	Hidden      bool
}

type Products struct{ db *sql.DB }

const productCols = `SELECT id,type_id,server_id,upstream_pid,upstream_cycle,name,description,stock,hidden,profit_type,profit_value,requires_identity`

func scanProduct(rows *sql.Rows, pr *Product) error {
	return rows.Scan(&pr.ID, &pr.TypeID, &pr.ServerID, &pr.UpstreamPID, &pr.UpstreamCycle,
		&pr.Name, &pr.Description, &pr.Stock, &pr.Hidden, &pr.ProfitType, &pr.ProfitValue, &pr.RequiresIdentity)
}

// ListAll 后台用：包含隐藏产品
func (p *Products) ListAll(ctx context.Context) ([]Product, error) {
	rows, err := p.db.QueryContext(ctx,
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

// ListAdmin 一次读取后台产品列表所需的价格、配置和利润回退数据。
func (p *Products) ListAdmin(ctx context.Context, pricesetID int64) ([]AdminProductRow, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT p.id, p.name,
		       CASE WHEN t.parent_id <> 0 AND parent.id IS NOT NULL
		            THEN parent.name || '/' || t.name
		            ELSE coalesce(t.name, '') END,
		       coalesce(s.name, ''), p.upstream_pid,
		       coalesce(pp.monthly::text, ''), p.hidden, p.requires_identity,
		       coalesce(p.configoption::text, '[]'),
		       p.profit_type, p.profit_value,
		       coalesce(s.profit_type, 0), coalesce(s.profit_value, 0)
		FROM products p
		LEFT JOIN product_types t ON t.id = p.type_id
		LEFT JOIN product_types parent ON parent.id = t.parent_id
		LEFT JOIN product_prices pp ON pp.product_id = p.id AND pp.priceset_id = $1
		LEFT JOIN servers s ON s.id = p.server_id
		ORDER BY p.id`, pricesetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AdminProductRow
	for rows.Next() {
		var row AdminProductRow
		var raw string
		var productProfitType, serverProfitType int16
		var productProfitValue, serverProfitValue float64
		if err := rows.Scan(&row.ID, &row.Name, &row.TypeName, &row.ServerName, &row.UpstreamPID, &row.Monthly, &row.Hidden, &row.RequiresIdentity,
			&raw, &productProfitType, &productProfitValue, &serverProfitType, &serverProfitValue); err != nil {
			return nil, err
		}
		if productProfitValue > 0 {
			row.ProfitType = productProfitType
			row.ProfitValue = productProfitValue
		} else {
			row.ProfitType = serverProfitType
			row.ProfitValue = serverProfitValue
		}
		if err := json.Unmarshal([]byte(raw), &row.Options); err != nil {
			row.Options = nil
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListVisibleByTypes 前台按分类集合取可见产品（当前仅传单 ID；保留集合签名便于后续组合展示）。
func (p *Products) ListVisibleByTypes(ctx context.Context, typeIDs []int64) ([]Product, error) {
	if len(typeIDs) == 0 {
		return nil, nil
	}
	rows, err := p.db.QueryContext(ctx,
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
	err := p.db.QueryRowContext(ctx, `
		SELECT p.hidden=false AND t.id IS NOT NULL AND t.hidden=false
			AND (t.parent_id=0 OR EXISTS (
				SELECT 1 FROM product_types parent WHERE parent.id=t.parent_id AND parent.hidden=false
			))
		FROM products p LEFT JOIN product_types t ON t.id=p.type_id WHERE p.id=$1`, id).Scan(&ok)
	return ok, err
}
func (p *Products) Get(ctx context.Context, id int64) (*Product, error) {
	var pr Product
	err := p.db.QueryRowContext(ctx,
		productCols+` FROM products WHERE id=$1`, id).
		Scan(&pr.ID, &pr.TypeID, &pr.ServerID, &pr.UpstreamPID, &pr.UpstreamCycle,
			&pr.Name, &pr.Description, &pr.Stock, &pr.Hidden, &pr.ProfitType, &pr.ProfitValue, &pr.RequiresIdentity)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &pr, err
}

func (p *Products) Create(ctx context.Context, typeID sql.NullInt64, name, description string, stock int) (int64, error) {
	var id int64
	err := p.db.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,description,stock,requires_identity) VALUES($1,$2,$3,$4,false) RETURNING id`,
		typeID, name, description, stock).Scan(&id)
	return id, err
}

// SetRequiresIdentity 设置产品购买前是否必须通过实名。
func (p *Products) SetRequiresIdentity(ctx context.Context, productID int64, required bool) error {
	_, err := p.db.ExecContext(ctx, `UPDATE products SET requires_identity=$2 WHERE id=$1`, productID, required)
	return err
}

func (p *Products) Update(ctx context.Context, id int64, typeID sql.NullInt64, name, description string, stock int, hidden bool) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE products SET type_id=$2,name=$3,description=$4,stock=$5,hidden=$6 WHERE id=$1`,
		id, typeID, name, description, stock, hidden)
	return err
}

// SetBinding 更新产品上游绑定。
func (p *Products) SetBinding(ctx context.Context, productID int64, serverID sql.NullInt64, upstreamPID int64) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE products SET server_id=$2,upstream_pid=$3 WHERE id=$1`,
		productID, serverID, upstreamPID)
	return err
}

// SetProfit 设置产品利润方式（0百分比/1固定金额）与值。
func (p *Products) SetProfit(ctx context.Context, productID int64, profitType int16, profitValue float64) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE products SET profit_type=$2,profit_value=$3 WHERE id=$1`,
		productID, profitType, profitValue)
	return err
}

// ServerProfitFallback 返回产品所绑服务器上的默认利润（profit_type/profit_value）。
// 供前台计价在产品未单独设置利润时回退；无绑定或无设置时返回 0,0。
// 与旧 handler 内两个查询的语义一致：type>0 与 value>0 各自独立判定。
func (p *Products) ServerProfitFallback(ctx context.Context, productID int64) (int16, float64) {
	var sid sql.NullInt64
	if err := p.db.QueryRowContext(ctx, `SELECT server_id FROM products WHERE id=$1`, productID).Scan(&sid); err != nil || !sid.Valid {
		return 0, 0
	}
	var st int16
	if err := p.db.QueryRowContext(ctx, `SELECT coalesce(profit_type,0) FROM servers WHERE id=$1`, sid.Int64).Scan(&st); err != nil || st <= 0 {
		st = 0
	}
	var sv float64
	if err := p.db.QueryRowContext(ctx, `SELECT coalesce(profit_value,0) FROM servers WHERE id=$1`, sid.Int64).Scan(&sv); err != nil || sv <= 0 {
		sv = 0
	}
	return st, sv
}

func (p *Products) Delete(ctx context.Context, id int64) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM products WHERE id=$1`, id)
	return err
}

// ---------- 分类管理 ----------

func (p *Products) ListTypes(ctx context.Context) ([]ProductType, error) {
	rows, err := p.db.QueryContext(ctx,
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
	err := p.db.QueryRowContext(ctx,
		`INSERT INTO product_types(name,description,sort,parent_id,hidden) VALUES($1,$2,$3,$4,$5) RETURNING id`,
		name, description, sort, parentID, hidden).Scan(&id)
	return id, err
}

func (p *Products) UpdateType(ctx context.Context, id int64, name, description string, sort int, parentID int64, hidden bool) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE product_types SET name=$2,description=$3,sort=$4,parent_id=$5,hidden=$6 WHERE id=$1`,
		id, name, description, sort, parentID, hidden)
	return err
}

// TypeProductCounts 各分类直挂产品数（不含子分类）。
func (p *Products) TypeProductCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := p.db.QueryContext(ctx,
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
	res, err := p.db.ExecContext(ctx,
		`UPDATE products SET type_id=$2 WHERE type_id=$1`, from, to)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (p *Products) DeleteType(ctx context.Context, id int64) error {
	// 有子分类或有产品均拒绝删除（对齐 ZJMF 删除保护）
	var n int
	if err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM product_types WHERE parent_id=$1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrTypeHasChildren
	}
	if err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM products WHERE type_id=$1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrTypeInUse
	}
	_, err := p.db.ExecContext(ctx, `DELETE FROM product_types WHERE id=$1`, id)
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
	err := p.db.QueryRowContext(ctx,
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
	if err := p.db.QueryRowContext(ctx, `SELECT min(id) FROM pricesets`).Scan(&id); err != nil {
		return 0, err
	}
	if !id.Valid {
		return 0, ErrNotFound
	}
	return id.Int64, nil
}

// ProductSellProfit 返回产品计价利润（产品未设置时回退服务器默认，与 CreateOrder 口径一致）。
func (p *Products) ProductSellProfit(ctx context.Context, productID int64) (profitType int16, profitValue float64, err error) {
	if err = p.db.QueryRowContext(ctx, `SELECT profit_type,profit_value FROM products WHERE id=$1`, productID).Scan(&profitType, &profitValue); err != nil {
		return 0, 0, err
	}
	if profitValue <= 0 {
		var sid sql.NullInt64
		if err := p.db.QueryRowContext(ctx, `SELECT server_id FROM products WHERE id=$1`, productID).Scan(&sid); err != nil {
			return 0, 0, err
		}
		if sid.Valid {
			if err := p.db.QueryRowContext(ctx, `SELECT coalesce(profit_type,0),coalesce(profit_value,0) FROM servers WHERE id=$1`, sid.Int64).Scan(&profitType, &profitValue); err != nil {
				return 0, 0, err
			}
		} else {
			profitType, profitValue = 0, 0
		}
	}
	return profitType, profitValue, nil
}

// BoundProduct 上游已绑定产品的同步所需字段。
type BoundProduct struct {
	ID          int64
	ServerID    int64
	UpstreamPID int64
}

// ListBound 返回所有绑定了上游（server_id + upstream_pid）的产品。
func (p *Products) ListBound(ctx context.Context) ([]BoundProduct, error) {
	rows, err := p.db.QueryContext(ctx,
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
	tx, err := p.db.BeginTx(ctx, nil)
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

// UpsertPrice 按价格组 upsert 产品月/季/年价（价格保留调用方原文）。
func (p *Products) UpsertPrice(ctx context.Context, productID, pricesetID int64, monthly, quarterly, yearly string) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO product_prices(product_id,priceset_id,monthly,quarterly,yearly) VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (product_id,priceset_id) DO UPDATE SET monthly=$3,quarterly=$4,yearly=$5`,
		productID, pricesetID, monthly, quarterly, yearly)
	return err
}

// SetDescription 更新产品描述（上游导入幂等刷新用）。
func (p *Products) SetDescription(ctx context.Context, productID int64, desc string) error {
	_, err := p.db.ExecContext(ctx, `UPDATE products SET description=$2 WHERE id=$1`, productID, desc)
	return err
}

// SetStock 更新产品库存。
func (p *Products) SetStock(ctx context.Context, productID int64, stock int) error {
	_, err := p.db.ExecContext(ctx, `UPDATE products SET stock=$2 WHERE id=$1`, productID, stock)
	return err
}

// FindByUpstreamPID 按 (server_id, upstream_pid) 反查已对接本地产品 id；未找到返回 0,nil。
func (p *Products) FindByUpstreamPID(ctx context.Context, serverID, upstreamPID int64) (int64, error) {
	var id int64
	err := p.db.QueryRowContext(ctx,
		`SELECT id FROM products WHERE server_id=$1 AND upstream_pid=$2 LIMIT 1`, serverID, upstreamPID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// LinkedUpstreamPIDs 该上游服务器已对接的本地产品上游 PID 集合（目录页标注“已对接”）。
func (p *Products) LinkedUpstreamPIDs(ctx context.Context, serverID int64) (map[int]bool, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT upstream_pid FROM products WHERE server_id=$1 AND upstream_pid>0`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var pid int
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		out[pid] = true
	}
	return out, rows.Err()
}
