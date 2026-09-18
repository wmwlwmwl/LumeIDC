package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

var ErrRecoveryConflict = errors.New("履约证据存在冲突或已变化，仍保持隔离，请重新核对")

// EasyPanel 续费不经过真实上游账单；历史 recovery 任务由 073 迁移转回 retry，
// 新任务在 recoverExpired 中直接回 retry，不进入本恢复页。

type RecoverySummary struct {
	ServiceID       int64  `json:"service_id"`
	JobID           int64  `json:"job_id"`
	Version         int64  `json:"version"`
	Kind            string `json:"kind"`
	OrderID         int64  `json:"order_id"`
	Cycle           string `json:"cycle"`
	Provider        string `json:"provider"`
	ServerID        int64  `json:"server_id"`
	Account         string `json:"account"`
	HostID          int64  `json:"host_id"`
	CheckpointHost  string `json:"checkpoint_host"`
	UpstreamInvoice string `json:"upstream_invoice"`
	InvoiceID       int64  `json:"invoice_id"`
	InvoiceStatus   int16  `json:"invoice_status"`
	OrderStatus     int16  `json:"order_status"`
	Amount          string `json:"amount"`
	PaidAmount      string `json:"paid_amount"`
	Refunded        string `json:"refunded"`
	Grants          int    `json:"grants"`
	PendingJobs     int    `json:"pending_jobs"`
	TargetProductID int64  `json:"target_product_id"`
	TargetPID       int64  `json:"target_pid"`
	CanResume       bool   `json:"can_resume"`
	ResumeReason    string `json:"resume_reason"`
	BlockReason     string `json:"block_reason"`
}

type RecoveryConfirmation struct {
	JobID            int64  `json:"job_id"`
	ExpectedVersion  int64  `json:"expected_version"`
	Decision         string `json:"decision"`
	Evidence         string `json:"evidence"`
	VerifiedHostID   int64  `json:"verified_host_id"`
	RemoteStable     bool   `json:"remote_stable"`
	BillingVerified  bool   `json:"billing_verified"`
	DeliveryVerified bool   `json:"delivery_verified"`
}

type recoveryState struct {
	RecoverySummary
	status                                int16
	transition                            string
	originalOrder, userID, productID      int64
	orderService, orderUser, orderProduct int64
	orderKind, orderCycle                 string
	checkpoint                            map[string]string
	snapshot                              []byte
	invoiceCount                          int
	targetServer                          int64
	retryHistory                          []int64
}

