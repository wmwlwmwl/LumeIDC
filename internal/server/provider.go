package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"lumeidc/internal/repo"
)

// ManualReviewError 标记需要人工复核的错误（如 settle 成功但 checkpoint 未落库）。
// 任务应进入 manual_review 状态，不自动重试。
type ManualReviewError struct {
	Msg string
	// UpstreamInvoiceID 上游账单号（如有），供人工查询
	UpstreamInvoiceID string
	// UpstreamHostID 上游主机号（如有）
	UpstreamHostID int64
}

func (e *ManualReviewError) Error() string { return e.Msg }

// RetryLaterError 标记"等外部条件自己好"的失败（典型：上游余额不足）。
// 与 ManualReviewError 的区别：不需要人工介入，队列按固定间隔持续重试、不消耗重试次数，
// 直到条件满足（上游充值到账）或转为人工（账单已被删除）。
type RetryLaterError struct{ Msg string }

func (e *RetryLaterError) Error() string { return e.Msg }

// IsManualReview 检查错误是否为需要人工介入（不自动重试）的类型。
// RetryLaterError 不算人工介入——它是等外部条件，由队列继续重试。
// 用 errors.As 而非类型断言：调用方包了一层（%w）也能识别，避免漏判后退回自动重试，
// 而这类失败（等人工确认、上游已涨价）自动重试永远不会成功。
func IsManualReview(err error) bool {
	var mre *ManualReviewError
	if errors.As(err, &mre) {
		return true
	}
	var pce *PriceChangedError
	return errors.As(err, &pce)
}

// IsRetryLater 检查错误是否为"等外部条件、应保持重试"的类型（不消耗重试次数）。
func IsRetryLater(err error) bool {
	var rle *RetryLaterError
	return errors.As(err, &rle)
}

// ErrHostMissing 上游明确回了业务错误、但原因不明（典型：该实例已被上游删除/释放）。
// 它与网络不通、超时、鉴权失败严格区分：后者是"我方或链路故障"，不能据此推断资源已消失。
// Provider 用 %w 包装本错误后，SyncUpstreamStatus 才把它计入 upstream_miss_count；
// 未包装的错误一律只记日志、不计分，避免上游抖动误判成资源消失。
var ErrHostMissing = errors.New("upstream host missing")

// IsHostMissing 检查错误是否为"上游该实例不可见"（Provider 已用 %w 包装 ErrHostMissing）。
func IsHostMissing(err error) bool { return errors.Is(err, ErrHostMissing) }

// PriceTolerance 上游账单与本地金额的容差（人民币分）：口径差异在 2 分内视为一致。
// 开通前比价、续费前比价、升级差价核对共用。
// ponytail: 一期按人民币分固定 0.02；多币种二期再按汇率/比例。
const PriceTolerance = 0.02

// PriceChangedError 上游按当前价出的账单高于下单时的成本额：继续付款会吃掉利润甚至亏损。
// 上游流程据此停下不进自动重试，交后台人工二选一：按此价继续（开通/续费），或退款给用户。
type PriceChangedError struct {
	UpstreamAmount    float64 // 上游按当前价算出的金额
	ExpectAmount      float64 // 下单时的成本额（orders.config_snapshot.quote.total）
	UpstreamInvoiceID string  // 上游账单号（开通路径已建账单时有值）
	UpstreamPID       int64
}

// PriceChangedPrefix PriceChangedError 文案里的固定片段。
// 后台服务列表据此识别"这次失败是上游改价、要管理员决定按新价继续还是退款"，
// 从而在点「重试」时弹二次确认（改这段文案需同步这里）。
const PriceChangedPrefix = "上游价格已变动"

func (e *PriceChangedError) Error() string {
	return fmt.Sprintf("%s：需付 ￥%.2f，下单时成本 ￥%.2f（贵 ￥%.2f）。"+
		"确认后再次重试即按此价继续，或改为退款给用户",
		PriceChangedPrefix, e.UpstreamAmount, e.ExpectAmount, e.UpstreamAmount-e.ExpectAmount)
}

