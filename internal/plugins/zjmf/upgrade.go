package zjmf

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/url"
	"strconv"

	"lumeidc/internal/server"
)

// upgTolerance 上游账单与本地差价的容差，与开通/续费前比价共用同一口径。
const upgTolerance = server.PriceTolerance

// ckUpgradeInvoice 升级账单检查点键：同一服务可能多次升级，必须带订单号区分。
func ckUpgradeInvoice(orderID int64) string {
	return server.CheckpointUpgradeInvoicePrefix + strconv.FormatInt(orderID, 10)
}

// Upgrade 执行上游升降级（host 维度换商品）。
// 魔方财务时序（home API，JWT 与现有登录同源）：
//  1. POST /upgrade/upgrade_product_post {hid,pid,billingcycle} —— 缓存待升级选择（幂等覆盖）
//  2. POST /upgrade/checkout_upgrade_product {hid} —— 结算：
//     - 降级（金额≤0）：上游自动退差额到其余额并立即生效，返回 status=1001（无待付账单）→ 直接成功
//     - 升级（金额>0）：返回 data.invoiceid + data.orderid，付款后生效
//  3. 升级场景：经 /get_invoices_detail 取上游账单金额，与本地 DiffAmount 做容差校验；
//     超差说明上游已改价，返回 PriceChangedError 停在未付款，交管理员二选一。
//  4. apply_credit 余额支付该账单使升级生效；失败按"能否自愈"分流（账单被删转人工 / 余额不足保持重试）。
//
// 可重入：结算出的账单写入 checkpoint（键 upgrade_invoice_<订单号>），重试复用同一张账单，
// 既不重复结算也不重复充值；且账单来自 checkpoint 时跳过比价——管理员确认后重试即"按此价强制开通"。
func (p Provider) Upgrade(ctx context.Context, cfg server.Config, upstreamHostID int64, req server.UpgradeRequest, ck server.CheckpointStore) error {
	cycle, ok := cycleMap[req.Cycle]
	if !ok {
		return fmt.Errorf("不支持的计费周期: %s", req.Cycle)
	}
	if req.TargetPID <= 0 {
		return fmt.Errorf("升级目标商品无效")
	}
	hid := strconv.FormatInt(upstreamHostID, 10)

	// 步骤0: 复用已结算的升级账单，避免重试时重复结算
	ckKey := ckUpgradeInvoice(req.OrderID)
	var invoiceID string
	if ck != nil {
		v, ok, err := ck.GetCheckpoint(ckKey)
		if err != nil {
			return fmt.Errorf("读取升级账单检查点失败: %w", err)
		}
		if ok {
			invoiceID = v
		}
	}
	// 比价只做一次：本次新结算出的账单才核对金额；账单来自检查点时说明管理员已确认，直接付款。
	if invoiceID == "" {
		// 步骤1: 提交升级选择（幂等覆盖上游缓存，可安全重试）
		if err := postForm(ctx, cfg, "/upgrade/upgrade_product_post", url.Values{
			"hid": {hid}, "pid": {strconv.FormatInt(req.TargetPID, 10)},
			"billingcycle": {cycle}, "currencyid": {"1"},
		}, &map[string]any{}); err != nil {
			return fmt.Errorf("提交升级选择失败: %w", err)
		}
		// 步骤2: 结算生成升级账单
		var out struct {
			Data struct {
				InvoiceID flexString `json:"invoiceid"`
			} `json:"data"`
		}
		if err := postForm(ctx, cfg, "/upgrade/checkout_upgrade_product", url.Values{"hid": {hid}}, &out); err != nil {
			return fmt.Errorf("升级结算失败: %w", err)
		}
		invoiceID = string(out.Data.InvoiceID)
		if !validInvoiceID(invoiceID) {
			// 降级 / 0 元升级：上游已自动退差并立即生效，无待付账单 → 视为成功。
			return nil
		}
		if ck != nil {
			if err := ck.SetCheckpoint(ckKey, invoiceID); err != nil {
				// 账单已结算但检查点未落库：重试会再结算一张，交人工而不是硬重试。
				return &server.ManualReviewError{
					Msg:               fmt.Sprintf("保存升级账单检查点失败（已结算账单 invoice=%s）: %v", invoiceID, err),
					UpstreamInvoiceID: invoiceID,
					UpstreamHostID:    upstreamHostID,
				}
			}
		}
		// 步骤3: 读上游账单金额，与本地差价核对（防计价口径不一致导致多扣/少收）
		upAmount, err := fetchUpstreamInvoiceAmount(ctx, cfg, invoiceID)
		if err != nil {
			return &server.ManualReviewError{
				Msg:               fmt.Sprintf("读取升级账单 %s 金额失败，无法核对价格: %v", invoiceID, err),
				UpstreamInvoiceID: invoiceID,
				UpstreamHostID:    upstreamHostID,
			}
		}
		if math.Abs(upAmount-req.DiffAmount) > upgTolerance {
			// 上游已改价：停在未付款（账单不付不扣钱），交管理员二选一：
			// 再次重试=按此账单强制开通，或退款给用户取消升级。
			return &server.PriceChangedError{
				UpstreamAmount:    upAmount,
				ExpectAmount:      req.DiffAmount,
				UpstreamInvoiceID: invoiceID,
				UpstreamPID:       req.TargetPID,
			}
		}
	}
	// 步骤4: 用上游余额支付账单（与续费 apply_credit 一致），支付后升级生效
	if err := postForm(ctx, cfg, "/apply_credit", url.Values{
		"invoiceid": {invoiceID}, "use_credit": {"1"}, "enough": {"1"},
	}, &map[string]any{}); err != nil {
		// 账单已失效（被删除/作废）→ 重试永远付不掉：清掉检查点让重试时重新结算，并转人工。
		if unusable, perr := upstreamInvoiceUnusable(ctx, cfg, invoiceID); perr != nil {
			return &server.ManualReviewError{Msg: "上游升级账单已付或状态不明确，保留检查点，请核对", UpstreamInvoiceID: invoiceID}
		} else if unusable {
			if ck != nil {
				if derr := ck.DeleteCheckpoint(ckKey); derr != nil {
					log.Printf("[zjmf] 清除失效升级账单检查点失败（invoice %s）: %v", invoiceID, derr)
				}
			}
			return &server.ManualReviewError{
				Msg: fmt.Sprintf("上游升级账单 %s 已失效（不存在或已作废），已清除其检查点，"+
					"请确认上游无残留订单后重试: %v", invoiceID, err),
				UpstreamInvoiceID: invoiceID,
				UpstreamHostID:    upstreamHostID,
			}
		}
		// 账单还在（多为上游余额不足）：保持重试、不消耗重试次数，充值到账后自动付掉。
		return &server.RetryLaterError{
			Msg: fmt.Sprintf("上游升级账单 %s 支付失败（请检查上游余额）: %v", invoiceID, err),
		}
	}
	// 升级成功：清掉账单检查点，避免同一订单再次重试时误复用这张已付账单。
	if ck != nil {
		if err := ck.DeleteCheckpoint(ckKey); err != nil {
			log.Printf("[zjmf] 清除升级账单检查点失败（host %d，invoice %s）: %v", upstreamHostID, invoiceID, err)
		}
	}
	return nil
}
