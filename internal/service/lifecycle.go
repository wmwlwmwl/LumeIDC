package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// Lifecycle 服务生命周期操作（接上游）。
type Lifecycle struct {
	db        *sql.DB
	Servers   *repo.Servers
	Products  *repo.Products
	Providers *server.Registry
	// Provisions 续费账单检查点存取（复用已建的上游续费单，避免重试重复建单）。
	Provisions *repo.ProvisionRepo
}

type serviceRef struct {
	ID               int64
	Status           int16
	ServerID         sql.NullInt64
	UpstreamPID      int64
	UpstreamProvider string
	UpstreamHost     int64
	UpstreamCycle    string
}

// loadService 读服务+产品绑定信息。
func (lc *Lifecycle) loadService(ctx context.Context, serviceID int64) (*serviceRef, error) {
	var s serviceRef
	err := lc.db.QueryRowContext(ctx,
		`SELECT sv.id,sv.status,coalesce(sv.server_id,p.server_id),
		        coalesce(nullif(sv.upstream_pid,0),p.upstream_pid),
		        coalesce(nullif(sv.upstream_provider,''),srv.provider,''),
		        sv.upstream_host_id,coalesce(p.upstream_cycle,'')
		 FROM services sv JOIN products p ON p.id=sv.product_id
		 LEFT JOIN servers srv ON srv.id=coalesce(sv.server_id,p.server_id)
		 WHERE sv.id=$1`,
		serviceID).Scan(&s.ID, &s.Status, &s.ServerID, &s.UpstreamPID, &s.UpstreamProvider,
		&s.UpstreamHost, &s.UpstreamCycle)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// resolveProvider 按供应商代码+服务器ID 解析上游 Provider 与 Config（lifecycle/console 共用）。
func resolveProvider(ctx context.Context, providers *server.Registry, servers *repo.Servers, providerCode string, serverID int64) (server.Provider, server.Config, error) {
	prov, err := providers.Get(providerCode)
	if err != nil {
		return nil, server.Config{}, err
	}
	sv, err := servers.Get(ctx, serverID)
	if err != nil {
		return nil, server.Config{}, fmt.Errorf("读取服务器失败: %w", err)
	}
	return prov, upstreamConfig(sv), nil
}

func (lc *Lifecycle) providerFor(ctx context.Context, s *serviceRef) (server.Provider, server.Config, error) {
	// 同 console.resolve：hostID>0 即有上游；upstream_pid=0 为合法弹性模式（EasyPanel）。
	if !s.ServerID.Valid || s.UpstreamHost == 0 {
		return nil, server.Config{}, errNoUpstream
	}
	return resolveProvider(ctx, lc.Providers, lc.Servers, s.UpstreamProvider, s.ServerID.Int64)
}

var errNoUpstream = lifecycleErr("该服务未绑定上游")

type lifecycleErr string

func (e lifecycleErr) Error() string { return string(e) }

func upstreamConfig(sv *repo.Server) server.Config {
	return server.Config{APIURL: sv.APIURL, APIUsername: sv.APIUsername, APIKey: sv.APIKey, CredentialRevision: sv.CredentialRevision}
}

const opTimeout = 60 * time.Second

// renewPendingState 续费待处理（上游涨价等待人工决定 / 上游欠费等待充值）。
// 复用 transition_state 当锁：钱已收、本地到期时间已延长，不加锁用户会重复下单续费。
const renewPendingState = "renew_pending"

// renewPriceOkKey 续费价格确认标记（键带订单号）：管理员确认按上游新价续费后写入，
// 本次续费重试据此跳过续费前比价。续费比价在建账单之前，没有可复用的账单检查点，
// 只能靠这个标记表达"管理员已确认"。
func renewPriceOkKey(orderID int64) string {
	return fmt.Sprintf("renew_price_ok_%d", orderID)
}

// renewDoneCkKey 续费终态检查点键：成功写时间戳，管理员退款取消写 refunded。
// 后台退款后不能只改服务状态——排队中的续费任务还会再跑一遍，把已退款的服务续到上游。
func renewDoneCkKey(orderID int64) string {
	return fmt.Sprintf("renew_order_%d", orderID)
}

// Renew 向上游续费。
// 使用 checkpoint 记录续费结果，避免重试时重复创建续费单。
func (lc *Lifecycle) Renew(ctx context.Context, serviceID int64, cycle string, orderID int64) error {
	s, err := lc.loadService(ctx, serviceID)
	if err != nil {
		return err
	}
	prov, cfg, err := lc.providerFor(ctx, s)
	if err == errNoUpstream {
		return nil // 本地服务，无需上游操作
	}
	if err != nil {
		return err
	}
	if orderID <= 0 {
		return fmt.Errorf("续费任务缺少订单号")
	}
	checkpointKey := renewDoneCkKey(orderID)
	if existing, ok, err := lc.getCheckpoint(ctx, serviceID, checkpointKey); err != nil {
		// 读取失败不能视为"未处理过"：重试会重复创建续费单
		return fmt.Errorf("读取续费 checkpoint 失败: %w", err)
	} else if ok && existing != "" {
		log.Printf("[lifecycle] service %d 已有续费 checkpoint，跳过: %s", serviceID, existing)
		lc.clearRenewPending(ctx, serviceID) // 上次成功后清理失败时补一次
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	// 上游偷偷改价核对：续费账单由上游按当前价出，高于下单时的成本额就说明上游涨了价，
	// 而用户已按旧价付过钱，继续会让平台吃差价。在建账单之前拦下，上游不留未付账单。
	if perr := lc.checkRenewPrice(cctx, prov, cfg, s, cycle, orderID); perr != nil {
		lc.markRenewPending(ctx, serviceID, perr)
		return perr
	}
	// 续费账单检查点由 Provider 读写：重试时复用同一张上游账单，不会重复建单。
	var ck server.CheckpointStore
	if lc.Provisions != nil {
		ck = &serviceCheckpoint{repo: lc.Provisions, serviceID: serviceID, ctx: cctx}
	}
	if err := prov.Renew(cctx, cfg, s.UpstreamHost, cycle, ck); err != nil {
		log.Printf("[lifecycle] service %d 上游续费失败: %v", serviceID, err)
		// 需要人工或等外部条件的失败（上游涨价 / 账单被删 / 欠费）：标记待处理，
		// 由管理员在后台二选一（重试=按上游新价续费 / 退款给用户）。
		if server.IsManualReview(err) || server.IsRetryLater(err) {
			lc.markRenewPending(ctx, serviceID, err)
		}
		return fmt.Errorf("续费失败: %w", err)
	}
	// 续费成功，写入 checkpoint
	if err := lc.setCheckpoint(ctx, serviceID, checkpointKey, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return &server.ManualReviewError{Msg: fmt.Sprintf("上游续费已返回成功，但保存终态检查点失败，请人工核对: %v", err)}
	}
	lc.clearRenewPending(ctx, serviceID)
	return nil
}

// markRenewPending 记录续费待处理原因（展示在后台服务列表「失败原因」列）并锁住服务，
// 避免待处理期间用户重复下单续费。已在其它过渡态（如升级中）时不覆盖，以免破坏那个流程的锁。
func (lc *Lifecycle) markRenewPending(ctx context.Context, serviceID int64, cause error) {
	msg := "续费失败：需要人工处理"
	if cause != nil {
		msg = "续费失败：" + cause.Error()
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if _, err := lc.db.ExecContext(ctx,
		`UPDATE services SET transition_state=$2, provision_error=$3
		  WHERE id=$1 AND coalesce(transition_state,'') IN ('',$2)`,
		serviceID, renewPendingState, msg); err != nil {
		log.Printf("[lifecycle] service %d 记录续费待处理原因失败: %v", serviceID, err)
	}
}

// clearRenewPending 解除续费待处理状态（续费成功或管理员退款后调用）。
// 只清自己设的锁，不误动升级/停机的过渡态。
func (lc *Lifecycle) clearRenewPending(ctx context.Context, serviceID int64) {
	if _, err := lc.db.ExecContext(ctx,
		`UPDATE services SET transition_state='', provision_error=''
		  WHERE id=$1 AND coalesce(transition_state,'')=$2`, serviceID, renewPendingState); err != nil {
		log.Printf("[lifecycle] service %d 清除续费待处理状态失败: %v", serviceID, err)
	}
}

// checkRenewPrice 续费前核对上游当前价是否已高于下单时的成本额。
// 只拦涨价（上游降价我们赚更多，不该拦）；供应商不支持单商品快照、或订单无成本快照时跳过。
// 返回 PriceChangedError 时任务进人工复核，由管理员决定按此价续费还是退款给用户。
func (lc *Lifecycle) checkRenewPrice(ctx context.Context, prov server.Provider, cfg server.Config, s *serviceRef, cycle string, orderID int64) error {
	// 管理员已确认按上游新价续费（后台点「重试续费」写入）：跳过比价，本次重试即强制续费。
	if v, ok, _ := lc.getCheckpoint(ctx, s.ID, renewPriceOkKey(orderID)); ok && v != "" {
		log.Printf("[lifecycle] service %d 续费订单 %d 已确认价格，跳过续费前比价", s.ID, orderID)
		return nil
	}
	fetcher, ok := prov.(server.ProductSnapshotFetcher)
	if !ok {
		return nil
	}
	localCost, selection := lc.orderCostBaseline(ctx, orderID)
	if localCost <= 0 {
		return nil // 无成本快照（老数据），无从比对
	}
	snap, err := fetcher.FetchProductSnapshot(ctx, cfg, s.UpstreamPID)
	if err != nil {
		// 拿不到上游当前价就无法判断是否被改价，按防亏优先转人工，不盲目续费。
		return &server.ManualReviewError{
			Msg: fmt.Sprintf("读取上游当前价失败，无法核对续费价格: %v", err),
		}
	}
	upQuote, qerr := CalculateQuote(snap.ConfigOptions, cycleAmount(snap.Monthly, snap.Quarterly, snap.Yearly, cycle), cycle, selection)
	if qerr != nil {
		return &server.ManualReviewError{
			Msg: fmt.Sprintf("按上游当前配置无法计价，无法核对续费价格: %v", qerr),
		}
	}
	if upQuote.Total-localCost > server.PriceTolerance {
		return &server.PriceChangedError{
			UpstreamAmount: upQuote.Total,
			ExpectAmount:   localCost,
			UpstreamPID:    s.UpstreamPID,
		}
	}
	return nil
}

// orderCostBaseline 读取订单下单时的成本额与配置选择（config_snapshot）。
// 与 payment.orderCostAmount 同源，这里额外要 selection 才能按上游当前配置项复算成本。
func (lc *Lifecycle) orderCostBaseline(ctx context.Context, orderID int64) (float64, map[string]string) {
	var raw []byte
	if err := lc.db.QueryRowContext(ctx,
		`SELECT config_snapshot FROM orders WHERE id=$1`, orderID).Scan(&raw); err != nil {
		return 0, nil
	}
	var snap struct {
		Quote struct {
			Total float64 `json:"total"`
		} `json:"quote"`
		Selection map[string]string `json:"selection"`
	}
	if json.Unmarshal(raw, &snap) != nil {
		return 0, nil
	}
	return snap.Quote.Total, snap.Selection
}

// getCheckpoint 从 services.provision_data 读取 checkpoint。
func (lc *Lifecycle) getCheckpoint(ctx context.Context, serviceID int64, key string) (string, bool, error) {
	var data []byte
	if err := lc.db.QueryRowContext(ctx,
		`SELECT provision_data FROM services WHERE id=$1`, serviceID).Scan(&data); err != nil {
		return "", false, err
	}
	if len(data) == 0 {
		return "", false, nil
	}
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		return "", false, nil
	}
	v, ok := m[key]
	return v, ok, nil
}

// setCheckpoint 写入 checkpoint 到 services.provision_data。
func (lc *Lifecycle) setCheckpoint(ctx context.Context, serviceID int64, key, val string) error {
	payload, err := json.Marshal(map[string]string{key: val})
	if err != nil {
		return err
	}
	_, err = lc.db.ExecContext(ctx,
		`UPDATE services SET provision_data = coalesce(provision_data,'{}')::jsonb || $2::jsonb WHERE id=$1`,
		serviceID, payload)
	return err
}

// transition 服务状态迁移通用流程（停机/解停/删除共用）。
// from/to 为状态迁移边界，cmp 为本地状态条件比较符（"=" 或 "<"，Terminate 用 status<3 表达"未终止皆可删"）；
// state 为过渡状态名；errMsg 为上游失败时的日志与包装文案；op 为对上游的实际操作。
func (lc *Lifecycle) transition(ctx context.Context, s *serviceRef, from, to int16, cmp, state, errMsg string, op func(context.Context, server.Provider, server.Config, int64) error) error {
	prov, cfg, err := lc.providerFor(ctx, s)
	if err == errNoUpstream {
		_, err = lc.db.ExecContext(ctx,
			`UPDATE services SET status=$1 WHERE id=$2 AND status`+cmp+`$3`, to, s.ID, from)
		return err
	}
	if err != nil {
		return err
	}
	// 设置过渡状态
	if _, err := lc.db.ExecContext(ctx,
		`UPDATE services SET desired_status=$1, transition_state=$4 WHERE id=$2 AND status`+cmp+`$3`,
		to, s.ID, from, state); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if err := op(cctx, prov, cfg, s.UpstreamHost); err != nil {
		log.Printf("[lifecycle] service %d %s: %v", s.ID, errMsg, err)
		// 失败：清除过渡状态，保留原状态
		lc.db.ExecContext(ctx, `UPDATE services SET desired_status=NULL, transition_state='' WHERE id=$1`, s.ID)
		return fmt.Errorf("%s: %w", errMsg, err)
	}
	// 成功：更新状态
	_, err = lc.db.ExecContext(ctx,
		`UPDATE services SET status=$1 WHERE id=$2 AND status`+cmp+`$3`, to, s.ID, from)
	// 无条件清除过渡状态：即便并发的 SyncUpstreamStatus 改了 status，
	// 也不让其卡在 transition_state（否则会被同步长期跳过）。
	if _, e := lc.db.ExecContext(ctx,
		`UPDATE services SET desired_status=NULL, transition_state='' WHERE id=$1`, s.ID); e != nil {
		log.Printf("[lifecycle] service %d 清除过渡状态失败: %v", s.ID, e)
	}
	return err
}

// Suspend 停机：本地状态 + 上游同步。
func (lc *Lifecycle) Suspend(ctx context.Context, serviceID int64) error {
	s, err := lc.loadService(ctx, serviceID)
	if err != nil {
		return err
	}
	if s.Status != 1 {
		return fmt.Errorf("服务当前状态不可停机")
	}
	return lc.transition(ctx, s, 1, 2, "=", "suspending", "上游停机失败",
		func(ctx context.Context, p server.Provider, cfg server.Config, host int64) error {
			return p.Suspend(ctx, cfg, host)
		})
}

// Unsuspend 解除停机。
func (lc *Lifecycle) Unsuspend(ctx context.Context, serviceID int64) error {
	s, err := lc.loadService(ctx, serviceID)
	if err != nil {
		return err
	}
	if s.Status != 2 {
		return fmt.Errorf("服务当前状态不可解除停机")
	}
	return lc.transition(ctx, s, 2, 1, "=", "unsuspending", "上游解除停机失败",
		func(ctx context.Context, p server.Provider, cfg server.Config, host int64) error {
			return p.Unsuspend(ctx, cfg, host)
		})
}

// Terminate 删除：本地终止 + 上游销毁。
func (lc *Lifecycle) Terminate(ctx context.Context, serviceID int64) error {
	s, err := lc.loadService(ctx, serviceID)
	if err != nil {
		return err
	}
	if s.Status == 3 {
		return nil
	}
	return lc.transition(ctx, s, 3, 3, "<", "terminating", "上游删除失败",
		func(ctx context.Context, p server.Provider, cfg server.Config, host int64) error {
			return p.Terminate(ctx, cfg, host)
		})
}

// TerminateLocal 仅本地删除：置 status=3（含清除过渡状态），不调用上游。
// 用于后台「本地删除」——保留上游实例，便于找回或避免误删。
func (lc *Lifecycle) TerminateLocal(ctx context.Context, serviceID int64) error {
	if _, err := lc.db.ExecContext(ctx,
		`UPDATE services SET status=3, desired_status=NULL, transition_state='' WHERE id=$1 AND status<3`,
		serviceID); err != nil {
		return err
	}
	return nil
}

// RetryProvision 手动重试开通（pending 状态的服务）。// 通过入队 fulfillment job 实现，不允许与同服务已有 running job 并行。
func (p *Payment) RetryProvision(ctx context.Context, serviceID int64) error {
	var cycle sql.NullString
	err := p.db.QueryRowContext(ctx,
		`SELECT (SELECT cycle FROM orders WHERE id=services.order_id) FROM services WHERE id=$1 AND status=0`,
		serviceID).Scan(&cycle)
	if err != nil {
		return fmt.Errorf("服务不存在或非待开通状态")
	}
	if p.Jobs == nil {
		return fmt.Errorf("履约队列未启用，无法重试开通")
	}
	if err := p.Jobs.EnqueueRetry(ctx, serviceID, 0, "provision", cycle.String); err != nil {
		return err
	}
	p.triggerFulfillment()
	return nil
}

// pendingRenewOrder 定位该服务待处理的续费订单（订单号 + 周期）。
// 以队列里尚未完成的续费任务为准（用户历史续费订单很多，只有这条是当前卡住的）：
// 涨价等人工复核时任务在 manual_review，欠费等待充值时时任务在 retry，两种都要能处置。
func (p *Payment) pendingRenewOrder(ctx context.Context, serviceID int64) (int64, string, error) {
	var orderID int64
	var cycle string
	err := p.db.QueryRowContext(ctx,
		`SELECT j.order_id, j.cycle FROM fulfillment_jobs j
		  WHERE j.service_id=$1 AND j.kind='renew' AND j.order_id IS NOT NULL AND j.status <> 'succeeded'
		  ORDER BY j.id DESC LIMIT 1`, serviceID).Scan(&orderID, &cycle)
	if err != nil {
		return 0, "", fmt.Errorf("没有待处理的续费任务")
	}
	// 已终结的续费（成功或已退款）不能再处置：任务停留在人工复核不会自己消失，
	// 重复点退款会把到期时间多撤回一个周期。
	if p.Provisions != nil {
		v, ok, cerr := p.Provisions.GetCheckpoint(ctx, serviceID, renewDoneCkKey(orderID))
		if cerr != nil {
			return 0, "", fmt.Errorf("读取续费状态失败: %w", cerr)
		}
		if ok && v != "" {
			if v == "refunded" {
				return 0, "", fmt.Errorf("该续费已取消并退款，无需重复操作")
			}
			return 0, "", fmt.Errorf("该续费已完成，无需重复操作")
		}
	}
	return orderID, cycle, nil
}

// RetryRenew 手动重试待处理的续费。先写本次订单的价格确认标记（跳过续费前比价），
// 再带订单号重新入队——重试即"管理员确认按上游新价续费"，与开通/升级的重试语义一致。
func (p *Payment) RetryRenew(ctx context.Context, serviceID int64) error {
	orderID, cycle, err := p.pendingRenewOrder(ctx, serviceID)
	if err != nil {
		return err
	}
	if p.Jobs == nil {
		return fmt.Errorf("履约队列未启用，无法重试续费")
	}
	// 确认价格、清除提示与入队由仓库在服务锁内原子提交，隔离任务不能绕过。
	if err := p.Jobs.EnqueueRetry(ctx, serviceID, orderID, "renew", cycle); err != nil {
		return err
	}
	p.triggerFulfillment()
	return nil
}

// RefundRenew 取消待处理的续费并退款：退续费订单实付 → 订单作废 → 回退本地已延长的到期时间 →
// 解除"续费待处理"。用于上游涨价/账单不可恢复时，管理员选择不给用户继续续费。
// 可重入：以 refunds 表是否已有 done 记录判定退款；先退款后改状态，顺序反了会在退款失败时
// 让服务丢掉续费记录（钱收着、上游也没续上）。
func (p *Payment) RefundRenew(ctx context.Context, adminID, serviceID int64, reason string) error {
	if err := repo.NewFulfillmentJobs(p.db).CheckRetry(ctx, serviceID); err != nil {
		return err
	}
	orderID, cycle, err := p.pendingRenewOrder(ctx, serviceID)
	if err != nil {
		return err
	}
	var amount string
	if err := p.db.QueryRowContext(ctx, `SELECT amount::text FROM orders WHERE id=$1`, orderID).Scan(&amount); err != nil {
		return fmt.Errorf("读取续费订单失败: %w", err)
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
	interval, ierr := CycleInterval(cycle)
	if ierr != nil {
		// 周期异常（老数据）不能猜着改到期时间：退款照退，到期时间交人工核对。
		log.Printf("[renew] service %d 续费周期 %q 无效，未回退到期时间: %v", serviceID, cycle, ierr)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET status=2 WHERE id=$1`, orderID); err != nil {
		return err
	}
	// 到期时间回退：续费支付时已把 expires_at 延长一个周期，退款必须减回去，否则用户白得一期。
	// ponytail: 按周期整体加减；若中途管理员手工改过到期时间，回退结果会偏离，需人工核对。
	if ierr == nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE services SET expires_at = expires_at - $2::interval WHERE id=$1`, serviceID, interval); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE services SET transition_state='', provision_error=''
		  WHERE id=$1 AND coalesce(transition_state,'')=$2`, serviceID, renewPendingState); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// 写终态检查点 + 清掉本次续费的中间检查点：排队中的续费任务再跑时会据此短路，
	// 否则已退款的服务会被续到上游（平台白付一期）。上游账单号是服务级键（不带订单号），
	// 必须一并清除，否则用户下次续费会误复用这张属于已退款订单的账单。
	if p.Provisions != nil {
		if err := p.Provisions.SetCheckpoint(ctx, serviceID, renewDoneCkKey(orderID), "refunded"); err != nil {
			log.Printf("[renew] service %d 写入续费退款 checkpoint 失败: %v", serviceID, err)
		}
		for _, k := range []string{renewPriceOkKey(orderID), server.CheckpointRenewInvoice} {
			if err := p.Provisions.DeleteCheckpoint(ctx, serviceID, k); err != nil {
				log.Printf("[renew] service %d 清除检查点 %s 失败: %v", serviceID, k, err)
			}
		}
	}
	return nil
}

// RetryUpgrade 手动重试失败的升级（服务处于"升级中"）。重新入队该升级订单的履约任务：
// 上游账单已记在检查点里，重试会复用同一张账单直接付款（= 管理员确认按上游新价强制开通）。
func (p *Payment) RetryUpgrade(ctx context.Context, serviceID int64) error {
	var orderID int64
	var cycle string
	if err := p.db.QueryRowContext(ctx,
		`SELECT o.id, coalesce(o.cycle,'')
		   FROM services sv JOIN orders o ON o.service_id=sv.id
		  WHERE sv.id=$1 AND coalesce(sv.transition_state,'')='upgrading' AND o.kind='upgrade' AND o.status=1
		  ORDER BY o.id DESC LIMIT 1`, serviceID).Scan(&orderID, &cycle); err != nil {
		return fmt.Errorf("服务不存在或没有待处理的升级")
	}
	if p.Jobs == nil {
		return fmt.Errorf("履约队列未启用，无法重试升级")
	}
	if err := p.Jobs.EnqueueRetry(ctx, serviceID, orderID, "upgrade", cycle); err != nil {
		return err
	}
	p.triggerFulfillment()
	return nil
}

// RefundUpgrade 取消待处理的升级并退款：全额退回升级订单实付（差价）→ 订单作废 →
// 清除"升级中"状态与上游账单检查点，服务保持原产品继续可用。
// 用于上游涨价后管理员选择不给用户按新价开通。
// 可重入：以 refunds 表是否已有 done 记录判定，退款成功但关单失败时重试只补齐关单。
func (p *Payment) RefundUpgrade(ctx context.Context, adminID, serviceID int64, reason string) error {
	if err := repo.NewFulfillmentJobs(p.db).CheckRetry(ctx, serviceID); err != nil {
		return err
	}
	var orderID int64
	var amount string
	if err := p.db.QueryRowContext(ctx,
		`SELECT o.id, o.amount::text
		   FROM services sv JOIN orders o ON o.service_id=sv.id
		  WHERE sv.id=$1 AND coalesce(sv.transition_state,'')='upgrading' AND o.kind='upgrade' AND o.status=1
		  ORDER BY o.id DESC LIMIT 1`, serviceID).Scan(&orderID, &amount); err != nil {
		return fmt.Errorf("服务不存在或没有待处理的升级")
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
	// 先退款后改状态：顺序反了会在退款失败时让服务失去升级记录（钱收着、升级也没了）。
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET status=2 WHERE id=$1`, orderID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE services SET transition_state='', provision_error=''
		  WHERE id=$1 AND coalesce(transition_state,'')='upgrading'`, serviceID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// 标记该升级订单已终局（重试任务据此短路，不重复退款），并清掉其上游账单检查点避免误复用。
	if p.Provisions != nil {
		if err := p.Provisions.SetCheckpoint(ctx, serviceID, upgradeDoneCkKey(orderID), "refunded"); err != nil {
			log.Printf("[upgrade] service %d 写入升级退款 checkpoint 失败: %v", serviceID, err)
		}
		key := server.CheckpointUpgradeInvoicePrefix + strconv.FormatInt(orderID, 10)
		if err := p.Provisions.DeleteCheckpoint(ctx, serviceID, key); err != nil {
			log.Printf("[upgrade] service %d 清除上游升级账单检查点失败: %v", serviceID, err)
		}
	}
	return nil
}

// defaultUpstreamMissThreshold 连续多少次「上游不可见」才判定资源已消失。
// 同步每 30s 一轮，20 次 ≈ 10 分钟：足以排除单次抖动，又不至于让服务挂上一整天。
const defaultUpstreamMissThreshold = 20

// 批量误删护栏：同一台上游服务器本轮缺失数 ≥ missBatchMin 且占比 ≥ missBatchRatio 时，
// 判定为「上游整体异常」（接口挂了、鉴权失效、上游维护），本轮只告警不删除。
// ponytail: 本地标记删除不可逆，宁可漏删一轮，也不能因上游一次抖动整站误删。
const missBatchMin, missBatchRatio = 5, 0.5

// syncProbe 单条服务的上游探测结果（探测与写库分离，便于按服务器聚合判定整体异常）。
type syncProbe struct {
	serviceID int64
	serverID  int64
	status    string
	hostname  string
	err       error
}

// gone 判定探测结果是否意味着「上游该实例已不存在」：
// 快路径为上游明确回 terminated/deleted 等终态；慢路径为 Provider 标记 ErrHostMissing。
func (p syncProbe) gone() bool {
	if p.err != nil {
		return server.IsHostMissing(p.err)
	}
	st, ok := mapUpstreamStatus(p.status)
	return ok && st == 3
}

// applySyncProbes 应用一轮探测结果：先按服务器剔除「上游整体异常」，再逐条落地。
func (lc *Lifecycle) applySyncProbes(ctx context.Context, probes []syncProbe) {
	if len(probes) == 0 {
		return
	}
	type agg struct{ total, missing int }
	byServer := make(map[int64]*agg)
	for _, p := range probes {
		a := byServer[p.serverID]
		if a == nil {
			a = &agg{}
			byServer[p.serverID] = a
		}
		a.total++
		if p.gone() {
			a.missing++
		}
	}
	broken := make(map[int64]bool, len(byServer))
	for sid, a := range byServer {
		if a.missing >= missBatchMin && float64(a.missing)/float64(a.total) >= missBatchRatio {
			broken[sid] = true
			log.Printf("[sync] 服务器 %d 本轮 %d/%d 条服务上游不可见，判定为上游整体异常，本轮不做删除",
				sid, a.missing, a.total)
		}
	}
	threshold := lc.upstreamMissThreshold(ctx)
	for _, p := range probes {
		switch {
		case p.err != nil && !server.IsHostMissing(p.err):
			// 网络/超时/鉴权/解析失败属于我方或链路故障，与资源是否消失无关，不计缺失分。
			log.Printf("[sync] service %d 状态查询失败: %v", p.serviceID, p.err)
		case p.err != nil:
			if broken[p.serverID] {
				continue
			}
			lc.countUpstreamMiss(ctx, p.serviceID, threshold)
		default:
			st, ok := mapUpstreamStatus(p.status)
			if !ok {
				// 未知上游状态只记录不覆盖本地
				log.Printf("[sync] service %d 未知上游状态: %s", p.serviceID, p.status)
				continue
			}
			if st == 3 && broken[p.serverID] {
				log.Printf("[sync] service %d 上游报已删除，但所属服务器本轮整体异常，暂不处理", p.serviceID)
				continue
			}
			lc.applyUpstreamStatus(ctx, p.serviceID, st, p.hostname)
		}
	}
}

// applyUpstreamStatus 落地上游状态，并清零缺失计数——能查到就说明资源还在。
func (lc *Lifecycle) applyUpstreamStatus(ctx context.Context, serviceID int64, status int16, hostname string) {
	if _, err := lc.db.ExecContext(ctx,
		`UPDATE services SET status=$2, hostname=coalesce(nullif($3,''), hostname),
		        upstream_miss_count=0, upstream_miss_since=NULL
		 WHERE id=$1 AND status<3`, serviceID, status, hostname); err != nil {
		log.Printf("[sync] service %d 状态更新失败: %v", serviceID, err)
	}
}

// countUpstreamMiss 累计一次「上游不可见」，达到阈值即标记本地已删除。
// 计数与判定放在同一条语句里，避免读回再写产生竞态。
func (lc *Lifecycle) countUpstreamMiss(ctx context.Context, serviceID int64, threshold int) {
	var dropped bool
	err := lc.db.QueryRowContext(ctx,
		`WITH bump AS (
			UPDATE services SET upstream_miss_count = upstream_miss_count + 1,
			       upstream_miss_since = coalesce(upstream_miss_since, now())
			 WHERE id=$1 AND status < 3
			 RETURNING id, upstream_miss_count
		 )
		 UPDATE services SET status=3
		  WHERE id IN (SELECT id FROM bump WHERE upstream_miss_count >= $2)
		 RETURNING true`, serviceID, threshold).Scan(&dropped)
	switch {
	case err == sql.ErrNoRows:
		// 未达阈值（或服务已终止）：静默等待下一轮
	case err != nil:
		log.Printf("[sync] service %d 累计上游缺失失败: %v", serviceID, err)
	default:
		log.Printf("[sync] service %d 上游持续不可见已达 %d 次，已标记删除（请核对上游是否真的释放）",
			serviceID, threshold)
	}
}

// upstreamMissThreshold 读取后台缺失阈值（隐藏键 upstream_miss_threshold），缺省 20。
func (lc *Lifecycle) upstreamMissThreshold(ctx context.Context) int {
	var v string
	if err := lc.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key='upstream_miss_threshold'`).Scan(&v); err == nil {
		if n, e := strconv.Atoi(strings.TrimSpace(v)); e == nil && n >= 1 && n <= 100000 {
			return n
		}
	}
	return defaultUpstreamMissThreshold
}

// SyncUpstreamStatus 将本地服务状态同步为上游真实状态（按上游）。
// ponytail: 仅覆盖已绑定上游 host 的服务；查询密集度=服务数/周期，规模大时建议增量+过期过滤。
// 跳过过渡中（transition_state!=”）和已终止（status=3）的服务，避免复活或干扰进行中的操作。
// 使用 LIMIT 分页避免一次加载过多服务；未知上游状态只记录不覆盖本地。
func (lc *Lifecycle) SyncUpstreamStatus(ctx context.Context) {
	// 同步租约：防止并发同步。
	// ponytail: 会话级 advisory lock 必须绑定专用连接——连接池上执行时加锁/解锁会落到
	// 不同连接，解锁无效且锁泄漏后 try_lock 永远失败，同步静默停摆；
	// 沿用 upstream_lock.go 的 db.Conn 模式。
	leaseKey := "sync_upstream_status"
	conn, err := lc.db.Conn(ctx)
	if err != nil {
		log.Printf("[sync] 获取连接失败: %v", err)
		return
	}
	var gotLease bool
	if err := conn.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock(hashtext($1))`, leaseKey).Scan(&gotLease); err != nil {
		log.Printf("[sync] 尝试获取同步租约失败: %v", err)
		_ = conn.Close()
		return
	}
	if !gotLease {
		_ = conn.Close()
		return
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, leaseKey)
		_ = conn.Close()
	}()

	const pageSize = 100
	var probes []syncProbe
	offset := 0
	for {
		rows, err := lc.db.QueryContext(ctx,
			`SELECT sv.id, srv.id, srv.api_url, srv.api_username, srv.api_key, sv.upstream_host_id,
			        coalesce(nullif(sv.upstream_provider,''),srv.provider,'')
			 FROM services sv
			 JOIN servers srv ON srv.id=sv.server_id
			 WHERE sv.upstream_host_id > 0 AND sv.status IN (0,1,2) AND sv.transition_state = ''
			 ORDER BY sv.id LIMIT $1 OFFSET $2`, pageSize, offset)
		if err != nil {
			log.Printf("[sync] 查询上游服务失败: %v", err)
			return
		}
		type row struct {
			id       int64
			serverID int64
			cfg      server.Config
			host     int64
			provider string
		}
		var list []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.serverID, &r.cfg.APIURL, &r.cfg.APIUsername, &r.cfg.APIKey, &r.host, &r.provider); err != nil {
				log.Printf("[sync] 读取服务失败: %v", err)
				continue
			}
			list = append(list, r)
		}
		// 每页读完立即释放连接，不 defer 到函数返回（分页循环会堆积 rows）
		err = rows.Err()
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			log.Printf("[sync] 遍历服务失败: %v", err)
			return
		}
		if len(list) == 0 {
			break
		}
		for _, r := range list {
			prov, err := lc.Providers.Get(r.provider)
			if err != nil {
				log.Printf("[sync] service %d 供应商错误: %v", r.id, err)
				continue
			}
			cc, cancel := context.WithTimeout(ctx, opTimeout)
			up, err := prov.Status(cc, r.cfg, r.host)
			cancel()
			// 探测与写库分离：攒满一轮再统一判定，才能按服务器聚合识别"上游整体异常"。
			probes = append(probes, syncProbe{serviceID: r.id, serverID: r.serverID,
				status: up.Status, hostname: up.Hostname, err: err})
		}
		offset += len(list)
	}
	lc.applySyncProbes(ctx, probes)
}

// mapUpstreamStatus 上游 domainstatus -> 本地 status；未知状态返回 false 不改动。
// 映射对齐魔方财务同平台对接文档：Cancelled（被取消）/Fraud（欺诈）/Deleted（已删除）
// 均视为本地"已删除"；terminated（上游自定义终止态）同义。
func mapUpstreamStatus(u string) (int16, bool) {
	switch strings.ToLower(u) {
	case "active":
		return 1, true
	case "suspended":
		return 2, true
	case "terminated", "cancelled", "fraud", "deleted":
		return 3, true
	case "pending":
		return 0, true
	}
	return 0, false
}

// upgradeDoneCkKey 升级终态检查点键（done/refunded）：同一升级订单只处理一次，重试不重复退款。
func upgradeDoneCkKey(orderID int64) string {
	return fmt.Sprintf("upgrade_%d", orderID)
}

// Upgrade 上游升降级执行（支付成功后调用）。
//   - 实现了 HostUpgradeProvider 的上游（zjmf）：真正调上游升级（换 host 商品），上游成功后才
//     本地同步换产品/周期/快照；上游失败回滚本地（退回收取的差价或扣回已退差额），使用户不因
//     失败受损。
//   - 未实现该接口的上游（easypanel 本地定价模式）：跳过上游，仅本地换产品。
//
// checkpoint（upgrade_{orderID}）防重复处理：成功写 done，失败退款后写 refunded，重试不重复退款。
// 上游账单检查点（upgrade_invoice_{orderID}）由 Provider 读写：重试复用同一张升级账单。
func (lc *Lifecycle) Upgrade(ctx context.Context, serviceID int64, cycle string, orderID int64) error {
	if orderID <= 0 {
		return fmt.Errorf("升级任务缺少订单号")
	}
	doneKey := upgradeDoneCkKey(orderID)
	if v, ok, _ := lc.getCheckpoint(ctx, serviceID, doneKey); ok && v != "" {
		log.Printf("[lifecycle] service %d 升级订单 %d 已处理（checkpoint=%s），跳过", serviceID, orderID, v)
		return nil
	}
	s, err := lc.loadService(ctx, serviceID)
	if err != nil {
		return err
	}
	// 目标本地产品 + 目标上游商品 id + 差价 + 订单配置快照 + 用户（退款用）
	var targetProductID, targetUpstreamPID int64
	var diffAmount float64
	var snapshot []byte
	var userID int64
	if err := lc.db.QueryRowContext(ctx,
		`SELECT o.user_id, o.target_product_id, coalesce(p.upstream_pid,0), coalesce(o.diff_amount,0)::float8, o.config_snapshot
		 FROM orders o JOIN products p ON p.id=o.target_product_id WHERE o.id=$1`,
		orderID).Scan(&userID, &targetProductID, &targetUpstreamPID, &diffAmount, &snapshot); err != nil {
		return fmt.Errorf("读取升级订单失败: %w", err)
	}
	if targetProductID <= 0 {
		return fmt.Errorf("升级订单缺少目标产品")
	}
	prov, cfg, err := lc.providerFor(ctx, s)
	if err == errNoUpstream {
		// 防御性兜底（升级订单必绑定服务器）：仅本地换产品
		return lc.localUpgradeApply(ctx, serviceID, targetProductID, cycle, snapshot)
	}
	if err != nil {
		return err
	}
	if hp, ok := prov.(server.HostUpgradeProvider); ok {
		// 弹性模式（PIDOptional，如 EasyPanel）没有"上游商品"概念，
		// 升级靠订单里的配置项描述，因此只有要求 PID 的供应商才必须绑定 upstream_pid。
		// 若不加此判断，EP 弹性产品（upstream_pid=0）的升级会被一律判成"目标产品暂不可用"而退款回滚。
		pidOptional := false
		if po, ok2 := prov.(server.PIDOptionalProvider); ok2 && po.PIDOptional() {
			pidOptional = true
		}
		if targetUpstreamPID <= 0 && !pidOptional {
			log.Printf("[lifecycle] service %d 升级目标产品未绑定上游商品，退款回滚（订单 %d）", serviceID, orderID)
			lc.rollbackUpgrade(ctx, userID, diffAmount, orderID)
			lc.clearUpgradeState(ctx, serviceID)
			lc.setCheckpoint(ctx, serviceID, doneKey, "refunded")
			return fmt.Errorf("目标产品暂不可用，已退款回滚")
		}
		cctx, cancel := context.WithTimeout(ctx, opTimeout)
		defer cancel()
		var ck server.CheckpointStore
		if lc.Provisions != nil {
			ck = &serviceCheckpoint{repo: lc.Provisions, serviceID: serviceID, ctx: cctx}
		}
		perr := hp.Upgrade(cctx, cfg, s.UpstreamHost, server.UpgradeRequest{
			OrderID: orderID, TargetPID: targetUpstreamPID, Cycle: cycle, DiffAmount: diffAmount,
			// 弹性模式上游靠这些配置项变更实例配额；缺了就退化成"只改本地记录"。
			ConfigOpts: upgradeConfigOpts(snapshot),
		}, ck)
		if cctx.Err() != nil {
			return &server.ManualReviewError{Msg: "升级执行超时或中断，上游结果未知，请核对账单和实例"}
		}
		if perr != nil {
			log.Printf("[lifecycle] service %d 上游升降级失败: %v", serviceID, perr)
			// 需要人工或等外部条件的失败（上游涨价 / 账单被删 / 余额不足）：保留上游账单
			// 与"升级中"状态、不退款，由管理员在后台二选一（重试=按上游新价开通 / 退款给用户）。
			// 一退款这两个出口就都不成立了（钱退了没法"强制开通"）。
			if server.IsManualReview(perr) || server.IsRetryLater(perr) {
				lc.markUpgradePending(ctx, serviceID, perr)
				return perr
			}
			return &server.ManualReviewError{Msg: "升降级结果未知，未退款，请核对上游账单与实际配置"}
		}
	}
	// 上游成功（或无上游能力）：本地换产品/周期/快照并解除升级中状态
	if err := lc.localUpgradeApply(ctx, serviceID, targetProductID, cycle, snapshot); err != nil {
		return &server.ManualReviewError{Msg: "上游升级已返回成功，但本地应用失败，请人工核对"}
	}
	if err := lc.setCheckpoint(ctx, serviceID, doneKey, "done"); err != nil {
		return &server.ManualReviewError{Msg: fmt.Sprintf("上游升级已返回成功，但保存终态检查点失败，请人工核对: %v", err)}
	}
	return nil
}

// upgradeConfigOpts 从升级订单的配置快照里取出目标配置选择。
// 弹性模式上游（EasyPanel）以这些字段描述实例配额，是升级真正落到上游的唯一依据。
// 快照缺失或格式异常返回 nil——由 Provider 侧兜底（会认为无配额可改）。
func upgradeConfigOpts(snapshot []byte) map[string]string {
	if len(snapshot) == 0 {
		return nil
	}
	var saved struct {
		Selection map[string]string `json:"selection"`
	}
	if err := json.Unmarshal(snapshot, &saved); err != nil {
		return nil
	}
	return saved.Selection
}

// markUpgradePending 记录升级待处理原因（展示在后台服务列表「失败原因」列），
// 服务保持 transition_state='upgrading' 直到管理员决定按新价开通或退款。
func (lc *Lifecycle) markUpgradePending(ctx context.Context, serviceID int64, cause error) {
	msg := "升级失败：需要人工处理"
	if cause != nil {
		msg = "升级失败：" + cause.Error()
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if _, err := lc.db.ExecContext(ctx,
		`UPDATE services SET provision_error=$2 WHERE id=$1`, serviceID, msg); err != nil {
		log.Printf("[lifecycle] service %d 记录升级待处理原因失败: %v", serviceID, err)
	}
}

// clearUpgradeState 解除服务"升级中"过渡状态（失败回滚后调用，避免服务被永久锁定）。
func (lc *Lifecycle) clearUpgradeState(ctx context.Context, serviceID int64) {
	if _, err := lc.db.ExecContext(ctx,
		`UPDATE services SET transition_state='' WHERE id=$1 AND coalesce(transition_state,'')='upgrading'`, serviceID); err != nil {
		log.Printf("[lifecycle] service %d 清除升级中状态失败: %v", serviceID, err)
	}
}

// localUpgradeApply 本地应用升级：换产品/周期/配置快照并解除"升级中"过渡状态。
// 同时清空下单时冻结的固定续费价——换了产品/周期后该价已失效，续费回退到按新产品当前价重算
// （对齐魔方财务「改周期按当前定价重算」）。
func (lc *Lifecycle) localUpgradeApply(ctx context.Context, serviceID, targetProductID int64, cycle string, snapshot []byte) error {
	return localUpgradeApply(ctx, lc.db, serviceID, targetProductID, cycle, snapshot)
}

func localUpgradeApply(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, serviceID, targetProductID int64, cycle string, snapshot []byte) error {
	res, err := q.ExecContext(ctx,
		`UPDATE services SET product_id=$2, upstream_pid=(SELECT upstream_pid FROM products WHERE id=$2), cycle=$3, config_snapshot=$4, transition_state='',
		        renew_monthly=NULL, renew_quarterly=NULL, renew_yearly=NULL, provision_error=''
		 WHERE id=$1 AND (coalesce(transition_state,'')='upgrading' OR (product_id=$2 AND cycle=$3))`,
		serviceID, targetProductID, cycle, snapshot)
	if err != nil {
		return fmt.Errorf("本地应用升级失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("服务升级状态已变化")
	}
	return nil
}

// rollbackUpgrade 升级失败回滚资金：使用户保持"未升级且无资金损失"。
// diff>0 升级场景：支付时已收差价 → 退回余额；diff<0 降级场景：不退款也不扣款（见 prepareUpgrade）。
// 注意：这里写的是用户可见的余额流水，只写中性原因；技术细节（上游报错原文）由调用方记服务端日志。
func (lc *Lifecycle) rollbackUpgrade(ctx context.Context, userID int64, diffAmount float64, orderID int64) {
	tx, err := lc.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("[lifecycle] 升级回滚事务启动失败（订单 %d）: %v", orderID, err)
		return
	}
	defer tx.Rollback()
	amountStr := strconv.FormatFloat(math.Abs(diffAmount), 'f', 2, 64)
	var signed, typ, note string
	if diffAmount > 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET balance=balance+$2::numeric WHERE id=$1`, userID, amountStr); err != nil {
			log.Printf("[lifecycle] 升级回滚退款失败（订单 %d）: %v", orderID, err)
			return
		}
		signed, typ = "+"+amountStr, "refund"
		note = "升级失败退款 订单#" + strconv.FormatInt(orderID, 10)
	} else if diffAmount < 0 {
		// 降级不再退差价（见 prepareUpgrade），所以这里没有"已退的差额"需要扣回。
		// 旧实现会在此扣减用户余额，在"根本没退过钱"的前提下会把余额扣成负数。
		return
	} else {
		return
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO balance_logs(user_id,amount,balance_after,type,note)
		 SELECT $1,$2::numeric,balance,$3,$4 FROM users WHERE id=$1`,
		userID, signed, typ, note); err != nil {
		log.Printf("[lifecycle] 升级回滚余额流水写入失败（订单 %d）: %v", orderID, err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("[lifecycle] 升级回滚提交失败（订单 %d）: %v", orderID, err)
	}
}
