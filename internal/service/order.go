package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/money"
	"lumeidc/internal/repo"
)

const invoiceDueInterval = "24 hours"

type Orders struct {
	db       *sql.DB
	Products *repo.Products
	Coupons  *repo.Coupons
	Identity interface {
		IsApproved(context.Context, int64) (bool, error)
	}
	// Upstream 下单前的上游实时价格校验（可为 nil：未装配时跳过校验）。
	Upstream *UpstreamGuard
	// Promotion 营销活动计价（可为 nil：未装配时跳过活动价）。
	Promotion *PromotionService
	Notifier  *Notifier
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
	// 上游实时价格校验：绑定了上游的商品必须先确认本地价没有落后于上游。
	// 必须放在事务之外——事务内不做网络调用。
	if o.Upstream != nil {
		if err := o.Upstream.VerifyBeforeOrder(ctx, productID, cycle, selection); err != nil {
			return 0, 0, "", err
		}
	}
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, "", err
	}
	defer tx.Rollback()
	// 同一用户的所有订单事务串行化，防止 new_user/limit_per_user 等活动校验并发绕过。
	// pg_advisory_xact_lock 事务结束自动释放，不持久化不持有连接。
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('user_order_lock:' || $1::text))`, userID); err != nil {
		return 0, 0, "", fmt.Errorf("获取订单锁失败: %w", err)
	}

	var stock int
	var productName string
	var requiresIdentity bool
	err = tx.QueryRowContext(ctx, `SELECT p.stock,p.name,p.requires_identity FROM products p JOIN product_types t ON t.id=p.type_id
		WHERE p.id=$1 AND p.hidden=false AND p.upstream_offline_reason='' AND t.hidden=false AND (t.parent_id=0 OR EXISTS (SELECT 1 FROM product_types parent WHERE parent.id=t.parent_id AND parent.hidden=false)) FOR UPDATE`, productID).Scan(&stock, &productName, &requiresIdentity)
	if err != nil {
		return 0, 0, "", fmt.Errorf("商品已下架")
	}
	if requiresIdentity {
		if o.Identity == nil {
			return 0, 0, "", fmt.Errorf("实名服务未配置")
		}
		approved, checkErr := o.Identity.IsApproved(ctx, userID)
		if checkErr != nil {
			return 0, 0, "", fmt.Errorf("实名状态查询失败，请稍后再试")
		}
		if !approved {
			return 0, 0, "", ErrIdentityRequired
		}
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
	// 周期可售性：有基础价的产品只能购买已配置的周期；纯配置计价/免费产品保留月付入口。
	var monthly, quarterly, yearly float64
	for name, value := range map[string]string{"monthly": "monthly", "quarterly": "quarterly", "yearly": "yearly"} {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT `+value+` FROM product_prices WHERE product_id=$1 AND priceset_id=$2`, productID, pricesetID).Scan(&raw); err == nil {
			v, _ := strconv.ParseFloat(raw, 64)
			switch name {
			case "monthly":
				monthly = v
			case "quarterly":
				quarterly = v
			case "yearly":
				yearly = v
			}
		}
	}
	if cycles, _ := AvailableCycles(monthly, quarterly, yearly); len(cycles) > 0 && base <= 0 {
		return 0, 0, "", fmt.Errorf("该产品未提供所选计费周期")
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
	// 成本口径 = 基础价 + 配置费用 + 初装费（初装费是上游一次性费用，仅首购收取）；
	// 按产品利润设置加成出售（对齐 ZJMF 上游百分比语义）。
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
	cost := mathRound(quote.PayableOnce())
	sell := mathRound(applyProfit(cost, profitType, profitValue))

	var promoID, promoProductID sql.NullInt64
	var promoType string
	var promoDiscount float64
	if o.Promotion != nil {
		ap, apErr := o.Promotion.ActivePromotionFor(ctx, productID, pricesetID, cycle)
		if apErr != nil {
			return 0, 0, "", fmt.Errorf("查询活动失败: %w", apErr)
		}
		if ap != nil {
			if apErr := o.Promotion.ValidatePromotionApplicable(ctx, ap, userID); apErr != nil {
				return 0, 0, "", apErr
			}
			if apErr := o.Promotion.CheckQuotaAndLimit(ctx, tx, ap, userID, ap.LimitPerUser); apErr != nil {
				return 0, 0, "", apErr
			}
			promoFinal, promoDisc := o.Promotion.ApplyPromotion(sell, ap)
			sell = promoFinal
			promoDiscount = promoDisc
			promoType = ap.Type
			promoID.Int64 = ap.PromotionID
			promoID.Valid = true
			promoProductID.Int64 = ap.PromotionProductID
			promoProductID.Valid = true
			if ap.Type == "coupon_giveaway" {
				couponCode = fmt.Sprintf("PROMO_%d_%d", ap.PromotionID, userID)
			} else if ap.Type != "full_reduction" {
				couponCode = ""
			}
		}
	}
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
	// 0 元订单：仅当产品该周期真实起步价（基础价+最低配置价，DisplayPrice）也为 0 时才是“纯免费产品”，
	// 放行并交给下单处自动核销开通；否则 0 元说明计价配置被绕过（未提交必填/计价的 CPU、内存等）
	// 或优惠过度，属于 0 元购漏洞，一律拒绝。
	if finalAmount == "0.00" && DisplayPrice(base, opts, profitType, profitValue) > 0 {
		return 0, 0, "", fmt.Errorf("订单金额为 0，无法下单：所选配置或计费周期无有效价格")
	}

	// 毛利 = 加成额（优惠前口径），下单时落库，后续改比例不影响历史订单。
	profit := strconv.FormatFloat(sell-cost, 'f', 2, 64)

	err = tx.QueryRowContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,profit,identity_required,
		        promotion_id,promo_product_id,promo_type,promo_discount)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		userID, productID, pricesetID, cycle, finalAmount, profit, requiresIdentity,
		promoID, promoProductID, promoType, promoDiscount).Scan(&orderID)
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
		`INSERT INTO invoices(no,user_id,order_id,amount,due_at) VALUES($1,$2,$3,$4,now()+$5::interval) RETURNING id`,
		no, userID, orderID, finalAmount, invoiceDueInterval).Scan(&invoiceID)
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
	if o.Notifier != nil {
		body := fmt.Sprintf("订单 %d 已提交，商品：%s，应付金额：%s 元，请在 24 小时内完成支付。", orderID, productName, finalAmount)
		if err := o.Notifier.NotifyTemplate(ctx, userID, "order_submitted", "订单已提交", body, map[string]string{"order_id": strconv.FormatInt(orderID, 10), "product_name": productName, "amount": finalAmount, "invoice_no": no}); err != nil {
			log.Printf("订单提交通知入队失败，订单编号=%d: %v", orderID, err)
		}
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
	err = o.db.QueryRowContext(ctx,
		`INSERT INTO invoices(no,user_id,amount,kind,due_at) VALUES($1,$2,$3,'recharge',now()+$4::interval) RETURNING id`,
		no, userID, amount, invoiceDueInterval).Scan(&id)
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

// mathRound 金额四舍五入到分：math.Round 对负数同样按半值远离零处理，
// 旧实现 int64(v*100+0.5) 对负数是向零截断，降级差价会有 ±1 分偏差。
func mathRound(v float64) float64 {
	return math.Round(v*100) / 100
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
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, "", err
	}
	defer tx.Rollback()
	// 同一用户的所有订单事务串行化（与 CreateOrder 共用同一 key），避免活动校验并发绕过。
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('user_order_lock:' || $1::text))`, userID); err != nil {
		return 0, 0, "", fmt.Errorf("获取订单锁失败: %w", err)
	}

	// 锁定服务，串行化同一服务的续费建单，避免并发产生多张未支付账单。
	var serviceStatus int16
	var svcTransition string
	if err := tx.QueryRowContext(ctx,
		`SELECT status, coalesce(transition_state,'') FROM services WHERE id=$1 AND user_id=$2 FOR UPDATE`,
		serviceID, userID).Scan(&serviceStatus, &svcTransition); err != nil {
		return 0, 0, "", fmt.Errorf("服务不存在或不可续费")
	}
	if serviceStatus != 1 && serviceStatus != 2 {
		return 0, 0, "", fmt.Errorf("服务不存在或不可续费")
	}
	// 有过进行中的流程（升级中 / 上次续费待人工处置）时不建单：钱可能已收，
	// 再下一单会造成重复付费与重复续期。
	if svcTransition != "" {
		return 0, 0, "", fmt.Errorf("服务正在处理中，请稍后再试")
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

	// 取产品 + 保存的初购配置 + 管理员设置的固定续费价覆盖（NULL=跟随产品价）。
	var productID int64
	var snap []byte
	var ovM, ovQ, ovY sql.NullFloat64
	err = tx.QueryRowContext(ctx,
		`SELECT sv.product_id, coalesce(sv.config_snapshot, o.config_snapshot),
		        sv.renew_monthly, sv.renew_quarterly, sv.renew_yearly
		 FROM services sv LEFT JOIN orders o ON o.id=sv.order_id
		 WHERE sv.id=$1 AND sv.user_id=$2 AND sv.status IN (1,2)`,
		serviceID, userID).Scan(&productID, &snap, &ovM, &ovQ, &ovY)
	if err != nil {
		return 0, 0, "", fmt.Errorf("服务不存在或不可续费")
	}
	var requiresIdentity bool
	if err := tx.QueryRowContext(ctx, `SELECT requires_identity FROM products WHERE id=$1`, productID).Scan(&requiresIdentity); err != nil {
		return 0, 0, "", fmt.Errorf("商品不存在")
	}
	if requiresIdentity {
		if o.Identity == nil {
			return 0, 0, "", fmt.Errorf("实名服务未配置")
		}
		approved, checkErr := o.Identity.IsApproved(ctx, userID)
		if checkErr != nil {
			return 0, 0, "", fmt.Errorf("实名状态查询失败，请稍后再试")
		}
		if !approved {
			return 0, 0, "", ErrIdentityRequired
		}
	}
	psID, err := o.Products.DefaultPricesetID(ctx)
	if err != nil {
		return 0, 0, "", fmt.Errorf("系统未配置价格组")
	}
	// 续费金额：按产品当前价 × 保存的初购配置 × 产品利润重算，不继承下单时的一次性优惠
	// 与订单成交额；管理员设了固定续费价（renew_*）时直接采用。季付/年付必须产品有对应周期正价。
	query := fmt.Sprintf(`SELECT %s FROM product_prices WHERE product_id=$1 AND priceset_id=$2`, col)
	var baseRaw string
	if err := tx.QueryRowContext(ctx, query, productID, psID).Scan(&baseRaw); err != nil {
		return 0, 0, "", fmt.Errorf("该产品未配置%s价格", cycleCol[cycle])
	}
	base, err := strconv.ParseFloat(baseRaw, 64)
	if err != nil || !money.FiniteNonNegative(base) {
		return 0, 0, "", fmt.Errorf("商品价格无效")
	}
	// 周期可售性：与新购同口径；纯配置计价/免费产品保留月付入口。
	var monthly, quarterly, yearly float64
	for name, value := range map[string]string{"monthly": "monthly", "quarterly": "quarterly", "yearly": "yearly"} {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT `+value+` FROM product_prices WHERE product_id=$1 AND priceset_id=$2`, productID, psID).Scan(&raw); err == nil {
			v, _ := strconv.ParseFloat(raw, 64)
			switch name {
			case "monthly":
				monthly = v
			case "quarterly":
				quarterly = v
			case "yearly":
				yearly = v
			}
		}
	}
	if cycles, _ := AvailableCycles(monthly, quarterly, yearly); len(cycles) > 0 && base <= 0 {
		return 0, 0, "", fmt.Errorf("该产品未提供所选计费周期")
	}
	selection := map[string]string{}
	if len(snap) > 0 {
		var saved struct {
			Selection map[string]string `json:"selection"`
		}
		_ = json.Unmarshal(snap, &saved)
		selection = saved.Selection
	}
	opts, err := o.Products.GetConfigOptions(ctx, productID)
	if err != nil {
		return 0, 0, "", fmt.Errorf("商品配置损坏，请联系管理员")
	}
	quote, err := CalculateQuote(opts, base, cycle, selection)
	if err != nil {
		return 0, 0, "", err
	}
	// 续费只收周期费：上游初装费（quote.Setup）是一次性费用，仅首购收取，这里刻意用 Total。
	var pType int16
	var pVal float64
	if err := tx.QueryRowContext(ctx, `SELECT profit_type,profit_value FROM products WHERE id=$1`, productID).Scan(&pType, &pVal); err != nil {
		return 0, 0, "", err
	}
	amountRaw := strconv.FormatFloat(mathRound(applyProfit(quote.Total, pType, pVal)), 'f', 2, 64)
	// 管理员固定续费价优先（后台「编辑服务」设置；NULL/0 表示跟随产品价）
	var ov *float64
	switch cycle {
	case "monthly":
		if ovM.Valid {
			v := ovM.Float64
			ov = &v
		}
	case "quarterly":
		if ovQ.Valid {
			v := ovQ.Float64
			ov = &v
		}
	case "yearly":
		if ovY.Valid {
			v := ovY.Float64
			ov = &v
		}
	}
	if ov != nil && *ov > 0 {
		amountRaw = strconv.FormatFloat(mathRound(*ov), 'f', 2, 64)
	}
	// 金额为 0 不再拒绝：月付价为 0 的免费商品同样要能续费（0 元单由调用方直接核销并延期）。
	// 季/年付为 0 属于"未配置该周期"，已在上面拦掉，不会走到这里。
	// 配置快照落库：续费前比价要用"下单时的成本额"（quote.total）当基准，
	// 缺失会让上游涨价时无从核对。
	snap, err = json.Marshal(map[string]any{"quote": quote, "selection": selection})
	if err != nil {
		return 0, 0, "", err
	}

	err = tx.QueryRowContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,service_id,identity_required,config_snapshot)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		userID, productID, psID, cycle, amountRaw, serviceID, requiresIdentity, snap).Scan(&orderID)
	if err != nil {
		return 0, 0, "", err
	}
	no, err := genInvoiceNo()
	if err != nil {
		return 0, 0, "", err
	}
	err = tx.QueryRowContext(ctx,
		`INSERT INTO invoices(no,user_id,order_id,amount,due_at) VALUES($1,$2,$3,$4,now()+$5::interval) RETURNING id`,
		no, userID, orderID, amountRaw, invoiceDueInterval).Scan(&invoiceID)
	if err != nil {
		return 0, 0, "", err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, "", err
	}
	return orderID, invoiceID, amountRaw, nil
}

