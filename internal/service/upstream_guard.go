package service

import (
	"context"
	"errors"
	"log"
	"strconv"
	"sync"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/server"
)

// orderUpstreamTimeout 下单前实时校验上游的超时。上游客户端本身 30s
// （zjmf/transport.go），下单路径必须更短，避免用户长时间等待。
const orderUpstreamTimeout = 8 * time.Second

var (
	// ErrUpstreamPriceChanged 上游价格/配置项已更新，本地已同步，需用户重新确认下单/升级。
	ErrUpstreamPriceChanged = errors.New("商品价格已更新，请重新确认")
	// ErrUpstreamUnshelved 上游可售目录中已无该商品。
	ErrUpstreamUnshelved = errors.New("商品已下架")
	// ErrUpstreamUnavailable 上游暂时不可用，无法校验最新价，拒绝下单（防亏优先）。
	ErrUpstreamUnavailable = errors.New("暂时无法下单，请稍后再试")
)

// UpstreamGuard 下单前的上游实时价格校验与单商品同步。
// 定时同步每 6 小时一轮，上游改价到下一轮之间存在空窗；本校验在该空窗内挡住亏损订单。
type UpstreamGuard struct {
	Servers   *repo.Servers
	Products  *repo.Products
	Providers *server.Registry
}

// VerifyBeforeOrder 校验本地售价是否落后于上游成本；命中则先同步该商品再拒绝本次下单。
// 上游不可用一律按 ErrUpstreamUnavailable 拒绝。未绑定上游的产品直接放行。
// 必须在订单事务之外调用——事务内不做网络请求。
func (g *UpstreamGuard) VerifyBeforeOrder(ctx context.Context, productID int64, cycle string, selection map[string]string) error {
	if g == nil || g.Servers == nil || g.Products == nil || g.Providers == nil {
		return nil // 未装配上游能力（精简部署/测试），不做校验
	}
	p, err := g.Products.Get(ctx, productID)
	if err != nil {
		return nil // 读不到产品：交由原流程报「商品已下架」
	}
	if !p.ServerID.Valid || p.UpstreamPID <= 0 {
		return nil // 未绑定上游，没有上游成本可比
	}
	sv, err := g.Servers.Get(ctx, p.ServerID.Int64)
	if err != nil {
		log.Printf("[guard] 产品 %d 读取服务器 %d 失败: %v", p.ID, p.ServerID.Int64, err)
		return ErrUpstreamUnavailable
	}
	prov, err := g.Providers.Get(sv.Provider)
	if err != nil {
		log.Printf("[guard] 产品 %d 供应商 %s 不可用: %v", p.ID, sv.Provider, err)
		return ErrUpstreamUnavailable
	}
	// 能力缺失（供应商不支持单商品快照/目录拉取）时无法实时比价，放行并留痕。
	snapFetcher, ok := prov.(server.ProductSnapshotFetcher)
	if !ok {
		log.Printf("[guard] 产品 %d 供应商 %s 未实现单商品快照，跳过实时价格校验", p.ID, sv.Provider)
		return nil
	}
	lister, ok := prov.(server.CatalogLister)
	if !ok {
		log.Printf("[guard] 产品 %d 供应商 %s 未实现目录拉取，跳过实时价格校验", p.ID, sv.Provider)
		return nil
	}
	cfg := server.Config{APIURL: sv.APIURL, APIUsername: sv.APIUsername, APIKey: sv.APIKey}

	// 上游请求带独立超时；后续本地写库仍用原 ctx，避免被上游超时拖累。
	upCtx, cancel := context.WithTimeout(ctx, orderUpstreamTimeout)
	defer cancel()

	// 并发拉取：单商品快照（基础价 + 配置项）与可售目录（判上下架）。
	var (
		snap       server.ProductSnapshot
		snapErr    error
		inCatalog  bool
		catalogErr error
	)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		snap, snapErr = snapFetcher.FetchProductSnapshot(upCtx, cfg, p.UpstreamPID)
	}()
	go func() {
		defer wg.Done()
		list, lerr := lister.CatalogLight(upCtx, cfg)
		if lerr != nil {
			catalogErr = lerr
			return
		}
		for _, up := range list {
			if int64(up.PID) == p.UpstreamPID {
				inCatalog = true
				return
			}
		}
	}()
	wg.Wait()

	if snapErr != nil {
		log.Printf("[guard] 产品 %d 拉取上游快照失败: %v", p.ID, snapErr)
		return ErrUpstreamUnavailable
	}
	if catalogErr != nil {
		log.Printf("[guard] 产品 %d 拉取上游目录失败: %v", p.ID, catalogErr)
		return ErrUpstreamUnavailable
	}

	// 上游已不在可售目录 → 同步下架并拒绝。
	if !inCatalog {
		log.Printf("[guard] 产品 %d 上游 pid=%d 已不在可售目录，标记下架", p.ID, p.UpstreamPID)
		g.setOffline(ctx, p.ID)
		return ErrUpstreamUnshelved
	}
	// 上游仍在售而本地被标记过下架（上一轮误判或上游已恢复）→ 恢复在售，避免用户被卡住。
	if p.UpstreamOfflineReason != "" {
		log.Printf("[guard] 产品 %d 上游 pid=%d 仍在售，恢复本地在售", p.ID, p.UpstreamPID)
		g.setOnline(ctx, p.ID)
	}

	// 本地成本 → 本地售价（口径与 CreateOrder 完全一致）
	psID, err := g.Products.DefaultPricesetID(ctx)
	if err != nil {
		log.Printf("[guard] 产品 %d 读取默认价格组失败: %v", p.ID, err)
		return ErrUpstreamUnavailable
	}
	pr, err := g.Products.Price(ctx, p.ID, psID)
	if err != nil {
		return nil // 无本地价：交由原流程报「该商品未配置此价格」
	}
	localBase, err := strconv.ParseFloat(cyclePrice(pr, cycle), 64)
	if err != nil {
		return nil // 价格不可解析：交由原流程报「商品价格无效」
	}
	localOpts, err := g.Products.GetConfigOptions(ctx, p.ID)
	if err != nil {
		return nil // 配置损坏：交由原流程报「商品配置损坏，请联系管理员」
	}
	localQuote, qerr := CalculateQuote(localOpts, localBase, cycle, selection)
	if qerr != nil {
		return qerr // 与原流程同一错误（「请选择 X」「X: 无效选项」等）
	}
	// 产品未设置利润时回退服务器默认，与 CreateOrder 一致。
	pt, pv := p.ProfitType, p.ProfitValue
	if pv <= 0 {
		pt, pv = sv.ProfitType, sv.ProfitValue
	}
	// 首购口径：周期费 + 初装费（上游对部分配置档位收一次性初装费，漏掉会比价失真）。
	localSell := mathRound(applyProfit(mathRound(localQuote.PayableOnce()), pt, pv))

	// 上游成本：同样按用户实选档位计价，保证两边口径一致。
	upBase := cycleAmount(snap.Monthly, snap.Quarterly, snap.Yearly, cycle)
	upQuote, uerr := CalculateQuote(snap.ConfigOptions, upBase, cycle, selection)
	if uerr != nil {
		// 上游配置项与本地不一致（选项值改名/新增必选项）→ 同步后让用户重选。
		log.Printf("[guard] 产品 %d 上游配置项与本地不一致: %v", p.ID, uerr)
		g.syncFromSnapshot(ctx, p, snap)
		return ErrUpstreamPriceChanged
	}
	if upCost := upQuote.PayableOnce(); localSell < upCost {
		log.Printf("[guard] 产品 %d 本地售价 %.2f 低于上游成本 %.2f（含初装费 ¥%.2f），已同步上游并拒绝本次下单",
			p.ID, localSell, upCost, upQuote.Setup)
		g.syncFromSnapshot(ctx, p, snap)
		return ErrUpstreamPriceChanged
	}
	return nil
}

