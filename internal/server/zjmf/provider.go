package zjmf

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"lumeidc/internal/server"
)

type Provider struct{}

func (Provider) Code() string { return "zjmf" }
func (Provider) Name() string { return "智简魔方财务（ZJMF）" }

// UpgradeTargets 实现 server.UpgradeTargetProvider：host 维度探测上游是否支持升降级并返回可升级目标。
// 判定顺序（对齐魔方财务官方口径）：
//  1. /host/header 的 host_data.allow_upgrade_product 开关（1=允许"升级产品"）——关闭即不可升级，
//     不再调用目标接口（省一次请求，也不依赖 400 报错判定）；
//  2. 开关开启后调 GET /upgrade/upgrade_product/{hid} 取可升级产品列表（data.host[]，配置在
//     product_upgrade_products 表）；成功但列表为空 = 无目标。
//
// 两者均视为"不支持/无目标"，返回空让调用方隐藏升降级入口（见 Console.UpgradeTargets 语义）。
// best-effort：网络/解析失败同样返回空并记日志，不阻断详情页。
func (p Provider) UpgradeTargets(ctx context.Context, cfg server.Config, upstreamHostID int64) ([]server.UpgradeTarget, error) {
	data, err := fetchHostHeaderRaw(ctx, cfg, upstreamHostID)
	if err != nil {
		log.Printf("[zjmf] host %d 升降级开关查询失败（按不可升级处理）: %v", upstreamHostID, err)
		return nil, nil
	}
	// 老版本缺失该开关时不拦截（返回 true），交由目标接口判定。
	if !allowUpgradeProduct(data) {
		return nil, nil
	}
	path := "/upgrade/upgrade_product/" + strconv.FormatInt(upstreamHostID, 10)
	var out struct {
		Data struct {
			Host []struct {
				PID  int64  `json:"pid"`
				Name string `json:"name"`
			} `json:"host"`
		} `json:"data"`
	}
	if err := getJSON(ctx, cfg, path, &out); err != nil {
		// 上游不可升级（400）/接口异常：统一按不可升级处理；不向上抛错，避免上层误走回退。
		log.Printf("[zjmf] host %d 升降级目标查询失败（按不可升级处理）: %v", upstreamHostID, err)
		return nil, nil
	}
	var targets []server.UpgradeTarget
	for _, hp := range out.Data.Host {
		if hp.PID <= 0 {
			continue
		}
		targets = append(targets, server.UpgradeTarget{UpstreamPID: hp.PID, Name: strings.TrimSpace(hp.Name)})
	}
	return targets, nil
}