// Provider 上游供应商适配器接口。实现方负责认证、协议细节。
type Provider interface {
	Code() string
	Name() string
	// TestConnection 验证凭据与连通性。
	TestConnection(ctx context.Context, cfg Config) error
	// Catalog 拉取上游商品目录（含价格与库存）。
	Catalog(ctx context.Context, cfg Config) ([]UpstreamProduct, error)
	// Provision 开通服务，返回开通结果。必须幂等：可凭 checkpoint 续跑。
	Provision(ctx context.Context, cfg Config, req ProvisionRequest, checkpoint CheckpointStore) (ProvisionResult, error)
	// Renew 续费。可凭 checkpoint 复用已建的上游续费账单，避免重试时重复建单。
	Renew(ctx context.Context, cfg Config, upstreamHostID int64, cycle string, checkpoint CheckpointStore) error
	Suspend(ctx context.Context, cfg Config, upstreamHostID int64) error
	Unsuspend(ctx context.Context, cfg Config, upstreamHostID int64) error
	Terminate(ctx context.Context, cfg Config, upstreamHostID int64) error
	Status(ctx context.Context, cfg Config, upstreamHostID int64) (ServiceStatus, error)
}

// PIDOptionalProvider 可选：上游产品 ID 可省略的供应商（弹性配置模式，
// 配额等参数由订单配置项直传，如 EasyPanel add_vh 详细参数模式）。
// 未实现该接口或返回 false 时，upstream_pid=0 的产品视为纯本地服务，不走上游开通。
type PIDOptionalProvider interface {
	PIDOptional() bool
}

// MarkupFreeProvider 可选：无上游成本概念的供应商（本地自主定价，如 EasyPanel）。
// 利润加成（上游成本外毛利）对其无意义，产品保存时服务端强制归零。
type MarkupFreeProvider interface {
	MarkupFree() bool
}

// BalanceFetcher 可选：拉取上游账户余额（测试连接时展示）。
type BalanceFetcher interface {
	FetchBalance(ctx context.Context, cfg Config) (string, error)
}

// FieldSuggestion 配置标识建议（后台配置项弹窗下拉候选）。
type FieldSuggestion struct {
	Field string `json:"field"`
	Label string `json:"label"`
}

// ProductFormHints 供应商对产品表单的差异化声明（布尔/文案级；带 UI 的差异走 ProductFormSpecProvider）。
type ProductFormHints struct {
	MarkupFree       bool              `json:"markupFree,omitempty"`       // 隐藏「利润加成」区
	PIDHint          string            `json:"pidHint,omitempty"`          // PID 输入框的专属提示文案
	FieldSuggestions []FieldSuggestion `json:"fieldSuggestions,omitempty"` // 配置标识 field 候选（如 EP 的 web_quota）
}

// ProductFormProvider 可选：声明产品表单差异（MarkupFree 由注册表自动合并，无需自填）。
type ProductFormProvider interface {
	ProductFormHints() ProductFormHints
}

// ConfigOptionsFetcher 可选拉取配置项能力的供应商（类型断言使用）。
type ConfigOptionsFetcher interface {
	FetchProductConfigOptions(ctx context.Context, cfg Config, upstreamPID int64) ([]repo.ConfigOption, error)
}

// CatalogLister 可选：仅列目录（PID/名称/分组），不逐个拉取价格/库存/描述，
// 用于快速填充“上游商品（按分组）”下拉；详情在选中后由 pullConfig 拉取。
type CatalogLister interface {
	CatalogLight(ctx context.Context, cfg Config) ([]UpstreamProduct, error)
}

// ProductMetaFetcher 可选拉取商品描述与库存（新建页选中后填充用）。
type ProductMetaFetcher interface {
	FetchProductMeta(ctx context.Context, cfg Config, upstreamPID int64) (desc string, stock int, err error)
}

// PriceFetcher 可选拉取商品基础价能力的供应商（后台“拉取配置项”同步刷新基础价用）。
type PriceFetcher interface {
	FetchProductPrice(ctx context.Context, cfg Config, upstreamPID int64) (monthly, quarterly, yearly float64, err error)
}

// ProductSnapshot 单商品全量快照（一次上游请求取全：三周期基础价 + 配置项）。
type ProductSnapshot struct {
	Monthly       float64
	Quarterly     float64
	Yearly        float64
	ConfigOptions []repo.ConfigOption
}