// recoveryStateTx 显式选择白名单证据；密码、密钥和完整检查点永不进入响应。
func recoveryStateTx(ctx context.Context, tx *sql.Tx, serviceID int64) (*recoveryState, error) {
	s := &recoveryState{}
	s.ServiceID = serviceID
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT sv.status,sv.transition_state,coalesce(sv.order_id,0),sv.user_id,sv.product_id,
 coalesce(sv.server_id,0),sv.upstream_provider,sv.upstream_host_id,sv.provision_data,
 coalesce(srv.name,'') || CASE WHEN coalesce(srv.api_username,'')='' THEN '' ELSE ' / ' || srv.api_username END
 FROM services sv LEFT JOIN servers srv ON srv.id=sv.server_id WHERE sv.id=$1`, serviceID).
		Scan(&s.status, &s.transition, &s.originalOrder, &s.userID, &s.productID, &s.ServerID, &s.Provider, &s.HostID, &raw, &s.Account)
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.checkpoint); err != nil {
			return nil, ErrRecoveryConflict
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT id,claim_version,kind,coalesce(order_id,0),cycle FROM fulfillment_jobs
 WHERE service_id=$1 AND recovery_required AND status='manual_review' ORDER BY id LIMIT 1`, serviceID).
		Scan(&s.JobID, &s.Version, &s.Kind, &s.OrderID, &s.Cycle)
	if err != nil {
		return nil, ErrRecoveryConflict
	}
	if s.OrderID == 0 && s.Kind == "provision" {
		s.OrderID = s.originalOrder
	}
	s.PendingJobs, err = repo.PendingFulfillmentJobsTx(ctx, tx, serviceID)
	if err != nil {
		return nil, err
	}
	s.retryHistory, err = repo.FulfillmentRetryHistoryTx(ctx, tx, s.JobID)
	if err != nil {
		return nil, err
	}
	s.PendingJobs -= len(s.retryHistory)
	err = tx.QueryRowContext(ctx, `SELECT status,coalesce(service_id,0),user_id,product_id,kind,cycle,amount::text,
 coalesce(target_product_id,0),config_snapshot FROM orders WHERE id=$1`, s.OrderID).
		Scan(&s.OrderStatus, &s.orderService, &s.orderUser, &s.orderProduct, &s.orderKind, &s.orderCycle, &s.Amount, &s.TargetProductID, &s.snapshot)
	if err != nil {
		return nil, ErrRecoveryConflict
	}
	if s.TargetProductID > 0 {
		if err := tx.QueryRowContext(ctx, `SELECT upstream_pid,coalesce(server_id,0) FROM products WHERE id=$1`, s.TargetProductID).Scan(&s.TargetPID, &s.targetServer); err != nil {
			return nil, err
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM invoices WHERE order_id=$1`, s.OrderID).Scan(&s.invoiceCount); err != nil {
		return nil, err
	}
	if s.invoiceCount == 1 {
		if err := tx.QueryRowContext(ctx, `SELECT id,status,coalesce(paid_amount,0)::text FROM invoices WHERE order_id=$1`, s.OrderID).Scan(&s.InvoiceID, &s.InvoiceStatus, &s.PaidAmount); err != nil {
			return nil, err
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(amount::numeric),0)::text FROM refunds WHERE order_id=$1 AND status<>'failed'`, s.OrderID).Scan(&s.Refunded); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM service_period_grants WHERE service_id=$1 AND invoice_id=$2 AND cycle=$3`, serviceID, s.InvoiceID, s.Cycle).Scan(&s.Grants); err != nil {
		return nil, err
	}
	var rollback bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM balance_logs WHERE user_id=$1 AND note IN ($2,$3))`, s.userID, "升级失败退款 订单#"+strconv.FormatInt(s.OrderID, 10), "升级失败扣回降级退款 订单#"+strconv.FormatInt(s.OrderID, 10)).Scan(&rollback); err != nil {
		return nil, err
	}
	s.CheckpointHost = s.checkpoint[server.CheckpointUpstreamHostIDs]
	switch s.Kind {
	case "provision":
		s.UpstreamInvoice = s.checkpoint[server.CheckpointUpstreamInvoiceID]
	case "renew":
		s.UpstreamInvoice = s.checkpoint[server.CheckpointRenewInvoice]
	case "upgrade":
		s.UpstreamInvoice = s.checkpoint[server.CheckpointUpgradeInvoicePrefix+strconv.FormatInt(s.OrderID, 10)]
	}
	refunded, err := strconv.ParseFloat(s.Refunded, 64)
	if err != nil {
		return nil, err
	}
	if s.PendingJobs != 1 || s.OrderStatus != 1 || s.invoiceCount != 1 || s.InvoiceStatus != 1 || refunded != 0 || rollback || s.orderUser != s.userID || s.Cycle != s.orderCycle || s.status == 3 {
		s.BlockReason = "订单、账单、退款或未决任务不一致，禁止解除隔离"
	}
	if _, err := CycleInterval(s.Cycle); err != nil {
		s.BlockReason = "履约周期无效"
	}
	if s.Provider != "zjmf" && s.Provider != "easypanel" {
		s.BlockReason = "该供应商尚不支持对账恢复"
	}
	if s.ServerID <= 0 {
		s.BlockReason = "服务未绑定上游账户，不能核对恢复"
	}
	switch s.Kind {
	case "provision":
		if s.originalOrder != s.OrderID || s.orderService != 0 || s.orderKind == "upgrade" || s.orderProduct != s.productID || (s.status != 0 && s.status != 1) || s.transition != "" {
			s.BlockReason = "开通订单与服务状态不一致"
		}
	case "renew":
		if s.orderService != serviceID || s.orderKind == "upgrade" || s.Grants != 1 || s.HostID <= 0 || (s.transition != "" && s.transition != renewPendingState) {
			s.BlockReason = "续费关联、周期授权或服务状态不一致"
		}
		if s.checkpoint[renewDoneCkKey(s.OrderID)] == "refunded" {
			s.BlockReason = "续费已退款"
		}
	case "upgrade":
		if s.orderService != serviceID || s.orderKind != "upgrade" || s.TargetProductID <= 0 || s.targetServer != s.ServerID || s.HostID <= 0 || (s.Provider == "zjmf" && s.TargetPID <= 0) || (s.transition != "upgrading" && s.productID != s.TargetProductID) {
			s.BlockReason = "升级目标、账户或服务状态不一致"
		}
		if s.checkpoint[upgradeDoneCkKey(s.OrderID)] == "refunded" {
			s.BlockReason = "升级已退款"
		}
	default:
		s.BlockReason = "未知履约类型"
	}
	s.ResumeReason = "存在账单或终态检查点，无法在不跳过价格核验或重复履约的前提下安全续跑；请先人工处理上游并核实完成"
	s.CanResume = s.BlockReason == "" && s.UpstreamInvoice == "" && s.checkpoint[renewDoneCkKey(s.OrderID)] == "" && s.checkpoint[upgradeDoneCkKey(s.OrderID)] == "" && s.checkpoint[renewPriceOkKey(s.OrderID)] == ""
	if s.Kind == "provision" {
		s.CanResume = s.CanResume && s.Provider == "zjmf" && s.HostID == 0 && s.CheckpointHost == ""
	}
	if s.Kind == "upgrade" {
		s.CanResume = s.CanResume && s.transition == "upgrading"
	}
	if s.Kind == "provision" && s.Provider == "easypanel" {
		s.ResumeReason = "EasyPanel 重入可能重置站点密码，不开放未执行续跑；请人工完成站点后核对绑定"
	}
	if s.CanResume {
		s.ResumeReason = "仅确认远端已稳定、未执行且无残留待付账单后，可原地重排；不会自动批准价格"
	}
	return s, nil
}