// allowUpgradeProduct 解析 /host/header 的 host_data.allow_upgrade_product 开关（数字或字符串）。
// 缺失/不可解析视为 true（老版本无此开关，不拦截，交由目标接口判定）。
func allowUpgradeProduct(data map[string]json.RawMessage) bool {
	hd, ok := data["host_data"]
	if !ok {
		return true
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(hd, &m) != nil {
		return true
	}
	raw, ok := m["allow_upgrade_product"]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return true // 缺失/未定义：不拦截，交由目标接口判定
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	switch v := rawNum(raw).(type) {
	case float64:
		return v == 1
	case string:
		switch strings.TrimSpace(v) {
		case "1", "true", "yes":
			return true
		}
		return false
	}
	return true
}

// TestConnection 登录并拉用户资料验证凭据。旧版上游无 /v1/user（404），
// 回退用 /cart/all 验证——登录成功且目录接口可用即视为连通。
func (p Provider) TestConnection(ctx context.Context, cfg server.Config) error {
	_, err := p.FetchBalance(ctx, cfg)
	return err
}

// FetchBalance 拉取上游账户余额。
// 优先魔方财务 home /user_info → user.credit；回退魔方财务 openapi /v1/user → data.client.credit；
// 再回退 CBAP/WHMCS 的 data.credit / data.balance。credit 可能是字符串或数字，用 flexString 兼容。
func (p Provider) FetchBalance(ctx context.Context, cfg server.Config) (string, error) {
	var info struct {
		User struct {
			Credit flexString `json:"credit"`
		} `json:"user"`
	}
	if err := getJSON(ctx, cfg, "/user_info", &info); err == nil && info.User.Credit != "" {
		return string(info.User.Credit), nil
	}
	var out struct {
		Data struct {
			Client struct {
				Credit flexString `json:"credit"`
			} `json:"client"`
			Credit  flexString `json:"credit"`
			Balance flexString `json:"balance"`
		} `json:"data"`
	}
	if err := getJSON(ctx, cfg, "/v1/user", &out); err != nil {
		if strings.Contains(err.Error(), "404") {
			// 旧版上游不支持，用 /cart/all 验证连通性
			var dummy map[string]any
			if err2 := getJSON(ctx, cfg, "/cart/all", &dummy); err2 != nil {
				return "", err2
			}
			return "", nil
		}
		return "", err
	}
	for _, v := range []flexString{out.Data.Client.Credit, out.Data.Credit, out.Data.Balance} {
		if v != "" {
			return string(v), nil
		}
	}
	return "", nil
}

// ---------- 商品目录 ----------

// prodConfigResp 兼容两种真实 ZJMF 响应形态：
// 1) data.product_pricings[] 数组（每元素一个周期：billingcycle + monthly/quarterly/annually 价格键）
// 2) data.pricing 对象（直接含 monthly 等键，部分老版本）
type productDetail struct {
	StockControl int     `json:"stock_control"`
	Stock        jsonNum `json:"stock"`
	Qty          jsonNum `json:"qty"` // 旧版魔方库存字段（无 stock 键时用 qty）
	Price        jsonNum `json:"price"`
	ProductPrice jsonNum `json:"product_price"`
	Description  string  `json:"description"`
}

type prodConfigResp struct {
	Data struct {
		Product         productDetail      `json:"product"`
		Products        productDetail      `json:"products"`         // 部分响应商品对象键为 products
		Pricing         map[string]jsonNum `json:"pricing"`          // 老版本
		ProductPricings []pricingEntry     `json:"product_pricings"` // 新版
		ConfigGroups    []cfgGroupField    `json:"config_groups"`    // 配置档（弹性云等按配置计价的产品）
		BasePrice       jsonNum            `json:"base_price"`       // 魔方财务 get_product_config 的“基础价格”（产品名称行）
		Price           jsonNum            `json:"price"`
	} `json:"data"`
}

// cfgGroupField 配置档组的极简结构，仅供兜底取价，复用 pricingEntry 解析价格。
type cfgGroupField struct {
	Options []cfgOptionField `json:"options"`
}

// stockOf stock_control=1 时取库存：新版用 stock 键，旧版魔方无 stock、用 qty。
func (d productDetail) stockOf() int {
	if d.StockControl != 1 {
		return -1
	}
	if d.Stock > 0 {
		return int(d.Stock)
	}
	if d.Qty > 0 {
		return int(d.Qty)
	}
	return 0
}

// FetchProductMeta 拉取上游商品描述与库存（get_product_config 的 product.description / stock）。
func (p Provider) FetchProductMeta(ctx context.Context, cfg server.Config, upstreamPID int64) (string, int, error) {
	_, pc, err := fetchProductConfigRaw(ctx, cfg, int(upstreamPID))
	if err != nil {
		return "", 0, err
	}
	desc := strings.TrimSpace(pc.Data.Product.Description)
	if desc == "" {
		desc = strings.TrimSpace(pc.Data.Products.Description)
	}
	stock := pc.Data.Product.stockOf()
	if stock == -1 {
		stock = pc.Data.Products.stockOf()
	}
	return desc, stock, nil
}

type cfgOptionField struct {
	Hidden     jsonNum       `json:"hidden"`
	OptionType int           `json:"option_type"`
	QtyMin     float64       `json:"qty_minimum"`
	Subs       []cfgSubField `json:"sub"`
}

type cfgSubField struct {
	Pricings []pricingEntry `json:"pricings"`
}

func subMonthly(s cfgSubField) float64 {
	if len(s.Pricings) == 0 {
		return 0
	}
	return float64(s.Pricings[0].Monthly)
}

// minConfigMonthly 按“每档最低可配置月价求和”得出基础价兜底（列表展示用）。
// 排除 hidden 与 OS 档（option_type 5，同 CalculateQuote 口径）。
// 数量档(range)：qty_minimum≤0 表示可配 0（免费）→ 记 0；否则取最低一档单价×最少档数(=该档价)。
// 选单档：取最低子档月价（含免费的 0）。
// 该价 = 产品“最低可配置”月租，仅作目录展示；购买/续费以购买页服务端重算为准。
// ponytail: 仅取最低一档，未考虑档区间阶梯步进，属展示近似；绝不回流为真实计价。
func minConfigMonthly(groups []cfgGroupField) float64 {
	var total float64
	for _, g := range groups {
		for _, o := range g.Options {
			if o.Hidden != 0 || o.OptionType == 5 { // 5=操作系统，不计价；hidden 上游为数字 0/1
				continue
			}
			if rangeTypes[o.OptionType] { // 数量档：可配 0 则最低 0，否则取最低一档单价
				if o.QtyMin <= 0 {
					continue
				}
				if len(o.Subs) > 0 {
					total += subMonthly(o.Subs[0])
				}
				continue
			}
			best := -1.0
			for _, s := range o.Subs {
				p := subMonthly(s)
				if p < 0 { // -1 表示未开启该周期
					p = 0
				}
				if best < 0 || p < best {
					best = p
				}
			}
			if best > 0 {
				total += best
			}
		}
	}
	return math.Round(total*100) / 100
}

// pricingEntry product_pricings 数组元素。价格键因周期而异：
// monthly 周期用 "monthly"，annually 用 "annually"，以此类推。
// 基础价常放在 product_price，故额外解析；
// type 用于区分基础价条目(type 为空或 "product"）与配置项条目。
type pricingEntry struct {
	Type         string  `json:"type"`
	BillingCycle string  `json:"billingcycle"`
	Monthly      jsonNum `json:"monthly"`
	ProductPrice jsonNum `json:"product_price"`
	Price        jsonNum `json:"price"`
	Quarterly    jsonNum `json:"quarterly"`
	SemiAnnually jsonNum `json:"semiannually"`
	Annually     jsonNum `json:"annually"`
}

// jsonNum 兼容上游价格字段为数字或字符串。
type jsonNum float64

func (n *jsonNum) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*n = jsonNum(f)
	return nil
}

