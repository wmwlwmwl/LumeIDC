package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"lumeidc/internal/money"
	"lumeidc/internal/repo"
)

type Orders struct {
	DB       *sql.DB
	Products *repo.Products
	Coupons  *repo.Coupons
}

var cycleCol = map[string]string{
	"monthly": "monthly", "quarterly": "quarterly", "yearly": "yearly",
}

// CreateOrder validates product/priceset/cycle and creates order + unpaid invoice atomically.
// selection 用户提交的配置选择，键为 field（cfg_ 前缀已由 handler 剥离）。
// couponCode 可选优惠码；有效时按规则抵扣并写入使用记录。
func (o *Orders) CreateOrder(ctx context.Context, userID, productID, pricesetID int64, cycle string, selection map[string]string, couponCode string) (orderID, invoiceID int64, amount string, err error) {
	col, ok := cycleCol[cycle]
	if !ok {
		return 0, 0, "", fmt.Errorf("无效的计费周期: %s", cycle)
	}
	tx, err := o.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, "", err
	}
	defer tx.Rollback()

	var stock int
	err = tx.QueryRowContext(ctx, `SELECT stock FROM products WHERE id=$1 AND hidden=false FOR UPDATE`, productID).Scan(&stock)
	if err != nil {
		return 0, 0, "", fmt.Errorf("商品不存在或已下架")
	}
	if stock > 0 {
		var reserved int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM stock_reservations WHERE product_id=$1 AND status='reserved' AND expires_at>now()`,
			productID).Scan(&reserved); err != nil {
			return 0, 0, "", err
		}
		if reserved >= stock {
			return 0, 0, "", fmt.Errorf("商品已售罄")
		}
	} else if stock == 0 {
		return 0, 0, "", fmt.Errorf("商品已售罄")
	}
	var amountRaw string
	query := fmt.Sprintf(`SELECT %s FROM product_prices WHERE product_id=$1 AND priceset_id=$2`, col)
	if err := tx.QueryRowContext(ctx, query, productID, pricesetID).Scan(&amountRaw); err != nil {
		return 0, 0, "", fmt.Errorf("该商品未配置此价格")
	}
	base, err := strconv.ParseFloat(amountRaw, 64)
	if err != nil || base < 0 {
		return 0, 0, "", fmt.Errorf("商品价格无效")
	}

	// 服务端权威计价：只认产品声明的配置项，防篡改
	opts, err := o.Products.GetConfigOptions(ctx, productID)
	if err != nil {
		return 0, 0, "", fmt.Errorf("商品配置损坏，请联系管理员")
	}
	quote, qerr := CalculateQuote(opts, base, cycle, selection)
	if qerr != nil {
		return 0, 0, "", qerr
	}
	// 成本口径 = 基础价+配置费用；按产品利润设置加成出售（对齐 ZJMF 上游百分比语义）。
	var profitType int16
	var profitValue float64
	if err := tx.QueryRowContext(ctx,
		`SELECT profit_type,profit_value FROM products WHERE id=$1`, productID).Scan(&profitType, &profitValue); err != nil {
		return 0, 0, "", err
	}
	// 产品未设置利润时回退服务器默认
	if profitValue <= 0 {
		var sid sql.NullInt64
		tx.QueryRowContext(ctx, `SELECT server_id FROM products WHERE id=$1`, productID).Scan(&sid)
		if sid.Valid {
			tx.QueryRowContext(ctx, `SELECT coalesce(profit_type,0) FROM servers WHERE id=$1`, sid.Int64).Scan(&profitType)
			tx.QueryRowContext(ctx, `SELECT coalesce(profit_value,0) FROM servers WHERE id=$1`, sid.Int64).Scan(&profitValue)
		}
	}
	cost := mathRound(quote.Total)
	sell := mathRound(applyProfit(cost, profitType, profitValue))
	finalAmount := strconv.FormatFloat(sell, 'f', 2, 64)

	// 优惠码抵扣：在订单事务内锁定并校验，避免并发超发；提前到建单前，失败不留孤儿订单。
	var couponID int64
	var couponDiscount string
	if couponCode != "" && o.Coupons != nil {
		cid, discount, cerr := o.Coupons.Validate(ctx, tx, couponCode, userID, finalAmount)
		if cerr != nil {
			return 0, 0, "", cerr
		}
		couponID = cid
		couponDiscount = discount
		finalAmount = subtractAmount(finalAmount, discount)
	}

	// 毛利 = 加成额（优惠前口径），下单时落库，后续改比例不影响历史订单。
	profit := strconv.FormatFloat(sell-cost, 'f', 2, 64)

	err = tx.QueryRowContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,profit) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
		userID, productID, pricesetID, cycle, finalAmount, profit).Scan(&orderID)
	if err != nil {
		return 0, 0, "", err
	}
	// 配置快照落库
	snap, err := json.Marshal(map[string]any{"quote": quote, "selection": selection})
	if err != nil {
		return 0, 0, "", err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE orders SET config_snapshot=$2 WHERE id=$1`, orderID, snap); err != nil {
		return 0, 0, "", err
	}
	no, err := genInvoiceNo()
	if err != nil {
		return 0, 0, "", err
	}
	err = tx.QueryRowContext(ctx,
		`INSERT INTO invoices(no,user_id,order_id,amount) VALUES($1,$2,$3,$4) RETURNING id`,
		no, userID, orderID, finalAmount).Scan(&invoiceID)
	if err != nil {
		return 0, 0, "", err
	}
	// 写入优惠码使用记录（已在校验阶段锁定）
	if couponID > 0 {
		if e := o.Coupons.Use(ctx, tx, couponID, userID, orderID, couponDiscount); e != nil {
			return 0, 0, "", e
		}
	}
	if stock > 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO stock_reservations(order_id,product_id,expires_at) VALUES($1,$2,now()+interval '30 minutes')`,
			orderID, productID); err != nil {
			return 0, 0, "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, "", err
	}
	return orderID, invoiceID, finalAmount, nil
}

// CreateRechargeInvoice 创建用户余额充值账单；充值账单不绑定产品订单。
func (o *Orders) CreateRechargeInvoice(ctx context.Context, userID int64, amount string) (int64, error) {
	canonical, _, err := money.ParsePositive(amount, 999999999999)
	if err != nil {
		return 0, fmt.Errorf("充值金额无效")
	}
	amount = canonical
	no, err := genInvoiceNo()
	if err != nil {
		return 0, err
	}
	var id int64
	err = o.DB.QueryRowContext(ctx,
		`INSERT INTO invoices(no,user_id,amount,kind) VALUES($1,$2,$3,'recharge') RETURNING id`,
		no, userID, amount).Scan(&id)
	return id, err
}

// applyProfit 利润加成（ZJMF 上游利润语义）：percent=成本×(1+比例%)，fixed=成本+固定金额。<=0 不加成。
func applyProfit(cost float64, profitType int16, profitValue float64) float64 {
	if profitValue <= 0 {
		return cost
	}
	if profitType == 1 {
		return cost + profitValue
	}
	return cost * (1 + profitValue/100)
}

func mathRound(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// subtractAmount 金额相减（元，两位小数），结果不为负。
func subtractAmount(a, b string) string {
	x, _ := strconv.ParseFloat(a, 64)
	y, _ := strconv.ParseFloat(b, 64)
	r := x - y
	if r < 0 {
		r = 0
	}
	return strconv.FormatFloat(r, 'f', 2, 64)
}

func genInvoiceNo() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("INV%s%s", time.Now().Format("20060102"), hex.EncodeToString(b)), nil
}

// CycleDuration maps a billing cycle to its period length.
// ponytail: 保留用于向后兼容的固定天数；新代码应使用 CycleAddDate 获取日历周期。
func CycleDuration(cycle string) (time.Duration, error) {
	switch cycle {
	case "monthly":
		return 30 * 24 * time.Hour, nil
	case "quarterly":
		return 90 * 24 * time.Hour, nil
	case "yearly":
		return 365 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("无效计费周期: %s", cycle)
}

// CycleAddDate 按日历周期计算到期时间：月/季/年使用 AddDate，避免固定天数导致的月底/闰年偏差。
func CycleAddDate(base time.Time, cycle string) (time.Time, error) {
	switch cycle {
	case "monthly":
		return base.AddDate(0, 1, 0), nil
	case "quarterly":
		return base.AddDate(0, 3, 0), nil
	case "yearly":
		return base.AddDate(1, 0, 0), nil
	}
	return base, fmt.Errorf("无效计费周期: %s", cycle)
}

// CycleInterval 返回 PostgreSQL interval 字符串，用于 SQL 中的日历周期计算。
func CycleInterval(cycle string) (string, error) {
	switch cycle {
	case "monthly":
		return "1 month", nil
	case "quarterly":
		return "3 months", nil
	case "yearly":
		return "1 year", nil
	}
	return "", fmt.Errorf("无效计费周期: %s", cycle)
}

// CreateRenewOrder 为既有服务生成续费订单+账单。
func (o *Orders) CreateRenewOrder(ctx context.Context, userID, serviceID int64, cycle string) (orderID, invoiceID int64, amount string, err error) {
	col, ok := cycleCol[cycle]
	if !ok {
		return 0, 0, "", fmt.Errorf("无效的计费周期: %s", cycle)
	}
	tx, err := o.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, "", err
	}
	defer tx.Rollback()

	// 锁定服务，串行化同一服务的续费建单，避免并发产生多张未支付账单。
	var serviceStatus int16
	if err := tx.QueryRowContext(ctx, `SELECT status FROM services WHERE id=$1 AND user_id=$2 FOR UPDATE`, serviceID, userID).Scan(&serviceStatus); err != nil {
		return 0, 0, "", fmt.Errorf("服务不存在或不可续费")
	}
	if serviceStatus != 1 && serviceStatus != 2 {
		return 0, 0, "", fmt.Errorf("服务不存在或不可续费")
	}

	// 防重复续费：若同一服务已存在未支付续费账单，直接复用，避免多次支付导致重复延期。
	var existOrderID, existInvoiceID int64
	var existAmount string
	err = tx.QueryRowContext(ctx,
		`SELECT o.id, i.id, i.amount FROM invoices i JOIN orders o ON o.id=i.order_id
		 WHERE o.service_id=$1 AND o.user_id=$2 AND i.status=0 AND o.cycle=$3
		 ORDER BY i.id DESC LIMIT 1`, serviceID, userID, cycle).
		Scan(&existOrderID, &existInvoiceID, &existAmount)
	if err == nil {
		return existOrderID, existInvoiceID, existAmount, nil
	}
	if err != sql.ErrNoRows {
		return 0, 0, "", err
	}

	// 校验服务归属 + 取产品 + 取该服务所属订单的实际成交额（含配置附加，如 NAT、数据盘）。
	// 配置计价型产品（弹性云/云电脑）基础价在 product_prices 存 0，仅读基础价会让续费单为 0 元，
	// 故续费金额优先取服务自己订单的成交额（与开局/详情页价一致）；无订单时兜底读产品基础价。
	var productID int64
	var ownAmt sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT sv.product_id, o.amount
		 FROM services sv LEFT JOIN orders o ON o.id=sv.order_id
		 WHERE sv.id=$1 AND sv.user_id=$2 AND sv.status IN (1,2)`,
		serviceID, userID).Scan(&productID, &ownAmt)
	if err != nil {
		return 0, 0, "", fmt.Errorf("服务不存在或不可续费")
	}
	psID, err := o.Products.DefaultPricesetID(ctx)
	if err != nil {
		return 0, 0, "", fmt.Errorf("系统未配置价格组")
	}
	// 续费金额：月付优先取服务订单成交额（配置计价型 base=0，成交额含配置价）；
	// 季付/年付必须产品有对应周期正价，防止以 0 价或月付额误续。
	var amountRaw string
	if cycle == "monthly" && ownAmt.Valid && ownAmt.String != "" {
		amountRaw = ownAmt.String
	} else {
		query := fmt.Sprintf(`SELECT %s FROM product_prices WHERE product_id=$1 AND priceset_id=$2`, col)
		if err := tx.QueryRowContext(ctx, query, productID, psID).Scan(&amountRaw); err != nil {
			return 0, 0, "", fmt.Errorf("该产品未配置%s价格", cycleCol[cycle])
		}
		f, _ := strconv.ParseFloat(amountRaw, 64)
		if f <= 0 {
			return 0, 0, "", fmt.Errorf("该产品未配置%s价格", cycleCol[cycle])
		}
		// 周期续费同按产品利润加成定价（月付取订单成交额，已含加成，勿重复加）。
		var pType int16
		var pVal float64
		if err := tx.QueryRowContext(ctx,
			`SELECT profit_type,profit_value FROM products WHERE id=$1`, productID).Scan(&pType, &pVal); err == nil {
			amountRaw = strconv.FormatFloat(mathRound(applyProfit(f, pType, pVal)), 'f', 2, 64)
		}
	}

	err = tx.QueryRowContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,service_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
		userID, productID, psID, cycle, amountRaw, serviceID).Scan(&orderID)
	if err != nil {
		return 0, 0, "", err
	}
	no, err := genInvoiceNo()
	if err != nil {
		return 0, 0, "", err
	}
	err = tx.QueryRowContext(ctx,
		`INSERT INTO invoices(no,user_id,order_id,amount) VALUES($1,$2,$3,$4) RETURNING id`,
		no, userID, orderID, amountRaw).Scan(&invoiceID)
	if err != nil {
		return 0, 0, "", err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, "", err
	}
	return orderID, invoiceID, amountRaw, nil
}