// ProductSnapshotFetcher 可选：一次请求拉取单商品基础价与配置项。
// 下单前的实时价格校验需要同时拿到基础价与配置项，分别调用 FetchProductPrice /
// FetchProductConfigOptions 会对同一上游接口重复请求，故合并为一个方法。
type ProductSnapshotFetcher interface {
	FetchProductSnapshot(ctx context.Context, cfg Config, upstreamPID int64) (ProductSnapshot, error)
}

// UpgradeTarget 上游产品可升级目标（本地服务升降级的目标候选）。
type UpgradeTarget struct {
	UpstreamPID int64  `json:"pid"`
	Name        string `json:"name"`
}

// UpgradeTargetProvider 可选：返回某上游 host 可升级目标（商品维度）。
// 上游无升降级能力或无可升级目标时返回空（nil error），调用方据此隐藏升降级入口。
// best-effort：网络/鉴权/解析失败也返回空并记录日志，不阻断详情页。
type UpgradeTargetProvider interface {
	UpgradeTargets(ctx context.Context, cfg Config, upstreamHostID int64) ([]UpgradeTarget, error)
}

// UpgradeRequest 上游升降级请求（host 维度换商品）。
type UpgradeRequest struct {
	OrderID    int64   // 本地升级订单号：拼账单检查点键（同一服务可能多次升级，不能只看服务）
	TargetPID  int64   // 上游目标商品 id；弹性模式（PIDOptional）可为 0，改由 ConfigOpts 描述配置
	Cycle      string  // monthly/quarterly/yearly
	DiffAmount float64 // 本地差价（目标周期售价折算后的差额），供上游账单核对
	// ConfigOpts 目标配置选择（如 web_quota/db_quota/flow_limit）。
	// 供弹性模式上游（EasyPanel）据此变更实例配额——这类上游没有"商品"概念，
	// 升级的实际含义是把这些配额参数改掉。
	ConfigOpts map[string]string
}

// HostUpgradeProvider 可选：上游 host 升降级执行能力（换商品）。
// 未实现的供应商（本地定价模式，如 EasyPanel）由调用方仅做本地换产品。
type HostUpgradeProvider interface {
	// Upgrade 执行升降级。可凭 checkpoint 复用已结算的升级账单，避免重试时重复结算。
	Upgrade(ctx context.Context, cfg Config, upstreamHostID int64, req UpgradeRequest, checkpoint CheckpointStore) error
}

// Config 连接配置（来自 servers 表行）。
type Config struct {
	APIURL             string `json:"api_url"`
	APIUsername        string `json:"api_username"`
	APIKey             string `json:"api_key"`
	CredentialRevision int    `json:"credential_revision,omitempty"` // 凭据版本号，轮换时递增使缓存失效
}

// UpstreamProduct 上游商品目录条目。
type UpstreamProduct struct {
	PID       int    `json:"pid"`
	Name      string `json:"name"`
	GroupName string `json:"group_name"`
	Monthly   float64
	Quarterly float64
	Yearly    float64
	// DisplayMonthly 仅目录展示：基础价为 0 时按最低可配置月价兜底（不参与真实计价）。
	DisplayMonthly float64
	Stock          int // -1 不限
	ConfigCount    int // 可配置项数量
	Description    string
	// ConfigOptions 目录回填时顺带解析的配置项（同一次 get_product_config 响应）。
	// 导入/同步可直接复用，避免对同一商品重复请求上游。
	ConfigOptions []repo.ConfigOption
}

// DisplayPrice 目录展示首个可售周期价格；无基础价时用最低配置月价兜底（仅展示）。
func (u UpstreamProduct) DisplayPrice() float64 {
	for _, price := range []float64{u.Monthly, u.Quarterly, u.Yearly} {
		if price > 0 {
			return price
		}
	}
	return u.DisplayMonthly
}

// DisplayCycle 目录展示价格对应的首个可售周期。
func (u UpstreamProduct) DisplayCycle() string {
	for _, cycle := range []struct {
		name  string
		price float64
	}{
		{"monthly", u.Monthly}, {"quarterly", u.Quarterly}, {"yearly", u.Yearly},
	} {
		if cycle.price > 0 {
			return cycle.name
		}
	}
	return "monthly"
}