// Catalog 拉取目录树，逐产品回填价格与库存（串行；批量并发优化二期再做）。
// ponytail: 每产品一次 get_product_config，目录大时较慢——批量并发优化属二期。
func (p Provider) Catalog(ctx context.Context, cfg server.Config) ([]server.UpstreamProduct, error) {
	// ZJMF /cart/all 结构因版本而异：data 可能是分组数组（组内商品在 "product" 键）
	// 或 {products:[{...products:[...]}]} 嵌套树。宽松遍历统一提取。
	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	if err := getJSON(ctx, cfg, "/cart/all", &raw); err != nil {
		return nil, fmt.Errorf("拉取目录失败: %w", err)
	}
	var out []server.UpstreamProduct
	walkCatalog(raw.Data, "", &out)
	if len(out) == 0 {
		return nil, fmt.Errorf("目录为空或响应结构无法识别")
	}
	// 并发回填价格与库存（每批 8 个）
	fillPricingParallel(ctx, cfg, out)
	return out, nil
}

// CatalogLight 仅列目录（PID/名称/分组），不逐个商品拉取价格/库存/描述，
// 用于快速填充上游商品下拉；详情由选中后的 pullConfig 单独拉取。
func (p Provider) CatalogLight(ctx context.Context, cfg server.Config) ([]server.UpstreamProduct, error) {
	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	if err := getJSON(ctx, cfg, "/cart/all", &raw); err != nil {
		return nil, fmt.Errorf("拉取目录失败: %w", err)
	}
	var out []server.UpstreamProduct
	walkCatalog(raw.Data, "", &out)
	if len(out) == 0 {
		return nil, fmt.Errorf("目录为空或响应结构无法识别")
	}
	return out, nil
}

// fillPricingParallel 并发拉取每个商品的 get_product_config 回填价格/库存。
// 单个失败静默跳过（价格留 0），全部原始响应结构差异记录日志便于排查。
func fillPricingParallel(ctx context.Context, cfg server.Config, out []server.UpstreamProduct) {
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rawBody, pc, err := fetchProductConfigRaw(ctx, cfg, out[idx].PID)
			if err != nil {
				return
			}
			// 顺带解析配置项（同一次响应），供导入/表单直接复用，避免重复请求。
			out[idx].ConfigOptions = parseConfigOptions([]byte(rawBody))
			out[idx].ConfigCount = len(out[idx].ConfigOptions)
			m, q, y := baseMonthly(pc)
			if m == 0 && q == 0 && y == 0 {
				// 按配置计价产品上游基础价就是 0：真实计价以此为准（勿再叠加配置价）。
				// 目录展示需一个兜底价，仅放 DisplayMonthly，绝不回流进制价表 product_prices。
				if fb := minConfigMonthly(pc.Data.ConfigGroups); fb > 0 {
					out[idx].DisplayMonthly = fb
					log.Printf("[zjmf] pid=%d 基础价 0，展示兜底最低配置价=%.2f", out[idx].PID, fb)
				} else {
					// 诊断：价仍为 0，dump 完整响应到临时文件便于核对字段
					if fn, werr := dumpRaw(out[idx].PID, rawBody); werr == nil {
						log.Printf("[zjmf] pid=%d 价格提取为 0，完整响应已写入 %s", out[idx].PID, fn)
					} else {
						log.Printf("[zjmf] pid=%d 价格提取为 0，原始响应片段: %.400s", out[idx].PID, rawBody)
					}
				}
			}
			out[idx].Monthly, out[idx].Quarterly, out[idx].Yearly = m, q, y
			if st := pc.Data.Product.stockOf(); st != -1 {
				out[idx].Stock = st
			} else if st := pc.Data.Products.stockOf(); st != -1 {
				out[idx].Stock = st
			}
			if d := strings.TrimSpace(pc.Data.Product.Description); d != "" {
				out[idx].Description = d
			} else if d := strings.TrimSpace(pc.Data.Products.Description); d != "" {
				out[idx].Description = d
			}
		}(i)
	}
	wg.Wait()
}

