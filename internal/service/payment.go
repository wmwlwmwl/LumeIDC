package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"lumeidc/internal/crypto"
	"lumeidc/internal/money"
	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

var (
	ErrAlreadyPaid = errors.New("账单已支付")
)

type Payment struct {
	db           *sql.DB
	Servers      *repo.Servers
	Products     *repo.Products
	Provisions   *repo.ProvisionRepo
	Jobs         *repo.FulfillmentJobs
	Balance      *repo.Balance
	Providers    *server.Registry
	PeriodGrants *repo.PeriodGrants
	Notifier     *Notifier
	Crypt        *crypto.Cryptor // 实例密码加密落库（services.password_crypt），可为 nil
	// Lifecycle 上游续费用；由组合根注入与 Fulfillment/Cron 共享的同一实例。
	Lifecycle *Lifecycle
	// TriggerFulfillment 支付成功后立即触发队列执行（httpserver 注入，异步 Drain）。
	// 为 nil 时仅靠 cron 每 15s 轮询，支付后开通最多延迟一个轮询周期。
	TriggerFulfillment func()
}

// triggerFulfillment 手动催一次履约队列。管理员点「重试」后立刻开跑，
// 不必干等 cron 的 15 秒轮询；未注入（精简部署/测试）时静默退回轮询。
func (p *Payment) triggerFulfillment() {
	if p.TriggerFulfillment != nil {
		p.TriggerFulfillment()
	}
}

// VerifyOrderPriceBeforePay 付款前复核：新购订单的「下单时成本」与该商品「本地当前成本」不一致（涨价）时拒绝收款。
// 场景：用户下单后把账单挂着，等上游改价了才付款。不拦的话钱先收进来，开通时才发现上游贵了，
// 只能转人工让管理员垫差价或退款。这里在付款入口用同步后的本地价（最多滞后一个同步周期）先拦一道，
// 让用户按新价重新下单；上游临时改价的实时差异仍由开通前比价兜底。
// 只对新购订单生效（service_id 为空）：续费/升级的本地价与上游成本本就不同步（续费走冻结价），
// 拦了会让用户永远续不了费，这两种维持「开通前比价转人工」。
// 读不到订单/快照/价格时一律放行，不阻断正常支付。
func (p *Payment) VerifyOrderPriceBeforePay(ctx context.Context, invoiceID, userID int64) error {
	var productID int64
	var cycle, kind string
	var svcID sql.NullInt64
	var raw []byte
	if err := p.db.QueryRowContext(ctx,
		`SELECT o.product_id, o.cycle, coalesce(o.kind,''), o.service_id, coalesce(o.config_snapshot::text,'')
		   FROM invoices i JOIN orders o ON o.id=i.order_id
		  WHERE i.id=$1 AND i.user_id=$2`, invoiceID, userID).
		Scan(&productID, &cycle, &kind, &svcID, &raw); err != nil {
		return nil // 充值账单等无关联订单：不拦
	}
	if svcID.Valid || kind == "upgrade" {
		return nil
	}
	var snap struct {
		Quote struct {
			Total float64 `json:"total"`
			Setup float64 `json:"setup"`
		} `json:"quote"`
		Selection map[string]string `json:"selection"`
	}
	if json.Unmarshal([]byte(raw), &snap) != nil {
		return nil // 老订单无快照：无从比对
	}
	psID, err := p.Products.DefaultPricesetID(ctx)
	if err != nil {
		return nil
	}
	pr, err := p.Products.Price(ctx, productID, psID)
	if err != nil {
		return nil
	}
	base, err := strconv.ParseFloat(cyclePrice(pr, cycle), 64)
	if err != nil || base < 0 {
		return nil
	}
	opts, err := p.Products.GetConfigOptions(ctx, productID)
	if err != nil {
		return nil // 配置损坏：交给开通流程报错，不在这里拦支付
	}
	cur, err := CalculateQuote(opts, base, cycle, snap.Selection)
	if err != nil {
		return fmt.Errorf("%w：商品配置已更新，请重新下单", ErrUpstreamPriceChanged)
	}
	if cur.PayableOnce() <= snap.Quote.Total+snap.Quote.Setup+server.PriceTolerance {
		return nil // 未涨价（含降价）：照常支付
	}
	pt, pv, _ := p.Products.ProductSellProfit(ctx, productID)
	return fmt.Errorf("%w：现价 ￥%.2f，请重新下单后支付", ErrUpstreamPriceChanged,
		mathRound(applyProfit(cur.PayableOnce(), pt, pv)))
}