// ProvisionResult 开通结果。
type ProvisionResult struct {
	UpstreamHostID int64
	// Password 开通时应用到实例的密码（供应商生成或透传用户所填）。
	// 非空时由调用方加密落库 services.password_crypt；空表示密码可随时从上游查回（如 zjmf）。
	Password string
}

// ProvisionRequest 开通请求。
type ProvisionRequest struct {
	UpstreamPID int64             `json:"upstream_pid"`
	Cycle       string            `json:"cycle"` // monthly/quarterly/yearly
	Hostname    string            `json:"hostname"`
	Password    string            `json:"password"`
	ConfigOpts  map[string]string `json:"config_opts,omitempty"` // field -> 值/子项
	// ServiceID 本地服务 ID。供应商可用它派生上游唯一标识（如 EasyPanel 站点名 u{id}）。
	ServiceID int64 `json:"service_id,omitempty"`
	// ExpectAmount 下单时的成本额（orders.config_snapshot.quote.total）。
	// 上游按当前价出的账单高于它即判定为"上游已涨价"，转人工决定强制开通或退款；
	// 为 0（老数据无快照）时跳过该比对。
	ExpectAmount float64 `json:"expect_amount,omitempty"`
}

// ServiceStatus 上游实例状态。
type ServiceStatus struct {
	Status   string `json:"status"` // active/suspended/terminated/unknown
	ExpiryAt string `json:"expiry_at,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

// HostDetail 上游实例登录与系统信息（详情页实时展示，不落库）。
type HostDetail struct {
	IP        string // 实例 IP
	Username  string
	Password  string // 敏感：仅用于页面展示，不留存
	Port      string
	Status    string // 实例状态（实时，如 运行中/硬重启中）
	OSName    string // 系统名称，如 Ubuntu
	OSVersion string // 系统版本，如 Ubuntu-20.04.1-x64
	// PanelURL 主机面板地址，**仅用于展示**（详情页给用户看、可点开手动登录）。
	// Password 为空时由调用方用 password_crypt 解密填充。
	PanelURL string
	// PanelLoginURL 自动登录的**表单提交地址**（POST username+passwd）。
	// 与 PanelURL 必须分开：EasyPanel 的 a=login 是"处理登录提交"，a=loginForm 才是
	// "显示登录表单"。用同一个地址会导致用户点开展示链接（GET）时被当成一次空凭证
	// 登录提交，直接报"账号密码错误"。为空时前端回退用 PanelURL。
	PanelLoginURL string
	// 以下字段取自 /host/header 的 host_data / config_options，详情页「实例信息」面板展示用。
	AdditionalIPs []string // 附加 IP
	BWLimit       string   // 带宽限额
	BWUsage       string   // 带宽已用
	Datacenter    string   // 数据中心 / 机房
	// EasyPanel 虚拟主机资源信息。
	WebQuota   string
	DBName     string
	DBQuota    string
	DBUsed     string
	FTP        bool
	Domain     string
	FlowLimit  string
	SpeedLimit string
	CreateTime string
	ExpiryAt   string // 上游服务到期时间，例如魔方的 nextduedate
}

// HostDetailFetcher 可选获取实例登录/系统信息能力的供应商（类型断言使用）。
type HostDetailFetcher interface {
	HostDetail(ctx context.Context, cfg Config, upstreamHostID int64) (HostDetail, error)
}

// ModuleArea 上游模块 client_area 方块（如 快照/安全组/NAT 转发/共享建站）。
type ModuleArea struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// ModuleButton 上游模块自定义按钮（module_button）。
type ModuleButton struct {
	Type     string `json:"type"` // default|custom
	Function string `json:"function"`
	Name     string `json:"name"`
	Group    string `json:"group"`
}

// ModuleSummary 详情页"产品模块"清单，来自 /host/header 的模块开关。
type ModuleSummary struct {
	Areas    []ModuleArea   `json:"areas"`
	Buttons  []ModuleButton `json:"buttons"`
	HasChart bool           `json:"has_chart"`
}

// ModuleProvider 可选：上游模块通用能力（魔方云方块/按钮/图表）。
// 上游本质是把模块能力以统一网关暴露，前台据此动态渲染，无需为每个模块写死。
type ModuleProvider interface {
	ModuleSummary(ctx context.Context, cfg Config, upstreamHostID int64) (ModuleSummary, error)
	// ModulePage 拉取某方块页面 HTML（key 为 client_area 方块标识）。
	ModulePage(ctx context.Context, cfg Config, upstreamHostID int64, key string) (string, error)
	// ModuleAction 提交方块表单到上游，返回上游响应原文。
	ModuleAction(ctx context.Context, cfg Config, upstreamHostID int64, form url.Values) (string, error)
}

// HostOverview 一次 /host/header 返回的概况：实例登录/系统信息 + 产品模块清单。
type HostOverview struct {
	Detail  HostDetail
	Summary ModuleSummary
}

// HostOverviewFetcher 可选：一次性拉取详情页概况，避免对 /host/header 重复请求。
type HostOverviewFetcher interface {
	HostOverview(ctx context.Context, cfg Config, upstreamHostID int64) (HostOverview, error)
}

// ChartPoint 监控时序点（time 为显示用时间戳字符串，value 为数值）。
type ChartPoint struct {
	Time  string  `json:"time"`
	Value float64 `json:"value"`
}

// ChartLine 单条监控曲线（如 CPU使用率、内存已用）。
type ChartLine struct {
	Label  string       `json:"label"`
	Points []ChartPoint `json:"points"`
}

// ChartSeries 某类型监控数据（详情页 echarts 渲染用）。
// 实测上游：cpu 单系列；memory(总量/已用)、disk(读/写)、flow(进/出) 为双系列。
type ChartSeries struct {
	Type  string      `json:"type"` // cpu|memory|disk|flow
	Unit  string      `json:"unit"`
	Lines []ChartLine `json:"lines"`
}

// ChartFetcher 可选：拉取监控图表时序（上游 /provision/chart 等）。
type ChartFetcher interface {
	Chart(ctx context.Context, cfg Config, upstreamHostID int64, typ, sel string) (ChartSeries, error)
}

// PowerStatus 实时电源四态（对齐 ZJMF-CBAP：on/off/operating/fault）。
type PowerStatus struct {
	Status string `json:"status"` // on|off|operating|fault
	Desc   string `json:"desc"`   // 上游描述，如 "开机"
}

// PowerStatusFetcher 可选：实时电源状态（上游 /provision/default func=status）。
type PowerStatusFetcher interface {
	PowerStatus(ctx context.Context, cfg Config, upstreamHostID int64) (PowerStatus, error)
}

// TrafficDay 每日流量（按天，in/out）。
type TrafficDay struct {
	Time string  `json:"time"`
	In   float64 `json:"in"`
	Out  float64 `json:"out"`
}

// TrafficUsageFetcher 可选：每日流量曲线（上游 /host/trafficusage）。
type TrafficUsageFetcher interface {
	TrafficUsage(ctx context.Context, cfg Config, upstreamHostID int64) ([]TrafficDay, error)
}

// SnapshotItem 快照/备份条目（list[] 元素）。
type SnapshotItem struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"` // snap|backup
	Status     int    `json:"status"`
	Remarks    string `json:"remarks"`
	CreateTime string `json:"create_time"`
}