// extractPricing 从两种响应形态提取三周期价格。
// 新版 product_pricings：每元素一个周期（billingcycle=monthly/quarterly/annually，
// 价格在该周期对应的键里）；老版 pricing 对象直接 monthly/quarterly/yearly。
func extractPricing(entries []pricingEntry, legacy map[string]jsonNum) (m, q, y float64) {
	for _, e := range entries {
		// 仅取基础价条目：type 为空或 "product"。配置项计价在 config_groups，勿重复计。
		if e.Type != "" && e.Type != "product" {
			continue
		}
		// 基础价可能在 product_price，部分版本在 price；monthly 缺失时兜底。
		mVal, qVal, yVal := float64(e.Monthly), float64(e.Quarterly), float64(e.Annually)
		if mVal == 0 {
			mVal = float64(e.ProductPrice)
		}
		if mVal == 0 {
			mVal = float64(e.Price)
		}
		if qVal == 0 {
			qVal = float64(e.ProductPrice)
		}
		if qVal == 0 {
			qVal = float64(e.Price)
		}
		if yVal == 0 {
			yVal = float64(e.ProductPrice)
		}
		if yVal == 0 {
			yVal = float64(e.Price)
		}
		// -1.00 表示上游未开启该周期，按 0 处理
		if mVal < 0 {
			mVal = 0
		}
		if qVal < 0 {
			qVal = 0
		}
		if yVal < 0 {
			yVal = 0
		}
		switch strings.ToLower(e.BillingCycle) {
		case "quarterly":
			q = qVal
		case "annually", "yearly":
			y = yVal
		default:
			// 真实 ZJMF：单条记录含全部周期键、无 billingcycle；monthly 条目或默认取全部字段
			if m == 0 && mVal > 0 {
				m = mVal
			}
			if q == 0 && qVal > 0 {
				q = qVal
			}
			if y == 0 && yVal > 0 {
				y = yVal
			}
		}
	}
	if m == 0 && q == 0 && y == 0 {
		m = float64(legacy["monthly"])
		q = float64(legacy["quarterly"])
		y = float64(legacy["annually"])
		if y == 0 {
			y = float64(legacy["yearly"])
		}
	}
	return
}

// baseMonthly 从 get_product_config 响应提取商品基础三周期价。
// 顺序：product_pricings → 商品对象 price/product_price → 魔方财务 data.base_price。
// 注意：绝不可用 data.price（那是基础价+默认配置的总价，非基础价）。
func baseMonthly(pc prodConfigResp) (m, q, y float64) {
	m, q, y = extractPricing(pc.Data.ProductPricings, pc.Data.Pricing)
	if m == 0 && q == 0 && y == 0 {
		pd := pc.Data.Product
		if pd == (productDetail{}) {
			pd = pc.Data.Products
		}
		if bp := float64(pd.Price); bp > 0 {
			m = bp
		} else if bp := float64(pd.ProductPrice); bp > 0 {
			m = bp
		} else if bp := float64(pc.Data.BasePrice); bp > 0 {
			m = bp
		}
	}
	return
}

// FetchProductPrice 供后台“拉取配置项”同步刷新基础价：拉取单商品配置并提取基础三周期价。
func (p Provider) FetchProductPrice(ctx context.Context, cfg server.Config, upstreamPID int64) (m, q, y float64, err error) {
	rawBody, pc, e := fetchProductConfigRaw(ctx, cfg, int(upstreamPID))
	if e != nil {
		return 0, 0, 0, e
	}
	m, q, y = baseMonthly(pc)
	if m == 0 && q == 0 && y == 0 {
		if fn, werr := dumpRaw(int(upstreamPID), rawBody); werr == nil {
			log.Printf("[zjmf] FetchProductPrice pid=%d 基础价 0，完整响应已写入 %s", upstreamPID, fn)
		}
	}
	return m, q, y, nil
}

// FetchProductSnapshot 单商品快照：复用 fetchProductConfigRaw 一次请求拿基础价与配置项。
// 供下单前的实时价格校验使用（口径与 Catalog 回填一致）。
func (p Provider) FetchProductSnapshot(ctx context.Context, cfg server.Config, upstreamPID int64) (server.ProductSnapshot, error) {
	rawBody, pc, err := fetchProductConfigRaw(ctx, cfg, int(upstreamPID))
	if err != nil {
		return server.ProductSnapshot{}, err
	}
	m, q, y := baseMonthly(pc)
	return server.ProductSnapshot{
		Monthly:       m,
		Quarterly:     q,
		Yearly:        y,
		ConfigOptions: parseConfigOptions([]byte(rawBody)),
	}, nil
}

// fetchProductConfigRaw 拉取商品配置并解析，同时返回原始 body 供诊断。
func fetchProductConfigRaw(ctx context.Context, cfg server.Config, pid int) (string, prodConfigResp, error) {
	path := "/cart/get_product_config?pid=" + strconv.Itoa(pid)
	body, err := doJSONRaw(ctx, cfg, http.MethodGet, path, nil, true)
	if err != nil {
		return "", prodConfigResp{}, err
	}
	var pc prodConfigResp
	if err := json.Unmarshal(body, &pc); err != nil {
		return string(body), prodConfigResp{}, fmt.Errorf("商品配置响应解析失败: %w", err)
	}
	return string(body), pc, nil
}