// MarkPaidByBalance 用余额支付账单。余额不足返回错误，账单保持未支付。
func (p *Payment) MarkPaidByBalance(ctx context.Context, invoiceNo string, userID int64) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var invID int64
	var status int16
	var kind, credit string
	// 写路径以数据库持久状态为准：已过期账单由 cron 置为 3 后才拒绝。
	// 不用 due_at 动态判过期，避免"到期前发起支付、到期后才回调"的真实付款被拒收丢单。
	if err := tx.QueryRowContext(ctx,
		`SELECT id,status,kind,coalesce(credit,0)::text
		 FROM invoices WHERE no=$1 AND user_id=$2 FOR UPDATE`, invoiceNo, userID).Scan(&invID, &status, &kind, &credit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound("账单不存在")
		}
		return err
	}
	if status != 0 {
		if status == 1 {
			return ErrAlreadyPaid
		}
		if status == 3 {
			return fmt.Errorf("账单已过期")
		}
		return fmt.Errorf("账单不可支付")
	}
	if kind == "recharge" {
		return fmt.Errorf("充值账单不能使用余额支付")
	}
	// 此前若已用余额抵扣过部分，先归还再按全额扣款，避免重复占用。
	if _, cents, perr := money.ParseNonNegative(credit, 999999999999); perr == nil && cents > 0 {
		if err := p.releaseCreditTx(ctx, tx, userID, invoiceNo, cents); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invoices SET credit=0 WHERE id=$1`, invID); err != nil {
		return err
	}
	// 余额支付也可以接管用户已退出的在线支付尝试，避免被旧 pending 记录锁死。
	if _, err := tx.ExecContext(ctx,
		`UPDATE payment_attempts SET status=2 WHERE invoice_id=$1 AND status=0`, invID); err != nil {
		return err
	}
	var amountStr string
	var orderID, productID int64
	var cycle string
	var renewServiceID sql.NullInt64
	var orderKind string
	var targetProductID int64
	var diffAmount float64
	if err := tx.QueryRowContext(ctx,
		`SELECT o.amount, o.cycle, o.id, o.service_id, o.product_id, o.kind,
		        coalesce(o.target_product_id,0), coalesce(o.diff_amount,0)::float8
		 FROM invoices i JOIN orders o ON o.id=i.order_id WHERE i.id=$1`, invID).
		Scan(&amountStr, &cycle, &orderID, &renewServiceID, &productID, &orderKind, &targetProductID, &diffAmount); err != nil {
		return fmt.Errorf("订单缺失: %w", err)
	}
	if ok, err := validPositiveAmount(ctx, tx, amountStr); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("账单金额无效")
	}
	if !renewServiceID.Valid {
		if err := p.reserveStock(ctx, tx, productID, orderID); err != nil {
			return err
		}
	}
	if err := p.Balance.ConsumeAmount(ctx, tx, userID, amountStr, "账单支付 "+invoiceNo); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE invoices SET status=1, paid_at=now(), gateway='balance', trade_no=$2,
			paid_amount=$3, fee_percent=0, fee_amount=0 WHERE id=$1`,
		invID, "BALANCE-"+invoiceNo, amountStr); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE orders SET status=1, paid_at=now() WHERE id=$1 AND status=0`, orderID); err != nil {
		return err
	}
	var svcID int64
	var newService bool
	isUpgrade := orderKind == "upgrade"
	interval, err := CycleInterval(cycle)
	if err != nil {
		return err
	}
	switch {
	case isUpgrade:
		if !renewServiceID.Valid || renewServiceID.Int64 <= 0 {
			return fmt.Errorf("升级订单缺少服务")
		}
		if targetProductID <= 0 {
			return fmt.Errorf("升级订单缺少目标产品")
		}
		svcID = renewServiceID.Int64
		// 支付事务内本地只做：降级退差 + 标记"升级中"防并发。
		// 换产品/周期/快照由 Lifecycle.Upgrade 在上游成功后统一落地，失败自动回滚退款。
		if err := p.prepareUpgrade(ctx, tx, userID, svcID, diffAmount, orderID); err != nil {
			return err
		}
	case renewServiceID.Valid && renewServiceID.Int64 > 0:
		svcID = renewServiceID.Int64
		// 检查是否已授权，防止重复续费
		if p.PeriodGrants != nil {
			if err := p.PeriodGrants.Grant(ctx, tx, svcID, invID, cycle); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE services SET status=1, expires_at=GREATEST(expires_at, now()) + $2::interval, expire_warn_sent=false WHERE id=$1`, svcID, interval); err != nil {
			return err
		}
	default:
		var svcIDNew int64
		var userIDFromOrder int64
		if err := tx.QueryRowContext(ctx,
			`SELECT user_id, product_id FROM orders WHERE id=$1`, orderID).Scan(&userIDFromOrder, &productID); err != nil {
			return fmt.Errorf("订单缺失: %w", err)
		}
		expiresAt, _ := CycleAddDate(time.Now().UTC(), cycle)
		if err := tx.QueryRowContext(ctx, insertServiceSQL,
			userIDFromOrder, productID, orderID, expiresAt, cycle,
			p.frozenRenewAmount(ctx, tx, orderID, amountStr)).Scan(&svcIDNew); err != nil {
			return err
		}
		svcID = svcIDNew
		newService = true
	}
	if p.Jobs != nil {
		kind := "renew"
		switch {
		case isUpgrade:
			kind = "upgrade"
		case newService:
			kind = "provision"
		}
		if err := p.enqueueFulfillment(ctx, tx, svcID, orderID, kind, cycle); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if p.TriggerFulfillment != nil {
		p.TriggerFulfillment()
	}
	if p.Notifier != nil {
		msg := "账单 " + invoiceNo + " 已通过余额支付，服务开通中。"
		if isUpgrade {
			msg = "账单 " + invoiceNo + " 已通过余额支付，服务升降级已生效。"
		} else if !newService {
			msg = "账单 " + invoiceNo + " 已通过余额支付，服务已续费。"
		}
		p.Notifier.NotifyTemplate(ctx, userID, "payment_success", "支付成功", msg, map[string]string{"amount": amountStr, "invoice_no": invoiceNo})
	}
	if p.Jobs == nil {
		switch {
		case isUpgrade:
			p.upgradeAsync(svcID, cycle, orderID)
		case renewServiceID.Valid && renewServiceID.Int64 > 0:
			p.renewAsync(svcID, cycle, orderID)
		case newService:
			p.provisionAsync(svcID, productID, cycle)
		}
	}
	return nil
}

// OnlinePrep 是“余额 + 在线”组合支付下单的结果。
type OnlinePrep struct {
	FullyCovered bool // 余额已覆盖全部应付，无需在线支付
	AttemptID    int64
	Payable      string // 在线应付（含手续费）
	Online       string // 在线本金（不含手续费）
	Credit       string // 本次余额抵扣
	FeePercent   string
	FeeAmount    string
}

