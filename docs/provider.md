# LumeIDC 上游供应商接入指南

LumeIDC 通过 `server.Provider` 接口对接上游（参照 FOSSBilling 模块化思想，Go 编译期注册）。
现有实现：`internal/server/zjmf`（智简魔方财务）、`internal/server/easypanel`（kangle+EasyPanel）。

## 快速接入新上游

1. 新建包 `internal/server/<code>/`，实现 `server.Provider` 必选接口：

```go
type Provider interface {
    Code() string        // 唯一标识，存 servers.provider（如 "zjmf"/"easypanel"）
    Name() string        // 后台下拉显示名
    TestConnection(ctx, cfg) error
    Catalog(ctx, cfg) ([]UpstreamProduct, error)   // 无目录 API 时返回 nil, nil
    Provision(ctx, cfg, req, checkpoint) (ProvisionResult, error)  // 必须幂等
    Renew(ctx, cfg, hostID, cycle) error           // 无上游续期概念时 no-op
    Suspend / Unsuspend / Terminate(ctx, cfg, hostID) error
    Status(ctx, cfg, hostID) (ServiceStatus, error)
}
```

2. 在 `internal/httpserver/server.go` 注册：`providers.Register(yourpkg.Provider{})`。

3. 凭据：统一用 `server.Config{APIURL, APIUsername, APIKey}`（对应 servers 表三件套，
   用不到的字段留空即可，如 EasyPanel 只用 APIURL+APIKey=面板安全码）。

## 可选能力接口（类型断言启用，实现即生效）

| 接口 | 用途 |
|---|---|
| `ConfigOptionsFetcher` | 拉取商品配置项（购买页动态计价） |
| `CatalogLister` | 轻量目录（仅 PID/名称/分组） |
| `ProductMetaFetcher` | 商品描述/库存 |
| `PriceFetcher` | 商品基础价 |
| `PIDOptionalProvider` | 上游产品 ID 可省略（弹性模式） |
| `MarkupFreeProvider` | 无上游成本概念（本地自主定价）：产品表单隐藏利润加成，保存时服务端强制归零 |
| `ProductFormProvider` | 声明产品表单差异（PID 提示；`MarkupFree` 注册表自动合并） |
| `ProductFormWidgetProvider` | 产品表单独立区块（内嵌模板插槽注入，同 `DetailWidgetProvider` 模式）：ZJMF 目录下拉/拉取配置项（`zjmf/productform.html`）、EP 站点类型（`easypanel/productform.html`）。区块脚本约定 `provFormRegister(code, 托管fields, init)`：init 在加载与每次选中时调用（幂等，从 `cfgOptions` 回显）；切换供应商时共享表单自动清理其它供应商托管 fields |
| `ProviderUI` | 声明凭据表单字段（服务器表单按类型动态渲染，`api_url`/`api_username`/`api_key` 三列映射） |
| `DetailWidgetProvider` | 自带服务详情页管理区块（内嵌模板 + 插槽注入，实现后全局面板自动让位）。参考 `easypanel/widget.go` |
| `ConsoleProvider` | 电源/重装/救援/重置密码（详情页控制台） |
| `HostDetailFetcher` / `HostOverviewFetcher` | 详情页登录/系统信息；`Detail.PanelURL` 非空时显示"登录主机面板"直登按钮 |
| `ChartFetcher` / `PowerStatusFetcher` / `TrafficUsageFetcher` / `SnapshotProvider` / `ModuleBlocksProvider` / `ModuleProvider` | 监控/快照/NAT 等高级能力 |

## 约定

- **幂等**：`Provision` 可崩溃重跑。每步成功后写 checkpoint
  （`checkpoint.SetCheckpoint(key, val)`），重入时先查 checkpoint 跳过已完成步骤。
  详见 zjmf Provision 的三段 checkpoint 示例。
- **hostID 语义**：`ProvisionResult.UpstreamHostID` 存入 `services.upstream_host_id`，
  后续所有生命周期/控制台调用都以它定位上游实例。上游用字符串标识时需自行派生稳定映射
  （EasyPanel 用 `u{ServiceID}`，`ProvisionRequest.ServiceID` 已传入）。
- **密码**：上游无法随时查回密码时，把开通/重置时应用的密码放进
  `ProvisionResult.Password` / `ResetPassword` 返回值，调用方会自动 AES-GCM 加密落库
  `services.password_crypt` 并在详情页回填。
- **续费策略**：上游不支持续期 API 时，可像 EasyPanel 一样"上游永不过期+本地管控"
  （`Renew` no-op，到期停机走 `Suspend`）。
- **弹性模式**：实现 `PIDOptionalProvider` 后，绑定了服务器但 `upstream_pid=0` 的产品
  仍走上游开通，订单配置项通过 `ProvisionRequest.ConfigOpts` 直传（注意白名单过滤，
  防止注入协议保留参数，参考 easypanel.flexibleFields）。

## 测试要求

- 签名/参数映射/响应解析必须有单测（无需真实上游，参考 easypanel/provider_test.go）。
- `go build ./... && go vet ./... && go test ./...` 全绿。
- 有真实环境时走一遍：测试连接 → 开通 → 状态 → 停机/恢复 → 重置密码 → 删除。