// DiskItem 实例磁盘（用于选盘创建快照/备份）。
type DiskItem struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // system|data
	Size int64  `json:"size"` // GB
	Dev  string `json:"dev"`
}

// SnapshotInfo 快照/备份概况：配额 + 列表 + 可用磁盘。
type SnapshotInfo struct {
	SnapNum   int            `json:"snap_num"`
	BackupNum int            `json:"backup_num"`
	List      []SnapshotItem `json:"list"`
	Disk      []DiskItem     `json:"disk"`
}

// SnapshotProvider 可选：快照/备份能力（上游 provision/custom/content v10）。
type SnapshotProvider interface {
	SnapshotInfo(ctx context.Context, cfg Config, upstreamHostID int64) (SnapshotInfo, error)
	// SnapshotAction 执行快照/备份操作（createSnap/delSnap/restoreSnap/createBackup/delBackup/restoreBackup）。
	SnapshotAction(ctx context.Context, cfg Config, upstreamHostID int64, fn string, form url.Values) (string, error)
}

// NatRule NAT 转发条目。
type NatRule struct {
	ID       int64  `json:"id"` // 0 = 默认规则（不可删除）
	Name     string `json:"name"`
	External string `json:"external"` // 外部地址 host:port
	Internal string `json:"internal"` // 内部端口
	Protocol string `json:"protocol"` // tcp/udp
}