// PrepareOnline 为在线支付准备账单，支持余额抵扣剩余应付：
//   - 释放上一次已抵扣的余额（重新选择支付方式时不重复占用）；
//   - useBalance 为真时用余额抵扣剩余应付（最多抵扣到 0，充值账单不允许）；
//   - 余额已覆盖全部时返回 FullyCovered，由调用方走余额核销；
//   - 否则就剩余本金创建在线支付尝试，手续费只对在线本金收取。
//
// 余额在此时实时扣减；若调用方最终无法生成支付链接，必须调用
// ReleaseInvoiceCredit 归还。
func (p *Payment) PrepareOnline(ctx context.Context, invoiceID, userID int64, gatewayCode, feePercent string, useBalance bool) (OnlinePrep, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return OnlinePrep{}, err
	}
	defer tx.Rollback()

	var no, amount, credit, kind, balance string
	var status int16
	if err := tx.QueryRowContext(ctx,
		`SELECT i.no,i.amount::text,coalesce(i.credit,0)::text,i.status,i.kind,u.balance::text
		 FROM invoices i JOIN users u ON u.id=i.user_id
		 WHERE i.id=$1 AND i.user_id=$2
		 FOR UPDATE OF i,u`, invoiceID, userID).
		Scan(&no, &amount, &credit, &status, &kind, &balance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OnlinePrep{}, errNotFound("账单不存在")
		}
		return OnlinePrep{}, err
	}
	if status != 0 {
		if status == 1 {
			return OnlinePrep{}, ErrAlreadyPaid
		}
		if status == 3 {
			return OnlinePrep{}, fmt.Errorf("账单已过期")
		}
		return OnlinePrep{}, fmt.Errorf("账单不可支付")
	}
	if kind == "recharge" {
		useBalance = false // 充值账单不使用余额抵扣
	}
	_, amountCents, err := money.ParsePositive(amount, 999999999999)
	if err != nil {
		return OnlinePrep{}, fmt.Errorf("账单金额无效")
	}
	_, creditCents, err := money.ParseNonNegative(credit, 999999999999)
	if err != nil || creditCents > amountCents {
		creditCents = 0
	}
	// 释放上一次抵扣，保证重新选择支付方式时不重复占用余额。
	if creditCents > 0 {
		if err := p.releaseCreditTx(ctx, tx, userID, no, creditCents); err != nil {
			return OnlinePrep{}, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT balance::text FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&balance); err != nil {
			return OnlinePrep{}, err
		}
	}
	// 旧抵扣已退回，本次必须从完整账单本金重新计算。
	remainingCents := amountCents
	if _, err := tx.ExecContext(ctx, `UPDATE payment_attempts SET status=2 WHERE invoice_id=$1 AND status=0`, invoiceID); err != nil {
		return OnlinePrep{}, err
	}
	var applyCents int64
	if useBalance && remainingCents > 0 {
		balanceCents := int64(0)
		if _, bc, berr := money.ParseNonNegative(balance, 999999999999); berr == nil {
			balanceCents = bc
		}
		applyCents = balanceCents
		if applyCents > remainingCents {
			applyCents = remainingCents
		}
	}
	if remainingCents > 0 && applyCents >= remainingCents {
		// 余额可覆盖全部：交回调用方走余额核销，不在本次预扣。
		if _, err := tx.ExecContext(ctx, `UPDATE invoices SET credit=0 WHERE id=$1`, invoiceID); err != nil {
			return OnlinePrep{}, err
		}
		if err := tx.Commit(); err != nil {
			return OnlinePrep{}, err
		}
		return OnlinePrep{FullyCovered: true}, nil
	}
	if applyCents > 0 {
		if err := p.applyCreditTx(ctx, tx, userID, no, applyCents); err != nil {
			return OnlinePrep{}, err
		}
	}
	online := money.FormatCents(remainingCents - applyCents)
	feeAmount, payable, ferr := money.AddPercent(online, feePercent)
	if ferr != nil {
		return OnlinePrep{}, fmt.Errorf("支付网关手续费配置无效")
	}
	var attemptID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO payment_attempts(invoice_id,gateway_code,amount,fee_percent,fee_amount) VALUES($1,$2,$3,$4,$5) RETURNING id`,
		invoiceID, gatewayCode, payable, feePercent, feeAmount).Scan(&attemptID); err != nil {
		return OnlinePrep{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE invoices SET credit=$2, gateway=$3 WHERE id=$1 AND status=0`,
		invoiceID, money.FormatCents(applyCents), gatewayCode); err != nil {
		return OnlinePrep{}, err
	}
	if err := tx.Commit(); err != nil {
		return OnlinePrep{}, err
	}
	return OnlinePrep{AttemptID: attemptID, Payable: payable, Online: online,
		Credit: money.FormatCents(applyCents), FeePercent: feePercent, FeeAmount: feeAmount}, nil
}