// dumpRaw 将上游完整响应当做诊断文件写入系统临时目录，便于核对价格字段真实位置。
func dumpRaw(pid int, body string) (string, error) {
	dir := os.TempDir()
	fn := filepath.Join(dir, fmt.Sprintf("zjmf_config_%d.json", pid))
	if err := os.WriteFile(fn, []byte(body), 0o600); err != nil {
		return "", err
	}
	return fn, nil
}

// walkCatalog 递归遍历目录节点，收集含 id+name 的商品叶子。
func walkCatalog(node json.RawMessage, groupName string, out *[]server.UpstreamProduct) {
	if len(node) == 0 {
		return
	}
	// 数组：逐元素递归
	var arr []json.RawMessage
	if json.Unmarshal(node, &arr) == nil {
		for _, el := range arr {
			walkCatalog(el, groupName, out)
		}
		return
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(node, &m) != nil {
		return
	}
	// 无 id/name 的容器节点：递归子数组继续找
	if _, ok := m["id"]; !ok {
		for _, key := range []string{"products", "product", "data", "children"} {
			if children, ok := m[key]; ok && isJSONArray(children) {
				walkCatalog(children, groupName, out)
			}
		}
		return
	}
	id, name := nodeIDName(m)
	if id > 0 && name != "" {
		// 有子节点 → 是分组；否则是商品
		if children, ok := m["products"]; ok && isJSONArray(children) {
			gn := name
			if groupName != "" {
				gn = groupName + "/" + name
			}
			walkCatalog(children, gn, out)
			return
		}
		if _, hasSub := m["product"]; !hasSub {
			up := server.UpstreamProduct{
				PID: int(id), Name: name,
				GroupName: groupName, Stock: -1,
			}
			*out = append(*out, up)
			return
		}
	}
	// 兼容旧结构：分组里 "product" 数组
	if children, ok := m["product"]; ok {
		walkCatalog(children, name, out)
	}
}

func isJSONArray(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return strings.HasPrefix(s, "[")
}

func nodeIDName(m map[string]json.RawMessage) (float64, string) {
	var id float64
	var name string
	if v, ok := m["id"]; ok {
		json.Unmarshal(v, &id)
	}
	if v, ok := m["name"]; ok {
		json.Unmarshal(v, &name)
	}
	return id, strings.TrimSpace(name)
}

// ---------- 开通（购物车 7 步编排） ----------

// cycleMap 本地周期 → ZJMF 计费周期。上游用 annually(非 yearly) 表示年付。
var cycleMap = map[string]string{
	"monthly": "monthly", "quarterly": "quarterly", "yearly": "annually",
}

const (
	ckInvoice = server.CheckpointUpstreamInvoiceID // checkpoint：已生成上游账单号
	ckHosts   = server.CheckpointUpstreamHostIDs   // checkpoint：已开通 host id 列表(逗号分隔)
	// ckRenewInvoice 续费账单号。重试时复用它直接付款，避免每次重试都在上游新建续费单；
	// 续费成功后清除，否则下次续费（新订单）会误复用这张已付账单。
	ckRenewInvoice = server.CheckpointRenewInvoice
)

// configOptionMap 把订单选择(field→子项值) 解析成上游 add_to_shop 识别的
// configoption[选项id]=子项id 形式。上游仅认 config_options.id 作数组键、sub.id 作取值，
// 此前按字段名/option_name 下发会被忽略，导致非默认档（如 NAT=10）一直落回首档。
// 单选取子项 id；range/数量型取所选数值。
// 未在 config_groups 命中（字段被移除/值过期）的项跳过，交由上游默认，避免误报错误。
func configOptionMap(pc map[string]any, sel map[string]string) map[string]string {
	out := map[string]string{}
	data, _ := pc["data"].(map[string]any)
	groups, _ := data["config_groups"].([]any)
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		options, _ := gm["options"].([]any)
		for _, o := range options {
			om, _ := o.(map[string]any)
			if om == nil {
				continue
			}
			name := str(om["option_name"])
			field := name
			if i := strings.Index(name, "|"); i >= 0 {
				field = strings.TrimSpace(name[:i])
			}
			val, ok := sel[field]
			if !ok || val == "" {
				continue
			}
			optID := str(om["id"])
			if optID == "" {
				continue
			}
			if rangeTypes[int(num(om["option_type"]))] {
				out[optID] = val // 数量型：option 自身按所选值计费
				continue
			}
			subs, _ := om["sub"].([]any)
			for _, s := range subs {
				sm, _ := s.(map[string]any)
				if sm == nil {
					continue
				}
				_, sval := splitSubName(str(sm["option_name"]))
				if sval != val {
					continue
				}
				if sid := str(sm["id"]); sid != "" {
					out[optID] = sid
				}
				break
			}
		}
	}
	return out
}