// NatWebEntry 共享建站条目。
type NatWebEntry struct {
	ID       int64  `json:"id"`
	Domain   string `json:"domain"`
	External string `json:"external"`
	Internal string `json:"internal"`
}

// SecurityGroup 安全组。
type SecurityGroup struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SecurityRule 安全组规则。
type SecurityRule struct {
	ID          int64  `json:"id"`
	Description string `json:"description"`
	Action      string `json:"action"`    // accept|drop
	Direction   string `json:"direction"` // in|out
	Protocol    string `json:"protocol"`
	PortRange   string `json:"port_range"`
	IP          string `json:"ip"`
}

// SelectOption 下拉选项（ISO/启动顺序等）。
type SelectOption struct {
	Value    string `json:"value"`
	Name     string `json:"name"`
	Selected bool   `json:"selected"`
}

// SettingData 设置方块数据：ISO 挂载与启动顺序。
type SettingData struct {
	Iso  []SelectOption `json:"iso"`
	Boot []SelectOption `json:"boot"`
}

// ModuleBlocksProvider 可选：NAT/共享建站/安全组/设置方块的结构化数据与操作。
// 数据来自上游方块 HTML（服务端解析为结构体），操作统一走 provision/custom 通道。
type ModuleBlocksProvider interface {
	NatList(ctx context.Context, cfg Config, upstreamHostID int64) ([]NatRule, error)
	NatWebList(ctx context.Context, cfg Config, upstreamHostID int64) ([]NatWebEntry, error)
	SecurityGroups(ctx context.Context, cfg Config, upstreamHostID int64) ([]SecurityGroup, error)
	SecurityRules(ctx context.Context, cfg Config, upstreamHostID int64, groupID int64) ([]SecurityRule, error)
	SettingData(ctx context.Context, cfg Config, upstreamHostID int64) (SettingData, error)
	// BlockAction 执行方块操作（addNatAcl/delNatAcl/addNatWeb/delNatWeb/createSecurityGroup/
	// delSecurityGroup/linkSecurityGroup/createSecurityRule/delSecurityRule/mountIso/setBootOrder）。
	BlockAction(ctx context.Context, cfg Config, upstreamHostID int64, fn string, form url.Values) (string, error)
}

// CheckpointStore 开通编排的检查点存取（实现方在每步成功后写入，崩溃后据此幂等续跑）。
type CheckpointStore interface {
	GetCheckpoint(key string) (string, bool, error)
	SetCheckpoint(key, val string) error
	// DeleteCheckpoint 清除检查点，让开通流程从头重跑。
	// 仅用于上游账单被删除/作废、必须重新下单的场景，由管理员在后台手动触发。
	DeleteCheckpoint(key string) error
}

// 开通检查点键（存于 services.provision_data）。上游开通据此续跑避免重复下单；
// 账单被上游删除/作废时由 Provision 自行清掉失效账单号并重新结算（无需人工重置）。
// 注意：不要把 provision_data 整个清空——升级流程的 upgrade_<订单号> 检查点（done/refunded）
// 也在这里，清掉会导致降级重复退款。
const (
	CheckpointUpstreamInvoiceID = "upstream_invoice_id" // 已生成的上游账单号
	CheckpointUpstreamHostIDs   = "upstream_host_ids"   // 已开通的上游 host id 列表
	// CheckpointUpgradeInvoicePrefix 升级账单检查点键前缀，后缀为本地升级订单号。
	// 结算出账单后写入：重试复用同一张账单，管理员确认涨价后重试即按该账单强制开通。
	CheckpointUpgradeInvoicePrefix = "upgrade_invoice_"
	// CheckpointRenewInvoice 续费账单检查点键：重试复用已建的上游续费账单，避免重复建单；
	// 续费成功或后台退款取消后续费时清除。
	CheckpointRenewInvoice = "renew_invoice_id"
)
