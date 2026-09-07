package zjmf

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/server"
)

// upgTolerance 上游账单与本地差价的容差（人民币分）。
// ponytail: 一期按人民币分固定 0.02；多币种二期再按汇率/比例。
const upgTolerance = 0.02

// Upgrade 执行上游升降级（host 维度换商品）。
// 魔方财务时序（home API，JWT 与现有登录同源）：
//  1. POST /upgrade/upgrade_product_post {hid,pid,billingcycle} —— 缓存待升级选择（幂等覆盖）
//  2. POST /upgrade/checkout_upgrade_product {hid} —— 结算：
//     - 降级（金额≤0）：上游自动退差额到其余额并立即生效，返回 status=1001（无待付账单）→ 直接成功
//     - 升级（金额>0）：返回 data.invoiceid + data.orderid，付款后生效
//  3. 升级场景：经 /get_invoices_detail 取上游账单金额，与本地 DiffAmount 做容差校验；
//     一致则 apply_credit 余额支付账单使升级生效；超差返回 ManualReviewError 转人工，不自动扣。
func (p Provider) Upgrade(ctx context.Context, cfg server.Config, upstreamHostID int64, req server.UpgradeRequest) error {
	cycle, ok := cycleMap[req.Cycle]
	if !ok {
		return fmt.Errorf("不支持的计费周期: %s", req.Cycle)
	}
	if req.TargetPID <= 0 {
		return fmt.Errorf("升级目标商品无效")
	}
	hid := strconv.FormatInt(upstreamHostID, 10)
	// 步骤1: 提交升级选择（幂等覆盖上游缓存，可安全重试）
	if err := postForm(ctx, cfg, "/upgrade/upgrade_product_post", url.Values{
		"hid": {hid}, "pid": {strconv.FormatInt(req.TargetPID, 10)},
		"billingcycle": {cycle}, "currencyid": {"1"},
	}, &map[string]any{}); err != nil {
		return fmt.Errorf("上游提交升级选择失败: %w", err)
	}
	// 步骤2: 结算生成升级账单
	var out struct {
		Data struct {
			InvoiceID flexString `json:"invoiceid"`
		} `json:"data"`
	}
	if err := postForm(ctx, cfg, "/upgrade/checkout_upgrade_product", url.Values{"hid": {hid}}, &out); err != nil {
		return fmt.Errorf("上游升级结算失败: %w", err)
	}
	invoiceID := string(out.Data.InvoiceID)
	if invoiceID == "" {
		// 降级路径：上游已自动退差并立即生效，无待付账单 → 视为成功。
		return nil
	}
	// 步骤3: 读上游账单金额，与本地差价核对（防计价口径不一致导致多扣/少收）
	var inv struct {
		Data struct {
			Detail struct {
				Total flexString `json:"total"`
			} `json:"detail"`
		} `json:"data"`
	}
	if err := getJSON(ctx, cfg, "/get_invoices_detail?id="+invoiceID, &inv); err != nil {
		return fmt.Errorf("读取上游升级账单失败: %w", err)
	}
	upAmount, err := strconv.ParseFloat(strings.TrimSpace(string(inv.Data.Detail.Total)), 64)
	if err != nil {
		return fmt.Errorf("上游升级账单金额无效: %q", inv.Data.Detail.Total)
	}
	if math.Abs(upAmount-req.DiffAmount) > upgTolerance {
		return &server.ManualReviewError{
			Msg:                fmt.Sprintf("上游升级账单金额 %.2f 与本地差价 %.2f 不一致，请人工核对后处理", upAmount, req.DiffAmount),
			UpstreamInvoiceID: invoiceID,
			UpstreamHostID:    upstreamHostID,
		}
	}
	// 步骤4: 用上游余额支付账单（与续费 apply_credit 一致），支付后升级生效
	if err := postForm(ctx, cfg, "/apply_credit", url.Values{
		"invoiceid": {invoiceID}, "use_credit": {"1"}, "enough": {"1"},
	}, &map[string]any{}); err != nil {
		return fmt.Errorf("上游升级账单支付失败（请检查上游余额）: %w", err)
	}
	return nil
}