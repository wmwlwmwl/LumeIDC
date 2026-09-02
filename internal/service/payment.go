package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	DB           *sql.DB
	Servers      *repo.Servers
	Products     *repo.Products
	Provisions   *repo.ProvisionRepo
	Jobs         *repo.FulfillmentJobs
	Balance      *repo.Balance
	Providers    *server.Registry
	PeriodGrants *repo.PeriodGrants
	Notifier     *Notifier
	Crypt        *crypto.Cryptor // 实例密码加密落库（services.password_crypt），可为 nil
	// TriggerFulfillment 支付成功后立即触发队列执行（httpserver 注入，异步 Drain）。
	// 为 nil 时仅靠 cron 每 15s 轮询，支付后开通最多延迟一个轮询周期。
	TriggerFulfillment func()
}

// MarkPaidByBalance 用余额支付账单。余额不足返回错误，账单保持未支付。
func (p *Payment) MarkPaidByBalance(ctx context.Context, invoiceNo string, userID int64) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var invID int64
	var status int16
	var kind string
	if err := tx.QueryRowContext(ctx,
		`SELECT id, status, kind FROM invoices WHERE no=$1 AND user_id=$2 FOR UPDATE`, invoiceNo, userID).Scan(&invID, &status, &kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound("账单不存在")
		}
		return err
	}
	if status != 0 {
		if status == 1 {
			return ErrAlreadyPaid
		}
		return fmt.Errorf("账单不可支付")
	}
	if kind == "recharge" {
		return fmt.Errorf("充值账单不能使用余额支付")
	}
	var pending bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM payment_attempts WHERE invoice_id=$1 AND status=0)`, invID).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return errors.New("账单已有支付进行中")
	}
	var amountStr string
	var orderID, productID int64
	var cycle string
	var renewServiceID sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT o.amount, o.cycle, o.id, o.service_id, o.product_id FROM invoices i JOIN orders o ON o.id=i.order_id WHERE i.id=$1`, invID).
		Scan(&amountStr, &cycle, &orderID, &renewServiceID, &productID); err != nil {
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
	interval, err := CycleInterval(cycle)
	if err != nil {
		return err
	}
	if renewServiceID.Valid && renewServiceID.Int64 > 0 {
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
	} else {
		var svcIDNew int64
		var userIDFromOrder int64
		if err := tx.QueryRowContext(ctx,
			`SELECT user_id, product_id FROM orders WHERE id=$1`, orderID).Scan(&userIDFromOrder, &productID); err != nil {
			return fmt.Errorf("订单缺失: %w", err)
		}
		expiresAt, _ := CycleAddDate(time.Now().UTC(), cycle)
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO services(user_id,product_id,server_id,order_id,name,status,expires_at,upstream_provider,upstream_pid)
			 SELECT $1,$2,p.server_id,$3,p.name,0,$4,coalesce(s.provider,''),p.upstream_pid
			 FROM products p LEFT JOIN servers s ON s.id=p.server_id WHERE p.id=$2 RETURNING id`,
			userIDFromOrder, productID, orderID, expiresAt).Scan(&svcIDNew); err != nil {
			return err
		}
		svcID = svcIDNew
		newService = true
	}
	if p.Jobs != nil {
		kind := "renew"
		if newService {
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
		p.Notifier.Notify(ctx, userID, "支付成功", "账单 "+invoiceNo+" 已通过余额支付，服务开通中。")
	}
	if p.Jobs == nil {
		if renewServiceID.Valid && renewServiceID.Int64 > 0 {
			p.renewAsync(svcID, cycle, orderID)
		} else if newService {
			p.provisionAsync(svcID, productID, cycle)
		}
	}
	return nil
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

// MarkPaid 原子核销账单并开通/续期服务。幂等：重复调用返回 ErrAlreadyPaid。
func (p *Payment) MarkPaid(ctx context.Context, invoiceNo, tradeNo, gatewayCode string, attemptIDs ...int64) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var invID, userID, orderID int64
	var status int16
	var kind string
	err = tx.QueryRowContext(ctx,
		`SELECT id,user_id,coalesce(order_id,0),status,kind FROM invoices WHERE no=$1 FOR UPDATE`, invoiceNo).
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
		return fmt.Errorf("账单不可支付")
	}
	now := time.Now().UTC()
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
	if kind == "recharge" {
		var amount string
		if err := tx.QueryRowContext(ctx, `SELECT amount::text FROM invoices WHERE id=$1`, invID).Scan(&amount); err != nil {
			return err
		}
		if ok, err := validPositiveAmount(ctx, tx, amount); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("充值金额无效")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance+$2::numeric WHERE id=$1`, userID, amount); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO balance_logs(user_id,amount,balance_after,type,note)
			 SELECT $1,$2::numeric,balance,'recharge',$3 FROM users WHERE id=$1`,
			userID, amount, "在线充值 "+invoiceNo); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE invoices SET status=1,paid_at=$2,gateway=$3,trade_no=$4,paid_amount=$5,fee_percent=$6,fee_amount=$7 WHERE id=$1`,
			invID, now, gatewayCode, tradeNo, paidAmount, feePercent, feeAmount); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE payment_attempts SET status=1,provider_trade_no=$2,paid_at=$3 WHERE id=$1`,
			attemptID, tradeNo, now)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("支付记录不存在或已处理")
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if p.Notifier != nil {
			p.Notifier.Notify(ctx, userID, "充值成功", "余额已充值 "+amount+" 元。")
		}
		return nil
	}
	var productID int64
	var cycle string
	var renewServiceID sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT product_id,cycle,service_id FROM orders WHERE id=$1`, orderID).
		Scan(&productID, &cycle, &renewServiceID)
	if err != nil {
		return fmt.Errorf("订单缺失: %w", err)
	}
	if renewServiceID.Valid && renewServiceID.Int64 <= 0 {
		renewServiceID.Valid = false
	}
	if !renewServiceID.Valid {
		if err := p.reserveStock(ctx, tx, productID, orderID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE invoices SET status=1,paid_at=$2,gateway=$3,trade_no=$4,paid_amount=$5,fee_percent=$6,fee_amount=$7 WHERE id=$1`,
		invID, now, gatewayCode, tradeNo, paidAmount, feePercent, feeAmount); err != nil {
		return err
	}
	// 只结束本次网关实例最近的一条支付尝试，保留同一账单切换网关的历史记录。
	res, err := tx.ExecContext(ctx,
		`UPDATE payment_attempts SET status=1,provider_trade_no=$2,paid_at=$3 WHERE id=$1`,
		attemptID, tradeNo, now)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("支付记录不存在或已处理")
	}
	if err := tx.QueryRowContext(ctx,
		`UPDATE orders SET status=1,paid_at=$2 WHERE id=$1 AND status=0 RETURNING id`, orderID, now).Scan(&orderID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	interval, err := CycleInterval(cycle)
	if err != nil {
		return err
	}

	// 续费单：直接延期指定服务并触发上游 Renew
	isRenew := false
	var svcID int64
	if renewServiceID.Valid && renewServiceID.Int64 > 0 {
		isRenew = true
		svcID = renewServiceID.Int64
		var svcStatus int16
		var transition string
		if err := tx.QueryRowContext(ctx, `SELECT status,coalesce(transition_state,'') FROM services WHERE id=$1 FOR UPDATE`, svcID).Scan(&svcStatus, &transition); err != nil {
			return err
		}
		if (svcStatus != 1 && svcStatus != 2) || transition != "" {
			return fmt.Errorf("服务当前状态不可续费")
		}
		// 检查是否已授权，防止重复续费
		if p.PeriodGrants != nil {
			if err := p.PeriodGrants.Grant(ctx, tx, svcID, invID, cycle); err != nil {
				return err
			}
		}
		// 续费：从当前到期时间（或现在，取较晚者）延长一个周期
		if _, err := tx.ExecContext(ctx,
			`UPDATE services SET expires_at=GREATEST(expires_at,now()) + $2::interval, expire_warn_sent=false WHERE id=$1 AND status IN (1,2) AND coalesce(transition_state,'')=''`,
			svcID, interval); err != nil {
			return err
		}
	} else {
		// 新购：每次支付独立建一个待开通服务（不合并既有同产品服务；续费走 isRenew 分支）
		var svcIDNew int64
		expiresAt, _ := CycleAddDate(now, cycle)
		err = tx.QueryRowContext(ctx,
			`INSERT INTO services(user_id,product_id,server_id,order_id,name,status,expires_at,upstream_provider,upstream_pid)
			 SELECT $1,$2,p.server_id,$3,p.name,0,$4,coalesce(s.provider,''),p.upstream_pid
			 FROM products p LEFT JOIN servers s ON s.id=p.server_id WHERE p.id=$2 RETURNING id`,
			userID, productID, orderID, expiresAt).Scan(&svcIDNew)
		if err != nil {
			return err
		}
		svcID = svcIDNew
	}

	if p.Jobs != nil {
		kind := "provision"
		if isRenew {
			kind = "renew"
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
		p.Notifier.Notify(ctx, userID, "支付成功", "账单 "+invoiceNo+" 已支付，服务开通中。")
	}
	if p.Jobs == nil {
		if isRenew {
			p.renewAsync(svcID, cycle, orderID)
		} else {
			p.provisionAsync(svcID, productID, cycle)
		}
	}
	return nil
}

const opRenewTimeout = 90 * time.Second

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
		lc := &Lifecycle{DB: p.DB, Servers: p.Servers, Products: p.Products}
		if rerr := lc.Renew(rctx, serviceID, cycle, orderID); rerr != nil {
			log.Printf("[renew] service %d 上游续费失败: %v", serviceID, rerr)
		}
	}()
}

func (p *Payment) provision(ctx context.Context, serviceID, _ int64, cycle string) error {
	var serverID sql.NullInt64
	var providerCode string
	var upstreamPID int64
	if err := p.DB.QueryRowContext(ctx,
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
		if _, err := p.DB.ExecContext(ctx, `UPDATE services SET status=1,provision_error='' WHERE id=$1 AND status=0`, serviceID); err != nil {
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
	unlock, err := lockUpstreamAccount(ctx, p.DB, cfg)
	if err != nil {
		return p.failProvision(ctx, serviceID, fmt.Errorf("锁定上游账户失败: %w", err))
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
	}, &serviceCheckpoint{repo: p.Provisions, serviceID: serviceID, ctx: ctx})
	if err != nil {
		return p.failProvision(ctx, serviceID, err)
	}
	if res.UpstreamHostID > 0 {
		// 开通成功即激活（此前只写 host id，等 30s 状态同步 cron 才置 1，用户看到长时间"待开通"）
		if _, err := p.DB.ExecContext(ctx,
			`UPDATE services SET upstream_host_id=$2, status=1, provision_error='' WHERE id=$1 AND status=0`, serviceID, res.UpstreamHostID); err != nil {
			return err
		}
	}
	// 供应商回传了实例密码（如 EasyPanel）：加密落库供详情页展示与面板直登。
	if res.Password != "" && p.Crypt != nil {
		if enc, cerr := p.Crypt.Encrypt(res.Password); cerr == nil {
			if _, uerr := p.DB.ExecContext(ctx,
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
	if err := p.DB.QueryRowContext(ctx,
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
		if _, err := p.DB.ExecContext(ctx, `UPDATE services SET provision_error=$2 WHERE id=$1`, serviceID, msg); err != nil {
			return fmt.Errorf("记录开通失败: %w（原错误：%v）", err, msg)
		}
	}
	return err
}

func (p *Payment) clearProvisionError(ctx context.Context, serviceID int64) {
	p.DB.ExecContext(ctx, `UPDATE services SET provision_error='' WHERE id=$1`, serviceID)
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
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