// syncFromSnapshot 用上游快照覆盖本地基础价与配置项。
// 库存不覆盖（传当前值）：上游未返回库存时留零值会被误当成「售罄」，库存由定时同步兜底。
func (g *UpstreamGuard) syncFromSnapshot(ctx context.Context, p *repo.Product, snap server.ProductSnapshot) {
	if err := g.Products.UpdatePriceAndStockSkippingZero(ctx, p.ID, snap.Monthly, snap.Quarterly, snap.Yearly, p.Stock); err != nil {
		log.Printf("[guard] 产品 %d 同步上游价格失败: %v", p.ID, err)
	}
	if len(snap.ConfigOptions) > 0 {
		if err := g.Products.SaveConfigOptions(ctx, p.ID, snap.ConfigOptions); err != nil {
			log.Printf("[guard] 产品 %d 同步上游配置项失败: %v", p.ID, err)
		}
	}
}

func (g *UpstreamGuard) setOffline(ctx context.Context, id int64) {
	if _, err := g.Products.SetUpstreamOfflineReason(ctx, []int64{id}, repo.UpstreamOfflineUnshelved); err != nil {
		log.Printf("[guard] 产品 %d 标记上游下架失败: %v", id, err)
	}
}

func (g *UpstreamGuard) setOnline(ctx context.Context, id int64) {
	if _, err := g.Products.SetUpstreamOfflineReason(ctx, []int64{id}, ""); err != nil {
		log.Printf("[guard] 产品 %d 恢复上游在售失败: %v", id, err)
	}
}

// cyclePrice 取本地价格行中指定周期的价格。
func cyclePrice(r *repo.PriceRow, cycle string) string {
	switch cycle {
	case "quarterly":
		return r.Quarterly
	case "yearly":
		return r.Yearly
	default:
		return r.Monthly
	}
}

// cycleAmount 取三周期金额中指定周期的金额。
func cycleAmount(monthly, quarterly, yearly float64, cycle string) float64 {
	switch cycle {
	case "quarterly":
		return quarterly
	case "yearly":
		return yearly
	default:
		return monthly
	}
}