// CreateUpgradeOrder 服务升降级（本地为主）：目标产品须与当前服务同服务器。
// diff>0 升级补差价；diff<0 降级退余额（amount=0，支付时退 abs(diff) 到余额）。
// 差额口径 = 目标月售价 − 当前月售价（MonthlySellPrice，两侧 monthly 归一；不含剩余天数折算，见 ponytail）。
// frozenRenewPrice 取服务在指定周期上的自定义续费价（后台编辑服务时可单独设置，054 迁移）。
// 未设置或为空返回空串，调用方回退到产品周期价。
func frozenRenewPrice(cycle string, m, q, y sql.NullString) string {
	var v sql.NullString
	switch cycle {
	case "quarterly":
		v = q
	case "yearly":
		v = y
	default:
		v = m
	}
	if !v.Valid {
		return ""
	}
	return strings.TrimSpace(v.String)
}

func (o *Orders) CreateUpgradeOrder(ctx context.Context, userID, serviceID, targetProductID int64, cycle string, selection map[string]string) (orderID, invoiceID int64, amount string, diff float64, err error) {
	col, ok := cycleCol[cycle]
	if !ok {
		return 0, 0, "", 0, fmt.Errorf("无效的计费周期: %s", cycle)
	}
	// 上游实时价格校验：与新购下单同一套逻辑，必须先确认目标商品的本地价没落后于上游，
	// 否则差价按旧价算出来、到开通时才被上游账单打回（转人工）。放在事务之外——事务内不做网络调用。
	// 只校验目标商品：当前商品是已购的，价格在下单时就已锁定。
	if o.Upstream != nil {
		if err := o.Upstream.VerifyBeforeOrder(ctx, targetProductID, cycle, selection); err != nil {
			return 0, 0, "", 0, err
		}
	}
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, "", 0, err
	}
	defer tx.Rollback()
	// 同一用户的所有订单事务串行化（与 CreateOrder 共用同一 key），避免活动校验并发绕过。
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('user_order_lock:' || $1::text))`, userID); err != nil {
		return 0, 0, "", 0, err
	}

	// 锁定服务，串行化同一服务的升降级建单
	var svcStatus int16
	var svcProductID int64
	var svcCycle, svcSnap, svcTransition string
	var svcOrderID sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT status, product_id, coalesce(cycle,''), coalesce(config_snapshot::text,''),
		        order_id, coalesce(transition_state,'')
		 FROM services WHERE id=$1 AND user_id=$2 FOR UPDATE`,
		serviceID, userID).Scan(&svcStatus, &svcProductID, &svcCycle, &svcSnap, &svcOrderID, &svcTransition)
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("服务不存在或不可升降级")
	}
	if svcStatus != 1 && svcStatus != 2 {
		return 0, 0, "", 0, fmt.Errorf("服务当前状态不可升降级")
	}
	if svcTransition != "" {
		return 0, 0, "", 0, fmt.Errorf("服务正在操作中，请稍后再试")
	}
	if targetProductID == svcProductID {
		return 0, 0, "", 0, fmt.Errorf("目标产品与当前产品相同")
	}

	// 防抖：复用同服务未支付升级单
	err = tx.QueryRowContext(ctx,
		`SELECT o.id, i.id, i.amount, o.diff_amount::float8 FROM invoices i JOIN orders o ON o.id=i.order_id
		 WHERE o.service_id=$1 AND o.user_id=$2 AND o.kind='upgrade' AND i.status=0
		 ORDER BY i.id DESC LIMIT 1`, serviceID, userID).
		Scan(&orderID, &invoiceID, &amount, &diff)
	if err == nil {
		return orderID, invoiceID, amount, diff, nil
	}
	if err != sql.ErrNoRows {
		return 0, 0, "", 0, err
	}

	// 目标产品：可售 + 与当前服务同服务器（本地升降级前提：同一上游实例）
	targetRepo, err := o.Products.Get(ctx, targetProductID)
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("目标产品不存在")
	}
	if targetRepo.Hidden {
		return 0, 0, "", 0, fmt.Errorf("目标产品已下架")
	}
	// 上游已下架：与新建订单同口径——上游停售的商品不能再作为升级目标，
	// 否则升级成功但上游无法交付。
	if targetRepo.UpstreamOfflineReason != "" {
		return 0, 0, "", 0, fmt.Errorf("目标产品已被上游下架")
	}
	// 库存校验（与 CreateOrder 同口径：stock>0 扣未过期预留，stock=0 视为售罄，<0 不限）
	if targetRepo.Stock > 0 {
		var reserved int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM stock_reservations WHERE product_id=$1 AND status='reserved' AND expires_at>now()`,
			targetProductID).Scan(&reserved); err != nil {
			return 0, 0, "", 0, fmt.Errorf("库存校验失败")
		}
		if reserved >= targetRepo.Stock {
			return 0, 0, "", 0, fmt.Errorf("目标产品已售罄")
		}
	} else if targetRepo.Stock == 0 {
		return 0, 0, "", 0, fmt.Errorf("目标产品已售罄")
	}
	var curServerID sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT coalesce(sv.server_id, p.server_id) FROM services sv JOIN products p ON p.id=sv.product_id WHERE sv.id=$1`,
		serviceID).Scan(&curServerID); err != nil || !curServerID.Valid {
		return 0, 0, "", 0, fmt.Errorf("该服务暂不支持升降级")
	}
	if !targetRepo.ServerID.Valid || targetRepo.ServerID.Int64 != curServerID.Int64 {
		return 0, 0, "", 0, fmt.Errorf("目标产品与当前服务不匹配，无法升降级")
	}

	var requiresIdentity bool
	if err := tx.QueryRowContext(ctx, `SELECT requires_identity FROM products WHERE id=$1`, targetProductID).Scan(&requiresIdentity); err != nil {
		return 0, 0, "", 0, fmt.Errorf("目标产品不存在")
	}
	if requiresIdentity {
		if o.Identity == nil {
			return 0, 0, "", 0, fmt.Errorf("实名服务未配置")
		}
		approved, checkErr := o.Identity.IsApproved(ctx, userID)
		if checkErr != nil {
			return 0, 0, "", 0, fmt.Errorf("实名状态查询失败，请稍后再试")
		}
		if !approved {
			return 0, 0, "", 0, ErrIdentityRequired
		}
	}

	psID, err := o.Products.DefaultPricesetID(ctx)
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("系统未配置价格组")
	}
	query := fmt.Sprintf(`SELECT %s FROM product_prices WHERE product_id=$1 AND priceset_id=$2`, col)
	var targetBaseRaw string
	if err := tx.QueryRowContext(ctx, query, targetProductID, psID).Scan(&targetBaseRaw); err != nil {
		return 0, 0, "", 0, fmt.Errorf("目标产品未配置%s价格", cycleCol[cycle])
	}
	targetBase, err := strconv.ParseFloat(targetBaseRaw, 64)
	if err != nil || !money.FiniteNonNegative(targetBase) {
		return 0, 0, "", 0, fmt.Errorf("目标产品价格无效")
	}
	if (cycle == "quarterly" || cycle == "yearly") && targetBase <= 0 {
		return 0, 0, "", 0, fmt.Errorf("目标产品未提供所选计费周期")
	}

	// 差价：按剩余天数折算（proration）。
	// 旧实现固定按月口径相减（目标月价 − 当前月价），用户选季付/年付时收的仍只是
	// "一个月的差价"，与实际周期严重不符。现按未使用天数折算，两侧各用自身周期的日价。
	curSelection := map[string]string{}
	if svcSnap != "" {
		var saved struct {
			Selection map[string]string `json:"selection"`
		}
		_ = json.Unmarshal([]byte(svcSnap), &saved)
		curSelection = saved.Selection
	}
	var expiresAt time.Time
	var renewM, renewQ, renewY sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT expires_at, renew_monthly::text, renew_quarterly::text, renew_yearly::text
		 FROM services WHERE id=$1`, serviceID).Scan(&expiresAt, &renewM, &renewQ, &renewY); err != nil {
		return 0, 0, "", 0, fmt.Errorf("读取服务到期时间失败")
	}
	// 剩余天数向上取整：不足一天按一天，避免临到期时算出接近 0 的差价被当成"等价"拒绝。
	remainDays := math.Ceil(time.Until(expiresAt).Hours() / 24)
	if remainDays <= 0 {
		return 0, 0, "", 0, fmt.Errorf("服务已到期，请先续费后再升降级")
	}
	curCycle := svcCycle
	if curCycle == "" {
		curCycle = "monthly"
	}
	// 当前周期价：管理员为该服务单独指定的续费价优先（后台编辑服务可设），否则按产品周期价计算。
	curPrice := 0.0
	if v, perr := strconv.ParseFloat(frozenRenewPrice(curCycle, renewM, renewQ, renewY), 64); perr == nil && v > 0 {
		curPrice = v
	} else {
		curPrice, err = CycleSellPrice(ctx, o.Products, svcProductID, curCycle, curSelection)
		if err != nil {
			return 0, 0, "", 0, fmt.Errorf("计算当前价格失败: %w", err)
		}
	}
	tgtPrice, err := CycleSellPrice(ctx, o.Products, targetProductID, cycle, selection)
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("计算目标价格失败: %w", err)
	}
	diff, err = ProratedDiff(curPrice, curCycle, tgtPrice, cycle, remainDays)
	if err != nil {
		return 0, 0, "", 0, err
	}
	if diff == 0 {
		return 0, 0, "", 0, fmt.Errorf("目标配置与原配置等价，无需升降级")
	}

	// 目标配置快照（按所选周期计价，供详情/续费展示）
	targetOpts, err := o.Products.GetConfigOptions(ctx, targetProductID)
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("目标产品配置损坏")
	}
	targetQuote, err := CalculateQuote(targetOpts, targetBase, cycle, selection)
	if err != nil {
		return 0, 0, "", 0, err
	}
	snap, err := json.Marshal(map[string]any{"quote": targetQuote, "selection": selection})
	if err != nil {
		return 0, 0, "", 0, err
	}

	orderAmount := "0.00"
	if diff > 0 {
		orderAmount = strconv.FormatFloat(diff, 'f', 2, 64)
	}
	err = tx.QueryRowContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,service_id,identity_required,kind,target_product_id,diff_amount,config_snapshot)
		 VALUES($1,$2,$3,$4,$5,$6,$7,'upgrade',$8,$9,$10) RETURNING id`,
		userID, targetProductID, psID, cycle, orderAmount, serviceID, requiresIdentity, targetProductID, diff, snap).Scan(&orderID)
	if err != nil {
		return 0, 0, "", 0, err
	}
	// 占住目标产品库存（30 分钟有效），与新建订单一致；未支付由 cron 回收。
	if targetRepo.Stock > 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO stock_reservations(order_id,product_id,expires_at) VALUES($1,$2,now()+interval '30 minutes')`,
			orderID, targetProductID); err != nil {
			return 0, 0, "", 0, err
		}
	}
	no, err := genInvoiceNo()
	if err != nil {
		return 0, 0, "", 0, err
	}
	err = tx.QueryRowContext(ctx,
		`INSERT INTO invoices(no,user_id,order_id,amount,due_at) VALUES($1,$2,$3,$4,now()+$5::interval) RETURNING id`,
		no, userID, orderID, orderAmount, invoiceDueInterval).Scan(&invoiceID)
	if err != nil {
		return 0, 0, "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, "", 0, err
	}
	return orderID, invoiceID, orderAmount, diff, nil
}