// ReleaseInvoiceCredit 归还未完成在线支付的账单已抵扣余额，并把 credit 清零、
// 结束对应支付尝试（在线下单失败等场景）。
func (p *Payment) ReleaseInvoiceCredit(ctx context.Context, invoiceID, attemptID int64) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var no, credit, gateway string
	var userID int64
	var status int16
	// 与准备支付、核销一致，先锁账单再检查尝试，避免迟到清理退掉新抵扣。
	if err := tx.QueryRowContext(ctx,
		`SELECT no,user_id,coalesce(credit,0)::text,status,gateway FROM invoices WHERE id=$1 FOR UPDATE`, invoiceID).
		Scan(&no, &userID, &credit, &status, &gateway); err != nil {
		return err
	}
	if status != 0 {
		return nil
	}
	var activeID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM payment_attempts WHERE id=$1 AND invoice_id=$2 AND gateway_code=$3 AND status=0 FOR UPDATE`,
		attemptID, invoiceID, gateway).Scan(&activeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // 旧尝试、其他账单或已结束的尝试均无需清理。
		}
		return err
	}
	if _, cents, perr := money.ParseNonNegative(credit, 999999999999); perr == nil && cents > 0 {
		if err := p.releaseCreditTx(ctx, tx, userID, no, cents); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invoices SET credit=0 WHERE id=$1`, invoiceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE payment_attempts SET status=2 WHERE id=$1 AND status=0`, activeID); err != nil {
		return err
	}
	return tx.Commit()
}

// CreditCapturedToBalance 把一笔无法再核销的在线到账退回用户余额（如切换网关
// 或重复支付）。以支付尝试的 provider_trade_no 作为幂等标记，重复回调不会重复入账。
func (p *Payment) CreditCapturedToBalance(ctx context.Context, attemptID int64, tradeNo string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 幂等：仅当该尝试尚未记录外部流水号时处理一次（唯一索引兜底防并发）。
	res, err := tx.ExecContext(ctx,
		`UPDATE payment_attempts SET provider_trade_no=$2, paid_at=now() WHERE id=$1 AND provider_trade_no=''`,
		attemptID, tradeNo)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return tx.Commit() // 已处理过，直接成功
	}
	var userID int64
	var amount, no string
	if err := tx.QueryRowContext(ctx,
		`SELECT i.user_id,p.amount::text,i.no FROM payment_attempts p JOIN invoices i ON i.id=p.invoice_id WHERE p.id=$1`,
		attemptID).Scan(&userID, &amount, &no); err != nil {
		return err
	}
	if _, cents, perr := money.ParsePositive(amount, 999999999999); perr != nil || cents <= 0 {
		return tx.Commit() // 金额异常时不入账，避免错误退款
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance+$2::numeric WHERE id=$1`, userID, amount); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO balance_logs(user_id,amount,balance_after,type,note)
		 SELECT $1,$2::numeric,balance,'refund',$3 FROM users WHERE id=$1`,
		userID, amount, "重复/失效支付退回 "+no); err != nil {
		return err
	}
	return tx.Commit()
}

// applyCreditTx 在事务内扣减余额作为账单抵扣并记录流水（余额不足报错）。
func (p *Payment) applyCreditTx(ctx context.Context, tx *sql.Tx, userID int64, invoiceNo string, cents int64) error {
	amount := money.FormatCents(cents)
	var enough bool
	if err := tx.QueryRowContext(ctx,
		`SELECT balance >= $2::numeric FROM users WHERE id=$1 FOR UPDATE`, userID, amount).Scan(&enough); err != nil {
		return err
	}
	if !enough {
		return repo.ErrInsufficientBalance
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance-$2::numeric WHERE id=$1`, userID, amount); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO balance_logs(user_id,amount,balance_after,type,note)
		 SELECT $1,-$2::numeric,balance,'consume',$3 FROM users WHERE id=$1`,
		userID, amount, "账单余额抵扣 "+invoiceNo)
	return err
}

// releaseCreditTx 在事务内把账单抵扣的余额归还用户并记录流水。
func (p *Payment) releaseCreditTx(ctx context.Context, tx *sql.Tx, userID int64, invoiceNo string, cents int64) error {
	amount := money.FormatCents(cents)
	if _, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance+$2::numeric WHERE id=$1`, userID, amount); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO balance_logs(user_id,amount,balance_after,type,note)
		 SELECT $1,$2::numeric,balance,'refund',$3 FROM users WHERE id=$1`,
		userID, amount, "账单余额抵扣释放 "+invoiceNo)
	return err
}

func validPositiveAmount(ctx context.Context, tx *sql.Tx, amount string) (bool, error) {
	_, _, err := money.ParsePositive(amount, 999999999999)
	return err == nil, nil
}

func (p *Payment) reserveStock(ctx context.Context, tx *sql.Tx, productID, orderID int64) error {
	var stock int
	if err := tx.QueryRowContext(ctx, `SELECT stock FROM products WHERE id=$1 FOR UPDATE`, productID).Scan(&stock); err != nil {
		return err
	}
	if stock < 0 {
		return nil
	}
	if stock == 0 {
		return fmt.Errorf("商品已售罄")
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE stock_reservations SET status='consumed',consumed_at=now()
		 WHERE order_id=$1 AND product_id=$2 AND status='reserved' AND expires_at>now()`, orderID, productID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		var reservationExists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM stock_reservations WHERE order_id=$1 AND product_id=$2)`, orderID, productID).Scan(&reservationExists); err != nil {
			return err
		}
		if reservationExists {
			return fmt.Errorf("库存预留已过期，请重新下单")
		}
		// 兼容 010 迁移前创建的未支付订单：订单锁定且支付状态已锁定，允许一次性消耗库存。
	}
	_, err = tx.ExecContext(ctx, `UPDATE products SET stock=stock-1 WHERE id=$1 AND stock>0`, productID)
	return err
}

func (p *Payment) enqueueFulfillment(ctx context.Context, tx *sql.Tx, serviceID, orderID int64, kind, cycle string) error {
	if p.Jobs == nil {
		return nil
	}
	return p.Jobs.EnqueueTx(ctx, tx, serviceID, orderID, kind, cycle)
}

// markPaidTx 聚合一次核销的已校验参数，供充值/履约两个事务内分支共用。
type markPaidTx struct {
	invoiceNo                         string
	invID, userID, orderID            int64
	now                               time.Time
	gatewayCode, tradeNo              string
	paidAmount, feePercent, feeAmount string
	attemptID                         int64 // 余额核销无支付尝试记录时为 0
}

// MarkPaid 原子核销账单并开通/续期服务。幂等：重复调用返回 ErrAlreadyPaid。
func (p *Payment) MarkPaid(ctx context.Context, invoiceNo, tradeNo, gatewayCode string, attemptIDs ...int64) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var invID, userID, orderID int64
	var status int16
	var kind string
	// 同 MarkPaidByBalance：写路径只认持久状态，过期由 cron 显式置 3。
	err = tx.QueryRowContext(ctx,
		`SELECT id,user_id,coalesce(order_id,0),status,kind
		 FROM invoices WHERE no=$1 FOR UPDATE`, invoiceNo).
		Scan(&invID, &userID, &orderID, &status, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound2
	}
	if err != nil {
		return err
	}
	if status != 0 {
		if status == 1 {
			return ErrAlreadyPaid
		}
		if status == 3 {
			return fmt.Errorf("账单已过期")
		}
		return fmt.Errorf("账单不可支付")
	}
	var attemptID int64
	var paidAmount, feePercent, feeAmount string
	if gatewayCode == "balance" {
		if err := tx.QueryRowContext(ctx, `SELECT amount::text FROM invoices WHERE id=$1`, invID).Scan(&paidAmount); err != nil {
			return err
		}
		feePercent, feeAmount = "0.00", "0.00"
	} else {
		if len(attemptIDs) != 1 {
			return fmt.Errorf("支付记录不存在")
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT id,amount::text,fee_percent::text,fee_amount::text FROM payment_attempts
			 WHERE id=$1 AND invoice_id=$2 AND gateway_code=$3 AND status=0 FOR UPDATE`,
			attemptIDs[0], invID, gatewayCode).Scan(&attemptID, &paidAmount, &feePercent, &feeAmount); err != nil {
			return fmt.Errorf("支付记录不存在或已处理")
		}
	}
	args := markPaidTx{
		invoiceNo: invoiceNo, invID: invID, userID: userID, orderID: orderID,
		now: time.Now().UTC(), gatewayCode: gatewayCode, tradeNo: tradeNo,
		paidAmount: paidAmount, feePercent: feePercent, feeAmount: feeAmount, attemptID: attemptID,
	}

	if kind == "recharge" {
		amount, err := p.rechargeInvoiceTx(ctx, tx, args)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if p.Notifier != nil {
			p.Notifier.NotifyTemplate(ctx, userID, "recharge_success", "充值成功", "余额已充值 "+amount+" 元。", map[string]string{"amount": amount, "invoice_no": invoiceNo})
		}
		return nil
	}

	res, err := p.fulfillOrderTx(ctx, tx, args)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if p.TriggerFulfillment != nil {
		p.TriggerFulfillment()
	}
	if p.Notifier != nil {
		msg := "账单 " + invoiceNo + " 已支付，服务开通中。"
		if res.kind == "upgrade" {
			msg = "账单 " + invoiceNo + " 已支付，服务升降级已生效。"
		} else if res.kind == "renew" {
			msg = "账单 " + invoiceNo + " 已支付，服务已续费。"
		}
		p.Notifier.NotifyTemplate(ctx, userID, "payment_success", "支付成功", msg, map[string]string{"amount": paidAmount, "invoice_no": invoiceNo})
	}
	if p.Jobs == nil {
		switch res.kind {
		case "upgrade":
			p.upgradeAsync(res.svcID, res.cycle, res.orderID)
		case "renew":
			p.renewAsync(res.svcID, res.cycle, res.orderID)
		default:
			p.provisionAsync(res.svcID, res.productID, res.cycle)
		}
	}
	return nil
}

// markPaidResult 履约结果：kind 为 provision/renew/upgrade，决定后续队列与提示文案。
type markPaidResult struct {
	svcID     int64
	cycle     string
	productID int64
	orderID   int64
	kind      string
}

