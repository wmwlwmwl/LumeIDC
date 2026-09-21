# 前台模板开发

LumeIDC 的前台（官网 + 购买流程 + 用户中心）支持多套模板，管理员在后台「设置 → 前台模板」一键切换，即时生效。模板随前端一起构建、嵌入 Go 二进制分发。

## 模板结构

每套模板是 `web/themes/` 下的一个目录：

```
web/themes/mytheme/
├── index.html        # 必需，SPA 入口；<script src="/themes/mytheme/src/main.ts">
├── theme.json        # 可选，模板元信息（后台卡片展示）
├── theme.png         # 可选，预览图（建议 1200×900，首页截图）
└── src/
    ├── main.ts       # 创建 Vue app、注册 router/pinia
    ├── App.vue
    ├── router/index.ts
    └── views/...     # 页面组件，结构完全自由
```

目录名即模板 key，只允许小写字母、数字、`-`、`_`（最长 64 字符）。

theme.json 全部字段可选，缺省时后台卡片以 key 作为名称：

```json
{
  "name": "我的模板",
  "description": "一句话描述",
  "author": "作者名",
  "version": "1.0.0"
}
```

## 创建新模板

最快的路径是复制默认模板再改：

```powershell
Copy-Item -Recurse web\themes\default web\themes\mytheme
```

然后：

1. 改 `web/themes/mytheme/theme.json` 的名称等信息；
2. 把 `web/themes/mytheme/index.html` 里的脚本路径改为 `/themes/mytheme/src/main.ts`；
3. 执行 `npm run build`（构建会自动扫描 `themes/*/index.html`，无需改任何配置）；
4. 重新编译二进制后，到后台「设置 → 前台模板」启用。

构建产物落在 `internal/handler/webui/dist/themes/mytheme/`，经 `go:embed` 嵌入二进制；改模板代码后必须重新 `npm run build`（生产部署还要重编二进制）。

## 共享代码边界

模板不是孤岛，以下共享代码通过别名引用，**不要复制进模板目录**：

| 别名 | 指向 | 内容 |
| --- | --- | --- |
| `@/` | `web/src/` | `api/`（后端接口封装）、`http/`（会话/CSRF/错误处理）、`components/`（公共组件）、`utils/`、`stores/`、`plugins/` |
| `@views/` | `web/src/views/` | 前后台共享视图（服务详情/升降级/控制台/异常页）及全部后台页面 |
| `@/style.css` | `web/src/style.css` | 前后台共享样式令牌 |

规则：模板只持有「整套前台页面」，`web/src/` 持有「跨模板、跨端共享的代码」。多个模板都需要的组件应下沉到 `web/src/components/`；模板私有的留在模板目录内。不要跨模板互相 import。

## 路由职责

后端 [SPAGate](../internal/handler/webui.go) 按白名单把浏览器导航下发给当前激活模板的 `index.html`，模板内的 vue-router 必须覆盖这些路径：

- `/`、`/cart`、`/promotions`、`/promotion/{id}`
- `/buy/{id}`、`/pay/{id}`（收银台）
- `/login`、`/register`、`/forgot`、`/user/verification`
- `/user`、`/user/recharge`、`/user/invoices`、`/user/password`、`/user/profile`、`/user/promotion-coupons`
- `/services`、`/services/{id}`、`/services/{id}/upgrade`、`/services/{id}/console`
- `/notifications`、`/tickets`、`/tickets/{id}`
- 未匹配深链（`GET /{path...}` 兜底）也会下发模板外壳，模板路由应有 404 页

以下路径由后端 SSR/API 承载，模板不要路由它们，跳转时用整页跳转（`location.href`）而非 `router.push`：

- `/install`（安装向导，始终由 `default` 模板承载）
- `/services/{id}/module*`、`/services/{id}/chart`、`/services/{id}/vnc-ws`（上游面板代理）
- `/pay/notify`、`/pay/qr`、`/pay/{id}/status`（支付回调与轮询）
- `/admin*`（后台，独立的 admin SPA）

模板内调接口一律走 `@/http`（自动带 CSRF、统一中文报错），前端数据接口直接复用 `@/api/` 下现有封装。

## 切换与回退语义

- 激活模板存于 settings 表 `site_theme` 键，后台切换即时生效（用户刷新即见）。
- 每次下发前台外壳时校验：key 非法、或 `themes/{key}/index.html` 不存在（例如升级后模板被移除），自动回退 `default`。
- `default` 是系统保底模板：`WebUIBuilt()` 启动校验要求 `themes/default/index.html` 存在，**不要删除或改名**。

## 预览图

`theme.png` 会随构建拷贝进产物，经 `GET /admin/themes/{key}/preview` 下发。缺图时后台卡片显示空白占位，不影响功能。生成方式：跑起站点后用无头浏览器截首页，例如仓库 smoke 脚本同款 puppeteer-core 调用，视口 1200×900。

## 后台接口（仅供排查）

| 接口 | 说明 |
| --- | --- |
| `GET /admin/themes` | 模板列表 + 激活标记（扫 `dist/themes/`） |
| `POST /admin/themes/switch` | body `{"key":"mytheme"}`，校验存在后写库 |
| `GET /admin/themes/{key}/preview` | 预览图 PNG |

## 注意事项

- **纯静态模板可行**：入口只有一个 `index.html` 也算合法模板（无 Vue），适合活动落地页；交互需自己写原生 fetch。
- **开发模式限制**：`npm run dev` 的 Vite 开发服务器对所有前台导航固定回退 `default` 模板；开发新模板时请直接访问 `http://localhost:5173/themes/{key}/index.html`（带 HMR），或在构建后用正式二进制验证切换效果。
- **测试**：模板内的 `*.test.ts` 会被 vitest 自动收集（`web/themes/**/*.test.ts`）；图标扫描同样覆盖 themes 目录，`build-icons.mjs` 无需改动。
