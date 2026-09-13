# LumeIDC 半成品

免费开源的 IDC 财务管理系统（Go + PostgreSQL）。

## 特性

- 单二进制部署，模板与迁移全部内嵌
- 安装向导：浏览器打开即装，装完自动锁定
- 用户端：注册/登录、产品购买、账单支付、我的服务、自动续期
- 服务生命周期：到期停机 → 30 天宽限后删除
- 支付：模拟网关（测试用）、易支付协议、支付宝当面付（后台可配置）
- 安全：bcrypt 密码、参数化查询全覆盖、CSRF 校验、HMAC 签名会话、登录延迟限速

## 环境要求

- Go 1.22+（仅编译时需要）
- PostgreSQL 12+

## 快速开始

1. 编译：

```
go build -o lumeidc ./cmd/lumeidc
```

2. 运行（需先建好 PostgreSQL 数据库）：

```
./lumeidc
```

3. 浏览器打开 `http://localhost:8080/install`，按表单填写数据库地址/端口/库名/用户/密码和管理员账号，提交即完成安装。

安装完成后 `config.yaml` 自动生成（权限 0600），安装向导永久锁定。修改监听端口可直接改配置文件或用环境变量 `LISTEN=:9000` 覆盖。

## 支付配置

登录管理后台 `/admin` → 支付网关，新增“支付宝当面付”实例，填写应用 ID、应用私钥和支付宝公钥。应用公钥需先上传到支付宝开放平台；支付宝公钥用于服务器验证异步回调，两者不是同一把公钥。在线支付手续费率可在每个网关实例中单独设置（0 到 100%，按分四舍五入）；支付页会按所选网关的费率向用户加收，余额支付不收手续费。在线充值的手续费由用户承担，但充值到账仍为充值本金。支付宝预下单成功后会跳转二维码地址，用户使用支付宝扫码付款。

异步回调地址为 `{base_url}/pay/notify?code=网关编码`，请确保支付宝平台能访问。支付宝应用需配置 RSA2，并将回调地址配置为该地址。

## 测试

单元测试无需数据库；数据库集成测试需设置环境变量：

```
TEST_DATABASE_DSN="postgres://user:pass@host:5432/db?sslmode=disable" go test ./...
```

## 目录结构

```
cmd/lumeidc/          入口
internal/config       配置加载
internal/db           连接与迁移
internal/middleware   会话 / CSRF / 权限
internal/handler      HTTP 处理器、SPA 外壳与 JSON API
internal/repo         数据访问（全参数化）
internal/service      业务逻辑（订单/支付/服务）
internal/gateway      支付网关接口与实现
internal/cron         定时任务（到期停机/删除）
internal/db/migrations  SQL 迁移文件
web/                  Vue3 + TS + Element Plus + Tailwind 前端工程（Vite 双入口）
```

## UI 架构（前后端分离）

前端为 **Vue3 SPA**（`web/`，Vite 构建，产物 `go:embed` 进单二进制）。**SPA 为前台与后台的唯一界面**，Go 侧对同一 URL 提供 JSON API；仅少量无状态/流程页保留 SSR。前端构建产物是**强制依赖**：`internal/handler/webui/dist` 缺失时进程拒绝启动并提示 `cd web && npm run build`。

**内容协商**：后端在**同一 URL**上按客户端返回 SPA 外壳（HTML）或 JSON——浏览器导航（`Accept` 含 `text/html`）由 `handler.SPAGate` 返回 SPA 外壳；SPA 的 `fetch` 显式携带 `Accept: application/json` 走 JSON API（见 `internal/handler/json.go` 的 `wantsJSON`）。会话（HMAC cookie）与 CSRF（`X-CSRF-Token` 请求头 / `_csrf` 表单字段）两种形态共用同一套会话存储，前台启动先调 `GET /session` 获取 csrf / 站点 / 登录态。

**SPA 接管范围（`handler.SPAGate` 的 `spaOwned`）**：

```
/            /cart          /buy/{id}      /pay/{id}
/services    /services/{id} /user          /user/{recharge,invoices,password,profile}
/notifications
```

这些路径的旧 SSR 模板与渲染分支**已删除**；`/login`、`/register`、`/user/verification` 通常也由 SPA 接管。