// rechargeInvoiceTx 充值账单核销：加余额、写流水、结账单与支付尝试，返回充值金额。
func (p *Payment) rechargeInvoiceTx(ctx context.Context, tx *sql.Tx, a markPaidTx) (string, error) {
	var amount string
	if err := tx.QueryRowContext(ctx, `SELECT amount::text FROM invoices WHERE id=$1`, a.invID).Scan(&amount); err != nil {
		return "", err
	}
	if ok, err := validPositiveAmount(ctx, tx, amount); err != nil {
		return "", err
	} else if !ok {
		return "", fmt.Errorf("充值金额无效")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance+$2::numeric WHERE id=$1`, a.userID, amount); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO balance_logs(user_id,amount,balance_after,type,note)
		 SELECT $1,$2::numeric,balance,'recharge',$3 FROM users WHERE id=$1`,
		a.userID, amount, "在线充值 "+a.invoiceNo); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE invoices SET status=1,paid_at=$2,gateway=$3,trade_no=$4,paid_amount=$5,fee_percent=$6,fee_amount=$7 WHERE id=$1`,
		a.invID, a.now, a.gatewayCode, a.tradeNo, a.paidAmount, a.feePercent, a.feeAmount); err != nil {
		return "", err
	}
	// 余额支付不产生 payment_attempts 记录（attemptID=0），跳过该更新。
	if a.attemptID > 0 {
		res, err := tx.ExecContext(ctx,
			`UPDATE payment_attempts SET status=1,provider_trade_no=$2,paid_at=$3 WHERE id=$1`,
			a.attemptID, a.tradeNo, a.now)
		if err != nil {
			return "", err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return "", fmt.Errorf("支付记录不存在或已处理")
		}
	}
	return amount, nil
}

// insertServiceSQL 新购建服务：把下单时的「周期费售价」冻结为对应周期的固定续费价
// （对齐魔方财务 host.amount 的「下单冻结、续费沿用」语义，产品改价不影响存量服务）。
// 金额 ≤0 时留 NULL，保持「跟随产品当前价」；管理员可在后台改或清空。
// 参数：$1 用户 / $2 产品 / $3 订单 / $4 到期 / $5 周期 / $6 周期费售价（见 frozenRenewAmount）。
const insertServiceSQL = `INSERT INTO services(user_id,product_id,server_id,order_id,name,status,expires_at,
			upstream_provider,upstream_pid,renew_monthly,renew_quarterly,renew_yearly)
		 SELECT $1,$2,p.server_id,$3,p.name,0,$4,coalesce(s.provider,''),p.upstream_pid,
		        CASE WHEN $5='monthly'   AND $6::numeric>0 THEN $6::numeric END,
		        CASE WHEN $5='quarterly' AND $6::numeric>0 THEN $6::numeric END,
		        CASE WHEN $5='yearly'    AND $6::numeric>0 THEN $6::numeric END
		 FROM products p LEFT JOIN servers s ON s.id=p.server_id WHERE p.id=$2 RETURNING id`

// frozenRenewAmount 冻结的续费价 = 订单成交额里「周期费」那部分的售价。
// 初装费是一次性费用（只首购收），不能被一起冻结，否则每次续费都会再收一遍：
// 例如成交额 33 = 周期费 28 + 初装费 5，冻结 33 后续费就一直多收 5。
// 利润对「周期费 + 初装费」整体加成，故先各自算售价再作差，得到初装费在成交额里的份额。
// 无初装费、无快照、金额非法时原样返回成交额（保持既有行为，兼容老数据）。
func (p *Payment) frozenRenewAmount(ctx context.Context, tx *sql.Tx, orderID int64, amount string) string {
	var productID int64
	var raw []byte
	if err := tx.QueryRowContext(ctx,
		`SELECT product_id, coalesce(config_snapshot::text,'') FROM orders WHERE id=$1`, orderID).
		Scan(&productID, &raw); err != nil {
		return amount
	}
	var snap struct {
		Quote struct {
			Total float64 `json:"total"`
			Setup float64 `json:"setup"`
		} `json:"quote"`
	}
	if json.Unmarshal([]byte(raw), &snap) != nil || snap.Quote.Setup <= 0 {
		return amount
	}
	amt, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return amount
	}
	pt, pv, err := p.Products.ProductSellProfit(ctx, productID)
	if err != nil {
		return amount
	}
	if v := frozenRenewValue(amt, snap.Quote.Total, snap.Quote.Setup, pt, pv); v > 0 {
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
	return "0"
}

// frozenRenewValue 冻结续费价的纯计算：成交额 − 初装费在成交额里的售价份额。
func frozenRenewValue(amount, quoteTotal, quoteSetup float64, profitType int16, profitValue float64) float64 {
	setupSell := mathRound(applyProfit(quoteTotal+quoteSetup, profitType, profitValue)) -
		mathRound(applyProfit(quoteTotal, profitType, profitValue))
	return mathRound(amount - setupSell)
}

// fulfillOrderTx 订单账单核销：结账单/结束支付尝试/结束订单后，按订单种类完成
// 升级（退差+标记升级中）/续费（延期）/新购（建服务）三分支并入队履约。
func (p *Payment) fulfillOrderTx(ctx context.Context, tx *sql.Tx, a markPaidTx) (markPaidResult, error) {
	var productID int64
	var cycle string
	var amountStr string
	var renewServiceID sql.NullInt64
	var orderKind string
	var targetProductID int64
	var diffAmount float64
	err := tx.QueryRowContext(ctx,
		`SELECT product_id,cycle,coalesce(amount,0)::text,service_id,kind,coalesce(target_product_id,0),coalesce(diff_amount,0)::float8 FROM orders WHERE id=$1`, a.orderID).
		Scan(&productID, &cycle, &amountStr, &renewServiceID, &orderKind, &targetProductID, &diffAmount)
	if err != nil {
		return markPaidResult{}, fmt.Errorf("订单缺失: %w", err)
	}
	if renewServiceID.Valid && renewServiceID.Int64 <= 0 {
		renewServiceID.Valid = false
	}
	if !renewServiceID.Valid {
		if err := p.reserveStock(ctx, tx, productID, a.orderID); err != nil {
			return markPaidResult{}, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE invoices SET status=1,paid_at=$2,gateway=$3,trade_no=$4,paid_amount=$5,fee_percent=$6,fee_amount=$7 WHERE id=$1`,
		a.invID, a.now, a.gatewayCode, a.tradeNo, a.paidAmount, a.feePercent, a.feeAmount); err != nil {
		return markPaidResult{}, err
	}
	// 只结束本次网关实例最近的一条支付尝试，保留同一账单切换网关的历史记录。
	// 余额支付没有支付尝试记录（attemptID=0），跳过。
	if a.attemptID > 0 {
		res, err := tx.ExecContext(ctx,
			`UPDATE payment_attempts SET status=1,provider_trade_no=$2,paid_at=$3 WHERE id=$1`,
			a.attemptID, a.tradeNo, a.now)
		if err != nil {
			return markPaidResult{}, err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return markPaidResult{}, fmt.Errorf("支付记录不存在或已处理")
		}
	}
	if err := tx.QueryRowContext(ctx,
		`UPDATE orders SET status=1,paid_at=$2 WHERE id=$1 AND status=0 RETURNING id`, a.orderID, a.now).Scan(&a.orderID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return markPaidResult{}, err
	}

	interval, err := CycleInterval(cycle)
	if err != nil {
		return markPaidResult{}, err
	}

	isUpgrade := orderKind == "upgrade"
	isRenew := false
	var svcID int64
	switch {
	case isUpgrade:
		// 升降级单：支付事务内仅退差 + 标记"升级中"；换产品由 Lifecycle.Upgrade 在上游成功后落地
		if !renewServiceID.Valid || renewServiceID.Int64 <= 0 {
			return markPaidResult{}, fmt.Errorf("升级订单缺少服务")
		}
		if targetProductID <= 0 {
			return markPaidResult{}, fmt.Errorf("升级订单缺少目标产品")
		}
		svcID = renewServiceID.Int64
		if err := p.prepareUpgrade(ctx, tx, a.userID, svcID, diffAmount, a.orderID); err != nil {
			return markPaidResult{}, err
		}
	case renewServiceID.Valid && renewServiceID.Int64 > 0:
		// 续费单：直接延期指定服务并触发上游 Renew
		isRenew = true
		svcID = renewServiceID.Int64
		var svcStatus int16
		var transition string
		if err := tx.QueryRowContext(ctx, `SELECT status,coalesce(transition_state,'') FROM services WHERE id=$1 FOR UPDATE`, svcID).Scan(&svcStatus, &transition); err != nil {
			return markPaidResult{}, err
		}
		if (svcStatus != 1 && svcStatus != 2) || transition != "" {
			return markPaidResult{}, fmt.Errorf("服务当前状态不可续费")
		}
		// 检查是否已授权，防止重复续费
		if p.PeriodGrants != nil {
			if err := p.PeriodGrants.Grant(ctx, tx, svcID, a.invID, cycle); err != nil {
				return markPaidResult{}, err
			}
		}
		// 续费：从当前到期时间（或现在，取较晚者）延长一个周期
		if _, err := tx.ExecContext(ctx,
			`UPDATE services SET expires_at=GREATEST(expires_at,now()) + $2::interval, expire_warn_sent=false WHERE id=$1 AND status IN (1,2) AND coalesce(transition_state,'')=''`,
			svcID, interval); err != nil {
			return markPaidResult{}, err
		}
	default:
		// 新购：每次支付独立建一个待开通服务（不合并既有同产品服务；续费走 isRenew 分支）
		var svcIDNew int64
		expiresAt, _ := CycleAddDate(a.now, cycle)
		err = tx.QueryRowContext(ctx, insertServiceSQL,
			a.userID, productID, a.orderID, expiresAt, cycle,
			p.frozenRenewAmount(ctx, tx, a.orderID, amountStr)).Scan(&svcIDNew)
		if err != nil {
			return markPaidResult{}, err
		}
		svcID = svcIDNew
	}

	kind := "provision"
	switch {
	case isUpgrade:
		kind = "upgrade"
	case isRenew:
		kind = "renew"
	}
	if p.Jobs != nil {
		if err := p.enqueueFulfillment(ctx, tx, svcID, a.orderID, kind, cycle); err != nil {
			return markPaidResult{}, err
		}
	}
	return markPaidResult{svcID: svcID, cycle: cycle, productID: productID, orderID: a.orderID, kind: kind}, nil
}