func (p *Payment) FulfillmentRecovery(ctx context.Context, serviceID int64) (*RecoverySummary, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	state, err := recoveryStateTx(ctx, tx, serviceID)
	if err != nil {
		return nil, err
	}
	return &state.RecoverySummary, tx.Commit()
}

func (p *Payment) ConfirmFulfillmentRecovery(ctx context.Context, adminID, serviceID int64, ip string, req RecoveryConfirmation) error {
	req.Evidence = strings.TrimSpace(req.Evidence)
	if adminID <= 0 || req.JobID <= 0 || req.ExpectedVersion < 0 || len([]rune(req.Evidence)) < 10 || len([]rune(req.Evidence)) > 2000 || !req.RemoteStable || !req.BillingVerified || !req.DeliveryVerified {
		return fmt.Errorf("请填写至少十字的核对证据（勿含密码），并确认远端稳定、账单与实际交付均已核对")
	}
	if req.Decision != "confirmed_completed" && req.Decision != "confirmed_not_executed" {
		return fmt.Errorf("请选择有效的对账决策")
	}
	conn, unlock, err := repo.NewFulfillmentJobs(p.db).TryExecutionLock(ctx, serviceID)
	if err != nil {
		return err
	}
	defer unlock()
	// 绑定 host 与开通使用相同账户锁，防止同账户并发恢复把一个 host 分配给多个服务。
	var sid int64
	if err := conn.QueryRowContext(ctx, `SELECT coalesce(server_id,0) FROM services WHERE id=$1`, serviceID).Scan(&sid); err != nil {
		return ErrRecoveryConflict
	}
	if sid <= 0 {
		return ErrRecoveryConflict
	}
	sv, err := p.Servers.Get(ctx, sid)
	if err != nil {
		return err
	}
	accountUnlock, err := lockUpstreamAccount(ctx, p.db, upstreamConfig(sv))
	if err != nil {
		return err
	}
	defer accountUnlock()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout='3s'`); err != nil {
		return err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM services WHERE id=$1 FOR UPDATE`, serviceID).Scan(&id); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM fulfillment_jobs WHERE id=$1 AND service_id=$2 AND claim_version=$3 AND recovery_required AND status='manual_review' FOR UPDATE`, req.JobID, serviceID, req.ExpectedVersion).Scan(&id); err != nil {
		return ErrRecoveryConflict
	}
	state, err := recoveryStateTx(ctx, tx, serviceID)
	if err != nil {
		return err
	}
	if state.JobID != req.JobID || state.Version != req.ExpectedVersion || state.ServerID != sid || state.BlockReason != "" {
		return ErrRecoveryConflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM orders WHERE id=$1 FOR UPDATE`, state.OrderID).Scan(&id); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM invoices WHERE id=$1 FOR UPDATE`, state.InvoiceID).Scan(&id); err != nil {
		return err
	}
	// 锁住账务行后以新快照再核对，不能用锁前读到的支付/退款证据提交。
	state, err = recoveryStateTx(ctx, tx, serviceID)
	if err != nil {
		return err
	}
	if state.BlockReason != "" {
		return ErrRecoveryConflict
	}
	if req.Decision == "confirmed_not_executed" && !state.CanResume {
		return fmt.Errorf("%s", state.ResumeReason)
	}
	if req.Decision == "confirmed_completed" || state.Kind != "provision" {
		if req.VerifiedHostID <= 0 || (state.HostID != 0 && state.HostID != req.VerifiedHostID) || (state.Provider == "easypanel" && req.VerifiedHostID != serviceID) {
			return fmt.Errorf("核实的主机标识与绑定不一致，禁止覆盖")
		}
		if state.CheckpointHost != "" && state.CheckpointHost != strconv.FormatInt(req.VerifiedHostID, 10) {
			return fmt.Errorf("主机检查点不一致，禁止覆盖")
		}
		var conflict bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM services WHERE id<>$1 AND server_id=$2 AND upstream_host_id=$3)`, serviceID, sid, req.VerifiedHostID).Scan(&conflict); err != nil {
			return err
		}
		if conflict {
			return fmt.Errorf("该上游主机已绑定其他服务")
		}
	}
	if err := repo.SupersedeFulfillmentHistoryTx(ctx, tx, state.JobID, state.retryHistory); err != nil {
		return err
	}
	next := "succeeded"
	if req.Decision == "confirmed_not_executed" {
		next = "queued"
	}
	res, err := tx.ExecContext(ctx, `UPDATE fulfillment_jobs SET recovery_required=false,status=$4,lease_until=NULL,last_error='',next_attempt_at=now(),updated_at=now()
 WHERE id=$1 AND service_id=$2 AND claim_version=$3 AND recovery_required AND status='manual_review'`, req.JobID, serviceID, req.ExpectedVersion, next)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrRecoveryConflict
	}
	if req.Decision == "confirmed_completed" {
		switch state.Kind {
		case "provision":
			_, err = tx.ExecContext(ctx, `UPDATE services SET upstream_host_id=$2,status=1,provision_error='',desired_status=NULL,transition_state='',
   provision_data=jsonb_set(coalesce(provision_data,'{}'::jsonb),ARRAY[$3]::text[],to_jsonb($4::text),true) WHERE id=$1`,
				serviceID, req.VerifiedHostID, server.CheckpointUpstreamHostIDs, strconv.FormatInt(req.VerifiedHostID, 10))
		case "renew":
			_, err = tx.ExecContext(ctx, `UPDATE services SET provision_data=(coalesce(provision_data,'{}')-$2-$3)||jsonb_build_object($4::text,$5::text),transition_state='',provision_error='' WHERE id=$1`, serviceID, server.CheckpointRenewInvoice, renewPriceOkKey(state.OrderID), renewDoneCkKey(state.OrderID), time.Now().UTC().Format(time.RFC3339))
		case "upgrade":
			err = localUpgradeApply(ctx, tx, serviceID, state.TargetProductID, state.Cycle, state.snapshot)
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE services SET provision_data=(coalesce(provision_data,'{}')-$2)||jsonb_build_object($3::text,'done'::text) WHERE id=$1`, serviceID, server.CheckpointUpgradeInvoicePrefix+strconv.FormatInt(state.OrderID, 10), upgradeDoneCkKey(state.OrderID))
			}
		}
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE services SET provision_error='' WHERE id=$1`, serviceID)
	}
	if err != nil {
		return err
	}
	detail, err := json.Marshal(struct {
		Request      RecoveryConfirmation `json:"confirmation"`
		Evidence     RecoverySummary      `json:"local_evidence"`
		Verification string               `json:"verification"`
	}{req, state.RecoverySummary, "管理员人工举证，未自动调用上游验证或写入"})
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_logs(admin_id,action,target_type,target_id,detail,ip) VALUES($1,'fulfillment_recovery','service',$2,$3,$4)`, adminID, serviceID, string(detail), ip); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if next == "queued" {
		p.triggerFulfillment()
	}
	return nil
}
