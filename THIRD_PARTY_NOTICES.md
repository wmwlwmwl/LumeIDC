# 前端第三方资源

LumeIDC 前端为 Vue3 SPA（`web/`），由 Vite 打包；构建产物 `internal/handler/webui/dist`
经 `//go:embed all:webui/dist`（`internal/handler/webui.go`）嵌入 Go 二进制，运行时不需要
npm、Node.js 或 CDN。

以下为 `web/package.json` 的运行时依赖及其许可证（取自各包内 `package.json` 的 `license`
字段，升级版本时请同步核对）。构建期工具（Vite、TypeScript、Sass、Vitest、Puppeteer 等，
见 `devDependencies`）不进入发布产物，不在此列。

| 依赖 | 许可证 | 用途 |
|---|---|---|
| `vue` | MIT | 前端框架 |
| `vue-router` | MIT | 路由 |
| `vue-i18n` | MIT | 国际化 |
| `pinia` | MIT | 状态管理 |
| `pinia-plugin-persistedstate` | MIT | 状态持久化插件 |
| `element-plus` | MIT | UI 组件库 |
| `@element-plus/icons-vue` | MIT | Element Plus 图标 |
| `@iconify/vue` | MIT | 图标组件（`art-svg-icon`） |
| `@iconify-json/ri` | Apache-2.0 | Remix Icon 图标数据 |
| `echarts` | Apache-2.0 | 图表（按需注册，异步加载） |
| `@novnc/novnc` | MPL-2.0 | VNC 控制台（`/services/{id}/console`） |
| `@vueuse/core` | MIT | 组合式工具函数集 |
| `mitt` | MIT | 事件总线（`utils/sys/mittBus.ts`） |
| `nprogress` | MIT | 路由切换进度条（`utils/router.ts`） |
| `vue-draggable-plus` | MIT | 表头列拖拽（`art-table-header`） |
| `ohash` | MIT | 哈希工具（当前 `web/src` 内未直接引用） |
| `tailwindcss` | MIT | 原子化 CSS |
| `@tailwindcss/vite` | MIT | Tailwind 的 Vite 插件 |

> `@novnc/novnc` 为 MPL-2.0（文件级弱著佐权）：修改其源码文件时需公开该文件的改动，
> 以依赖形式引用不受影响。

## 免责与保留

各依赖的完整许可证文本与版权声明见对应 npm 包内的 `LICENSE` 文件，发布包应保留这些声明。
本项目自身代码采用仓库根目录的 MIT License。