func (p Provider) Provision(ctx context.Context, cfg server.Config, req server.ProvisionRequest, ck server.CheckpointStore) (server.ProvisionResult, error) {
	cycle, ok := cycleMap[req.Cycle]
	if !ok {
		return server.ProvisionResult{}, fmt.Errorf("不支持的计费周期: %s", req.Cycle)
	}

	// checkpoint 1: 已结算过 → 直接回查 host id，绝不重复下单
	if v, ok, err := ck.GetCheckpoint(ckHosts); err != nil {
		return server.ProvisionResult{}, fmt.Errorf("读取开通检查点失败: %w", err)
	} else if ok && v != "" {
		idStr := strings.Split(v, ",")[0]
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err == nil && id > 0 {
			return server.ProvisionResult{UpstreamHostID: id}, nil
		}
	}

	// checkpoint 2: 结算过但未支付（如上次支付时上游余额不足）→ 跳过加购/结算，
	// 直接重试支付同一张账单，避免在上游重复创建 Pending host。
	var invoiceID string
	var hostID int64
	var settleBody string
	if v, ok, err := ck.GetCheckpoint(ckInvoice); err != nil {
		return server.ProvisionResult{}, fmt.Errorf("读取账单检查点失败: %w", err)
	} else if ok {
		invoiceID = v
	}

	// 一次调用内最多走两轮：首轮用检查点里的账单（没有就结算一张）；若发现账单已失效
	// （被上游删除/作废），清掉检查点自动重新结算一轮，不劳管理员先"重置检查点"再重试。
	// 只重建一次：账单失效说明上游侧已异常，继续自动重建只会不断堆积 Pending host，转人工更稳。
	for round := 0; ; round++ {
		// builtInvoice 本轮是否新建了上游账单：只有新建的账单才做开通前比价。
		// 账单来自检查点（管理员确认后的重试）时跳过比价，直接付款即"按此价强制开通"。
		builtInvoice := false
		if invoiceID == "" {
			// 步骤1: 清空购物车（幂等保护）。
			// 旧版魔方会把"同人重复开通同商品"检查也放在该接口：status=400
			// "该订单已开通,请勿重新开通"。此错误不代表购物车清理失败——
			// 真正的重复购买会在后续 add_to_shop/settle 报错，这里跳过以放行流程。
			if err := postForm(ctx, cfg, "/cart/clear", url.Values{}, &map[string]any{}); err != nil {
				if !strings.Contains(err.Error(), "该订单已开通") {
					return server.ProvisionResult{}, fmt.Errorf("清空购物车失败: %w", err)
				}
			}
			// 步骤2: 取商品配置（currencyid 等）
			var pc map[string]any
			if err := getJSON(ctx, cfg,
				"/cart/get_product_config?pid="+strconv.FormatInt(req.UpstreamPID, 10), &pc); err != nil {
				return server.ProvisionResult{}, fmt.Errorf("读取商品配置失败: %w", err)
			}
			currencyID := extractCurrency(pc)
			if currencyID == "" {
				// 配置响应未必含顶层 currencyid；缺它 add_to_shop 会报“周期未配置价格”。
				// 本系统单一币种人民币，缺省用 1（与主流 ZJMF 默认币种一致）。
				currencyID = "1"
			}

			// 步骤3: 加购。configoption 结构依赖上游商品定义，一期传空让上游用默认配置
			pw := req.Password
			if !validHostPassword(pw) {
				pw = randomHostPassword() // 空或不合规时生成合规密码（必含大写+小写+数字），避免魔方云模块校验失败
			}
			// 主机名：上游规则 = 商品前缀(如 XAGJB) + 所填 host，总长 ≥10。
			// 调用方未传时用 u{ServiceID}（中性标识，虚拟主机/CDN/服务器均适用），
			// 不足 10 位补随机字母数字。
			hostname := req.Hostname
			if hostname == "" && req.ServiceID > 0 {
				hostname = "u" + strconv.FormatInt(req.ServiceID, 10)
			}
			for len(hostname) < 10 {
				hostname += server.RandomAlnum(3) // 主机名仅允许字母数字，不能混入密码特殊字符
			}
			form := url.Values{
				"pid":          {strconv.FormatInt(req.UpstreamPID, 10)},
				"billingcycle": {cycle},
				"qty":          {"1"},
				"checkout":     {"0"},
				"host":         {hostname},
				"password":     {pw},
			}
			if currencyID != "" {
				form.Set("currencyid", currencyID)
			}
			for k, v := range configOptionMap(pc, req.ConfigOpts) {
				form.Set("configoption["+k+"]", v)
			}
			if err := postForm(ctx, cfg, "/cart/add_to_shop", form, &map[string]any{}); err != nil {
				return server.ProvisionResult{}, fmt.Errorf("加入购物车失败: %w", err)
			}
			// 步骤4: 结算 → 上游账单号（部分版本结算时已返回 hostid，另一些则留待付款后创建）。
			// pos 是购物车位置（0 起）：清空+加购后唯一商品固定在第 0 位；传商品 ID 会被
			// 魔方财务 settle 的位置过滤落空，进而误结算整个购物车。
			settleBody, settle, serr := postFormSettle(ctx, cfg, "/cart/settle",
				url.Values{"pos[0]": {"0"}, "checkout": {"1"}})
			if serr != nil {
				return server.ProvisionResult{}, fmt.Errorf("结算失败: %w", serr)
			}
			invoiceID = string(settle.Data.InvoiceID)
			if !validInvoiceID(invoiceID) {
				log.Printf("[zjmf] pid=%d settle 原始响应: %.500s", req.UpstreamPID, settleBody)
				return server.ProvisionResult{}, fmt.Errorf("结算未返回账单号，响应: %.200s", settleBody)
			}
			if len(settle.Data.HostIDs) > 0 {
				hostID = settle.Data.HostIDs[0]
			}
			builtInvoice = true
			if err := ck.SetCheckpoint(ckInvoice, invoiceID); err != nil {
				// settle 成功但 checkpoint 未落库：标记人工复核，避免重试重复创建账单
				return server.ProvisionResult{}, &server.ManualReviewError{
					Msg:               fmt.Sprintf("保存上游账单检查点失败（settle 已成功，invoice=%s）: %v", invoiceID, err),
					UpstreamInvoiceID: invoiceID,
					UpstreamHostID:    hostID,
				}
			}
			// ckHosts 刻意不在这里写：它兼作"已完成"标记，checkpoint 1 靠它短路返回成功。
			// 此处 host 可能已建但账单未付（auto_setup=order），提前写会让付款失败后的重试
			// 被误判为已完成。统一放到付款成功之后再写。
		} // end 未结算分支（invoiceID 已有值时跳过加购/结算，直接支付）

		// 开通前比价：上游按当前价出的账单若高于下单时的成本额，说明上游已涨价，
		// 继续付款会吃掉利润甚至亏损。停在这里不付款（账单已存在但不付不扣钱），
		// 交后台人工二选一：重试即按此账单强制开通，或退款给用户。
		if builtInvoice && req.ExpectAmount > 0 {
			upAmount, aerr := fetchUpstreamInvoiceAmount(ctx, cfg, invoiceID)
			if aerr != nil {
				return server.ProvisionResult{}, &server.ManualReviewError{
					Msg:               fmt.Sprintf("读取上游账单 %s 金额失败，无法核对价格: %v", invoiceID, aerr),
					UpstreamInvoiceID: invoiceID,
				}
			}
			if upAmount-req.ExpectAmount > upgTolerance {
				return server.ProvisionResult{}, &server.PriceChangedError{
					UpstreamAmount:    upAmount,
					ExpectAmount:      req.ExpectAmount,
					UpstreamInvoiceID: invoiceID,
					UpstreamPID:       req.UpstreamPID,
				}
			}
		}

		// 步骤5: 余额支付账单。upstream_auto_setup=payment 的商品在付款时自动创建 host，
		// 其 id 由 apply_credit 响应 data.hostid[] 返回（结算阶段通常无 hostid）。
		payForm := url.Values{"invoiceid": {invoiceID}, "use_credit": {"1"}, "enough": {"1"}}
		var payResp struct {
			Data struct {
				HostIDs []int64 `json:"hostid"`
			} `json:"data"`
		}
		perr := postForm(ctx, cfg, "/apply_credit", payForm, &payResp)
		if perr != nil {
			// 先看账单是否还能付：失效（被删除/作废）就去重新结算，可付则是上游余额不足。
			unusable, probeErr := upstreamInvoiceUnusable(ctx, cfg, invoiceID)
			if probeErr != nil {
				return server.ProvisionResult{}, &server.ManualReviewError{Msg: "上游账单已支付或状态无法确认，保留检查点，请人工核对", UpstreamInvoiceID: invoiceID}
			} else if unusable {
				if round == 0 {
					// 账单已失效：清掉检查点，本轮自动重新结算（含付款前比价），无需人工重置。
					if derr := ck.DeleteCheckpoint(ckInvoice); derr != nil {
						return server.ProvisionResult{}, fmt.Errorf("清除失效账单检查点失败: %w", derr)
					}
					log.Printf("[zjmf] 上游账单 %s 已失效，自动重新结算后重试付款", invoiceID)
					invoiceID, hostID = "", 0
					continue
				}
				return server.ProvisionResult{}, &server.ManualReviewError{
					Msg: fmt.Sprintf("上游账单 %s 已失效（不存在或已作废），自动重新结算后仍付不掉，"+
						"请人工核对上游是否残留待开通主机: %v", invoiceID, perr),
					UpstreamInvoiceID: invoiceID,
					UpstreamHostID:    hostID,
				}
			}
			// 账单仍待支付（多为上游余额不足）：保持重试、不消耗重试次数，充值到账后自动付掉。
			// host 若在结算阶段已建（auto_setup=order），重试也复用同一张账单重新付款，不会重复下单。
			return server.ProvisionResult{}, &server.RetryLaterError{
				Msg: fmt.Sprintf("上游账单 %s 支付失败（请检查上游余额）: %v", invoiceID, perr),
			}
		}
		if hostID == 0 && len(payResp.Data.HostIDs) > 0 {
			hostID = payResp.Data.HostIDs[0]
		}
		if hostID == 0 {
			log.Printf("[zjmf] pid=%d settle 响应: %.500s", req.UpstreamPID, settleBody)
			return server.ProvisionResult{}, fmt.Errorf("结算与支付均未返回 host id，响应: %.200s", settleBody)
		}
		// 付款成功后才写 ckHosts：它兼作"已完成"标记，checkpoint 1 靠它短路返回成功。
		// 若在结算时就写，付款失败后的重试会误判为已完成，把一张未付的上游账单悄悄甩掉
		// （本地显示已开通、上游欠费停机，且任务标 succeeded 不再重试）。
		if err := ck.SetCheckpoint(ckHosts, strconv.FormatInt(hostID, 10)); err != nil {
			return server.ProvisionResult{}, &server.ManualReviewError{
				Msg:               fmt.Sprintf("保存主机检查点失败（账单 %s 已支付，host=%d）: %v", invoiceID, hostID, err),
				UpstreamInvoiceID: invoiceID,
				UpstreamHostID:    hostID,
			}
		}
		return server.ProvisionResult{UpstreamHostID: hostID}, nil
	}
}