const opRenewTimeout = 90 * time.Second

// provisionAsync / renewAsync / upgradeAsync 只在 p.Jobs == nil（履约队列未启用）时被调用，
// 生产环境恒走 Fulfillment 的持久任务。这三个 goroutine 失败仅记日志：不发管理员告警邮件、
// 也不进后台通知铃铛的待办，所以新增失败告警逻辑时不必在这里重复接入（告警挂在 Fulfillment.processOne）。
//
// provisionAsync 上游自动开通。ponytail: 一期同步 goroutine + 日志；
// 二期换持久化队列。
func (p *Payment) provisionAsync(serviceID, productID int64, cycle string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*60*time.Second)
		defer cancel()
		if err := p.provision(ctx, serviceID, productID, cycle); err != nil {
			log.Printf("[provision] service %d 开通失败: %v", serviceID, err)
		}
	}()
}

// renewAsync 异步触发上游续费，失败仅记日志（账单已核销，后台可重试）。
func (p *Payment) renewAsync(serviceID int64, cycle string, orderID int64) {
	go func() {
		rctx, cancel := context.WithTimeout(context.Background(), opRenewTimeout)
		defer cancel()
		lc := p.Lifecycle
		if lc == nil {
			log.Printf("[renew] service %d 缺少 Lifecycle 服务", serviceID)
			return
		}
		if rerr := lc.Renew(rctx, serviceID, cycle, orderID); rerr != nil {
			log.Printf("[renew] service %d 上游续费失败: %v", serviceID, rerr)
		}
	}()
}