**仍由 SSR 承载（不可删）**：
- `/install`（安装向导，装完锁定）。
- `/services/{id}` 的**子路径**：JSON 端点 `blocks` / `block/{fn}` / `block-rules` / `snapshot` / `chart` / `usage` / `power` / `traffic` 等，以及 `vnc-ws`（wss 隧道）与 `vnc-pass`（当前 VNC 会话密码）。
- `/pay/notify`（网关回调）、`/pay/qr`（本站二维码结算页）、`/mock/pay/{no}`（测试网关页）。
- **认证页**：后台登录 `/admin/login`，以及部署启用**手机号流程**（`login_phone_otp_enabled` / `registration_phone_enabled`）时的 `/login`、`/register`（`auth.html`）。
- **实名插件流程**：配置了自动实名插件（`verification_provider` 非空且非 `manual`）时的 `/user/verification`（`site.html` + `user_verification.html`）。

前两个认证/实名判定见组合根 `authReady` / `verifyReady`。新增 SPA 页面时，若该 URL 同时存在 SSR 路由，需把它加进 `spaOwned` 并补 `TestSpaOwned` 断言。

**管理后台入口**：后台 SPA（`dist/admin.html`，hash 路由）由 `SPAGate` 挂在**后台根路径**上——默认 `/admin`，启用自定义后台路径后即该路径（`AdminPath` 中间件会把它改写为内部 `/admin`）。后台所有列表/表单/写操作均为 JSON API，旧后台模板与 `renderAdmin` 已删除；`/admin/login` 保留 SSR 图形验证码页。自定义路径启用时直连 `/admin` 仍返回 404（不泄漏路径）。

**构建**：

```
cd web && npm install && npm run build   # 产物 → internal/handler/webui/dist
cd .. && go build -o lumeidc ./cmd/lumeidc
```

`webui/dist` 已加入 `.gitignore`（仅保留 `.gitkeep` 占位），干净 clone 可直接 `go build`/`go test`；但**运行前必须构建前端**，否则进程启动时即报错。仅构建机需要 Node，运行环境仍为单二进制。

**已知取舍（体积）**：Element Plus 目前为 `app.use(ElementPlus)` 全量注册 + `element-plus/dist/index.css` 全量样式，前端主包约 1 MB（gzip ~350 KB）、样式约 380 KB。如需瘦身，可改用 `unplugin-vue-components` + `unplugin-auto-import` 按需引入——但需同时把各文件显式 `import { ElMessage } from 'element-plus'` 改为自动导入（否则程序式组件的样式不会随包引入），并改用 `<el-config-provider :locale="zhCn">` 配置中文，属于一次跨约 30 个文件的重构，务必逐个页面回归。ECharts 已完成按需注册（仅折线图所需模块，异步加载，不进首屏）。

开发期 `web/` 内 `npm run dev` 会把 API 代理到本地 Go 服务（`/__api/*` → `:8080`，即 `config.yaml` 的 `listen` 默认值；若本地改了端口，用 `LUME_DEV_API=http://localhost:端口 npm run dev` 覆盖）。

**供应商能力契约**：供应商差异全部改为结构化声明、由 SPA 渲染——后台产品表单用 `ProductFormSpecProvider`（`ProductFormField`），用户侧服务详情「功能面板」用 `ModuleBlocksProvider` / `ModuleProvider`（`/services/{id}/blocks`、`/block-rules`、`/snapshot` 等 JSON 端点，前端组件 `web/src/components/ServiceBlocks.vue` 渲染 NAT 转发 / 共享建站 / 安全组 / 实例设置 / 快照备份）。原「HTML+脚本插槽」机制（`productform.html`、`widget.html` 与依赖 Bootstrap4/jQuery 的模块页）已删除，不再由 Go 原生渲染。

VNC 控制台为前台 SPA 页面（`/services/{id}/console`，`web/src/views/ServiceConsole.vue`），满屏裸页（路由 `meta.bare`，不套前台头尾），noVNC 以 `@novnc/novnc` 随前端打包；Go 侧只保留 wss 隧道 `vnc-ws` 与会话密码 `vnc-pass`，上游 wss 地址与令牌不下发浏览器。

安装结果页（`done.html`、`error.html`）仍由 Go 原生渲染，样式已全部内联，不依赖外部静态资源。

## ai提供商

https://api.6top.site/


## License

MIT