// 密码策略统一走 server 包（生成+校验），与其它上游共用。
func validHostPassword(s string) bool { return server.ValidHostPassword(s) }
func randomHostPassword() string      { return server.RandomHostPassword() }

// validInvoiceID 上游返回的账单号是否可用。
// 上游在"无需付款"时（0 元商品续费、降级自动退差）不生成账单，字段显式为 invoiceid=null，
// flexString 把它解析成字符串 "null"。拿这种值当账单号去支付，上游只会回"未找到支付项目"。
func validInvoiceID(s string) bool {
	switch strings.TrimSpace(s) {
	case "", "null", "0":
		return false
	}
	return true
}

// fetchUpstreamInvoiceAmount 读取上游账单金额（元）：升级前比价与开通前比价共用。
func fetchUpstreamInvoiceAmount(ctx context.Context, cfg server.Config, invoiceID string) (float64, error) {
	var inv struct {
		Data struct {
			Detail struct {
				Total flexString `json:"total"`
			} `json:"detail"`
		} `json:"data"`
	}
	if err := getJSON(ctx, cfg, "/get_invoices_detail?id="+invoiceID, &inv); err != nil {
		return 0, err
	}
	amt, err := strconv.ParseFloat(strings.TrimSpace(string(inv.Data.Detail.Total)), 64)
	if err != nil {
		return 0, fmt.Errorf("账单金额无效: %q", inv.Data.Detail.Total)
	}
	return amt, nil
}