// prepareUpgrade 支付事务内的升级预处理：标记服务"升级中"防并发。
// 不动 expires_at（保留已购时长）。不在此换产品——真实升降级由 Lifecycle.Upgrade
// 在上游成功后统一落地（本地换产品/周期/快照），失败时自动回滚补款。
// 降级（diffAmount<0）**不退差价**：与魔方财务/魔方v10 的默认策略一致——
// 降级只变更配置，差额不返还。旧实现会把 |diff| 退到余额，存在
// "先买高配用一阵、再降配把差价套现"的套利路径；差额仍记在 orders.diff_amount 供审计。
func (p *Payment) prepareUpgrade(ctx context.Context, tx *sql.Tx, userID, svcID int64, diffAmount float64, orderID int64) error {
	var st int16
	var tr string
	if err := tx.QueryRowContext(ctx, `SELECT status,coalesce(transition_state,'') FROM services WHERE id=$1 FOR UPDATE`, svcID).Scan(&st, &tr); err != nil {
		return err
	}
	if (st != 1 && st != 2) || tr != "" {
		return fmt.Errorf("服务当前状态不可升降级")
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE services SET transition_state='upgrading' WHERE id=$1 AND status IN (1,2) AND coalesce(transition_state,'')=''`,
		svcID); err != nil {
		return err
	}
	// 降级不退款（见函数注释）：差额不入账，仅保留 orders.diff_amount 记录。
	return nil
}

// upgradeAsync 异步触发上游升降级（Lifecycle.Upgrade 驱动，含失败自动回滚）。
func (p *Payment) upgradeAsync(serviceID int64, cycle string, orderID int64) {
	go func() {
		rctx, cancel := context.WithTimeout(context.Background(), opRenewTimeout)
		defer cancel()
		lc := p.Lifecycle
		if lc == nil {
			log.Printf("[upgrade] service %d 缺少 Lifecycle 服务", serviceID)
			return
		}
		if uerr := lc.Upgrade(rctx, serviceID, cycle, orderID); uerr != nil {
			log.Printf("[upgrade] service %d 上游升降级失败: %v", serviceID, uerr)
		}
	}()
}

func (p *Payment) provision(ctx context.Context, serviceID, _ int64, cycle string) error {
	var serverID sql.NullInt64
	var providerCode string
	var upstreamPID int64
	if err := p.db.QueryRowContext(ctx,
		`SELECT server_id,coalesce(upstream_provider,''),upstream_pid FROM services WHERE id=$1`, serviceID).
		Scan(&serverID, &providerCode, &upstreamPID); err != nil {
		return p.failProvision(ctx, serviceID, err)
	}
	// 本地服务判定：未绑定服务器；或绑定了服务器但无上游产品 ID 且该供应商不支持弹性模式
	// （PID 可选，如 EasyPanel 详细参数直传）。zjmf 等要求 PID 的供应商行为不变。
	needUpstream := serverID.Valid
	if needUpstream && upstreamPID == 0 {
		if prov0, gerr := p.Providers.Get(providerCode); gerr == nil {
			if po, ok := prov0.(server.PIDOptionalProvider); !ok || !po.PIDOptional() {
				needUpstream = false
			}
		} else {
			needUpstream = false // 未知供应商：按本地服务兜底（不阻断开通）
		}
	}
	if !needUpstream {
		if _, err := p.db.ExecContext(ctx, `UPDATE services SET status=1,provision_error='' WHERE id=$1 AND status=0`, serviceID); err != nil {
			return err
		}
		return nil
	}
	prov, err := p.Providers.Get(providerCode)
	if err != nil {
		return p.failProvision(ctx, serviceID, err)
	}
	sv, err := p.Servers.Get(ctx, serverID.Int64)
	if err != nil {
		return p.failProvision(ctx, serviceID, fmt.Errorf("读取服务器配置失败: %w", err))
	}
	cfg := server.Config{APIURL: sv.APIURL, APIUsername: sv.APIUsername, APIKey: sv.APIKey, CredentialRevision: sv.CredentialRevision}
	unlock, err := lockUpstreamAccount(ctx, p.db, cfg)
	if err != nil {
		return p.failProvision(ctx, serviceID, fmt.Errorf("锁定账户失败: %w", err))
	}
	defer unlock()
	res, err := prov.Provision(ctx, cfg, server.ProvisionRequest{
		UpstreamPID: upstreamPID,
		Cycle:       cycle,
		// 订单配置选择回传上游，确保按所选配置开通（如 NAT 转发=10 时上游真正开通 10 个，
		// 否则上游一直用默认档）。此前漏传会导致选择被忽略。
		ConfigOpts: p.orderConfigOpts(ctx, serviceID),
		// ServiceID 供上游派生唯一标识（如 EasyPanel 站点名 u{id}）。
		ServiceID: serviceID,
		// 下单时的成本额，供上游开通前比对账单金额；上游已涨价则转人工决定强制开通或退款。
		ExpectAmount: p.orderCostAmount(ctx, serviceID),
	}, &serviceCheckpoint{repo: p.Provisions, serviceID: serviceID, ctx: ctx})
	if err != nil {
		return p.failProvision(ctx, serviceID, err)
	}
	if res.UpstreamHostID > 0 {
		// 开通成功即激活（此前只写 host id，等 30s 状态同步 cron 才置 1，用户看到长时间"待开通"）
		if _, err := p.db.ExecContext(ctx,
			`UPDATE services SET upstream_host_id=$2, status=1, provision_error='' WHERE id=$1 AND status=0`, serviceID, res.UpstreamHostID); err != nil {
			return err
		}
	}
	// 供应商回传了实例密码（如 EasyPanel）：加密落库供详情页展示与面板直登。
	if res.Password != "" && p.Crypt != nil {
		if enc, cerr := p.Crypt.Encrypt(res.Password); cerr == nil {
			if _, uerr := p.db.ExecContext(ctx,
				`UPDATE services SET password_crypt=$2 WHERE id=$1`, serviceID, enc); uerr != nil {
				log.Printf("[provision] service %d 密码落库失败: %v", serviceID, uerr)
			}
		}
	}
	return nil
}

const maxProvisionErrLen = 500

// orderConfigOpts 读本服务所属订单保存的配置选择（config_snapshot->selection），
// 用于开通时回传上游。无快照/解析失败返回 nil（上游用默认配置）。
func (p *Payment) orderConfigOpts(ctx context.Context, serviceID int64) map[string]string {
	var snap []byte
	if err := p.db.QueryRowContext(ctx,
		`SELECT o.config_snapshot FROM orders o JOIN services sv ON sv.order_id = o.id WHERE sv.id=$1`,
		serviceID).Scan(&snap); err != nil || len(snap) == 0 {
		return nil
	}
	var cs struct {
		Selection map[string]string `json:"selection"`
	}
	if json.Unmarshal(snap, &cs) != nil {
		return nil
	}
	// 回传订单的全量配置选择（field→子项值）。由 zjmf.configOptionMap 解析为上游
	// configoption[选项id]=子项id 后下单，确保按所选配置开通（如 NAT=10 真正开 10 个）。
	// 此前仅回传含 "nat" 的字段且按字段名下发，上游忽略键名导致非默认档一直落回默认。
	return cs.Selection
}

// failProvision 记录开通失败原因并返回原错误（原因展示在后台/用户端服务页）。
func (p *Payment) failProvision(ctx context.Context, serviceID int64, err error) error {
	if err != nil {
		msg := err.Error()
		if len(msg) > maxProvisionErrLen {
			msg = msg[:maxProvisionErrLen]
		}
		if _, err := p.db.ExecContext(ctx, `UPDATE services SET provision_error=$2 WHERE id=$1`, serviceID, msg); err != nil {
			return fmt.Errorf("记录开通失败: %w（原错误：%v）", err, msg)
		}
	}
	return err
}

// serviceCheckpoint 把 CheckpointStore 桥接到 provision_data JSONB。
type serviceCheckpoint struct {
	repo      *repo.ProvisionRepo
	serviceID int64
	ctx       context.Context
}

func (s *serviceCheckpoint) GetCheckpoint(key string) (string, bool, error) {
	return s.repo.GetCheckpoint(s.ctx, s.serviceID, key)
}

func (s *serviceCheckpoint) SetCheckpoint(key, val string) error {
	return s.repo.SetCheckpoint(s.ctx, s.serviceID, key, val)
}

func (s *serviceCheckpoint) DeleteCheckpoint(key string) error {
	return s.repo.DeleteCheckpoint(s.ctx, s.serviceID, key)
}

// orderCostAmount 读取服务对应订单在下单时的成本额（config_snapshot.quote 的周期费 + 初装费），
// 作为开通前比价的基准。上游开通账单里含一次性初装费，基准漏掉它会每次都误判成"上游涨价"。
// 取不到（老数据无快照）返回 0，调用方跳过比价。
func (p *Payment) orderCostAmount(ctx context.Context, serviceID int64) float64 {
	var raw []byte
	if err := p.db.QueryRowContext(ctx,
		`SELECT o.config_snapshot FROM services sv JOIN orders o ON o.id=sv.order_id WHERE sv.id=$1`,
		serviceID).Scan(&raw); err != nil {
		return 0
	}
	var snap struct {
		Quote struct {
			Total float64 `json:"total"`
			Setup float64 `json:"setup"`
		} `json:"quote"`
	}
	if json.Unmarshal(raw, &snap) != nil {
		return 0
	}
	return snap.Quote.Total + snap.Quote.Setup
}

// RefundPendingService 关闭一个「已付款但未开通」的服务：全额退订单实付 → 取消订单 → 终止服务。
// 用于上游涨价后管理员选择退款给用户（不再按新价开通）。
// 可重入：以 refunds 表是否已有记录判定。退款成功但关单失败时，重试只补齐关单、不重复退款；
// 反过来若先关单后退款，退款失败会让用户钱货两空，故顺序固定为"先退后关"。
func (p *Payment) RefundPendingService(ctx context.Context, adminID, serviceID int64, reason string) error {
	if err := repo.NewFulfillmentJobs(p.db).CheckRetry(ctx, serviceID); err != nil {
		return err
	}
	var orderID int64
	var amount string
	if err := p.db.QueryRowContext(ctx,
		`SELECT o.id, o.amount::text
		   FROM services sv JOIN orders o ON o.id=sv.order_id
		  WHERE sv.id=$1 AND sv.status=0`, serviceID).Scan(&orderID, &amount); err != nil {
		return fmt.Errorf("服务不存在或非待开通状态")
	}
	var refunded bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM refunds WHERE order_id=$1 AND status='done')`, orderID).Scan(&refunded); err != nil {
		return err
	}
	if !refunded {
		if err := p.Refund(ctx, adminID, orderID, amount, reason, "balance"); err != nil {
			return err
		}
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET status=2 WHERE id=$1`, orderID); err != nil {
		return err
	}
	// provision_error 一并清空：退款终止后该服务无处展示（读取点均过滤 status<3），留着是脏数据。
	if _, err := tx.ExecContext(ctx, `UPDATE services SET status=3, provision_error='' WHERE id=$1 AND status=0`, serviceID); err != nil {
		return err
	}
	return tx.Commit()
}

var ErrNotFound2 = errNotFound("账单不存在")

type errNotFound string

func (e errNotFound) Error() string { return string(e) }

// Refund 为已支付订单退款：校验金额上限后写入退款记录，并按方式退回用户余额（method=balance）。
// gateway 方式仅记账（实际渠道退款由人工处理）。退款金额不得超 已付 - 已退。
func (p *Payment) Refund(ctx context.Context, adminID, orderID int64, amount, reason, method string) error {
	if amount == "" {
		return fmt.Errorf("退款金额不能为空")
	}
	canonical, _, err := money.ParsePositive(amount, 999999999999)
	if err != nil {
		return fmt.Errorf("退款金额无效")
	}
	amount = canonical
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 先取服务执行锁再按既有订单/账单顺序加行锁；try 锁避免与恢复相互等待。
	var serviceID int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(o.service_id,(SELECT id FROM services WHERE order_id=o.id LIMIT 1),0) FROM orders o WHERE o.id=$1`, orderID).Scan(&serviceID); err != nil {
		return fmt.Errorf("订单不存在")
	}
	if serviceID > 0 {
		var locked bool
		if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, repo.FulfillmentLockKey(serviceID)).Scan(&locked); err != nil {
			return err
		}
		if !locked {
			return repo.ErrFulfillmentBusy
		}
		var blocked bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fulfillment_jobs WHERE service_id=$1 AND (recovery_required OR status='running'))`, serviceID).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return repo.ErrFulfillmentRecoveryRequired
		}
	}
	var userID int64
	var orderAmount string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, amount FROM orders WHERE id=$1 FOR UPDATE`, orderID).
		Scan(&userID, &orderAmount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("订单不存在")
		}
		return err
	}
	var invStatus int16
	var invGateway string
	if err := tx.QueryRowContext(ctx, `SELECT status,gateway FROM invoices WHERE order_id=$1 FOR UPDATE`, orderID).Scan(&invStatus, &invGateway); err != nil {
		return err
	}
	if invStatus != 1 {
		return fmt.Errorf("订单未支付，不可退款")
	}
	if method != "balance" && method != "gateway" {
		return fmt.Errorf("退款方式无效")
	}
	if invGateway == "balance" {
		method = "balance"
	} else {
		method = "gateway"
	}
	var refunded string
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount::numeric),0)::text FROM refunds WHERE order_id=$1 AND status='done'`, orderID).Scan(&refunded); err != nil {
		return err
	}
	var ok bool
	if err := tx.QueryRowContext(ctx,
		`SELECT $1::numeric <= ($2::numeric - $3::numeric)`, amount, orderAmount, refunded).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("退款金额超过可退余额（已付 %s，已退 %s）", orderAmount, refunded)
	}
	if method == "balance" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET balance = balance + $2::numeric WHERE id=$1`, userID, amount); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO balance_logs(user_id,amount,balance_after,type,note)
			 SELECT $1,$2::numeric,balance,'refund',$3 FROM users WHERE id=$1`,
			userID, amount, reason); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO refunds(user_id,order_id,invoice_id,amount,method,reason,admin_id,status)
		 SELECT $1,$2,(SELECT id FROM invoices WHERE order_id=$2 LIMIT 1),$3,$4,$5,$6,'done'`,
		userID, orderID, amount, method, reason, adminID); err != nil {
		return err
	}
	return tx.Commit()
}