// upstreamInvoiceUnusable 判断上游账单是否已不可支付：不存在（上游业务错误）或状态不是待支付。
// 上游后台"删除账单"实际是置为 Cancelled，记录仍可查询——只看"是否存在"会把已作废的账单
// 当成可付账单无限重试，而付款只会一直回与账单无关的"余额不足"，把人工引向错误方向。
// 状态字段缺失时按仍可支付处理（字段异常不该把可付账单判死）；传输/解析故障返回 perr，
// 由调用方维持原有提示，避免上游抖动被误判成账单失效。账单不存在时上游回非成功业务码。
func upstreamInvoiceUnusable(ctx context.Context, cfg server.Config, invoiceID string) (bool, error) {
	var inv struct {
		Data struct {
			Detail struct {
				Status flexString `json:"status"`
			} `json:"detail"`
		} `json:"data"`
	}
	if err := getJSON(ctx, cfg, "/get_invoices_detail?id="+invoiceID, &inv); err != nil {
		if isBizError(err) && (strings.Contains(err.Error(), "账单不存在") || strings.Contains(err.Error(), "未找到账单")) {
			return true, nil
		}
		return false, err
	}
	status := strings.TrimSpace(string(inv.Data.Detail.Status))
	if strings.EqualFold(status, "Unpaid") {
		return false, nil
	}
	if strings.EqualFold(status, "Cancelled") {
		return true, nil
	}
	return false, &server.ManualReviewError{Msg: "上游账单已付或状态不明确，禁止重建", UpstreamInvoiceID: invoiceID}
}
