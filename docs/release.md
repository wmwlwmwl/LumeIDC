# LumeIDC 发布与在线更新

本文覆盖从「发版」到「服务器后台点更新」的完整闭环。

## 版本号约定

- 版本使用 `v主.次.修订` 三段式，例如 `v1.2.3`；必须与 git tag 完全一致。

- 本地/未打 tag 的构建版本号为 `dev`（后台「系统更新」页可见），视为可升级到任一正式版。

- 程序通过 `-ldflags "-X main.version=vX.Y.Z"` 注入版本；打 tag 发布时由 GitHub
  Actions 自动注入，本地手动构建可不带（默认为 dev）。

## 一、发布（打 tag 自动构建）

1. 在 master 提交并推送代码后，打上版本 tag：

```bash
git tag v1.2.3
git push origin v1.2.3
```

1. `.github/workflows/release.yml` 自动执行：

- `go vet` + `go test` 全绿后交叉编译两个平台产物

- 产物与校验文件（资产命名与程序侧严格匹配，小写）：

```text
lumeidc_linux_amd64
lumeidc_linux_amd64.sha256
lumeidc_linux_arm64
lumeidc_linux_arm64.sha256
```

- 汇总到 GitHub Release 页面，带本次变更的 release notes

1. 需在仓库 Settings → Actions 启用 workflow（`contents: write` 权限已声明在
   workflow 内）。也可在 Actions 页手动触发（workflow\_dispatch，填入 `vX.Y.Z`）。

## 二、在线更新（服务器后台操作）

1. 登录后台 → `系统设置 → 系统更新`
2. 「检查更新」：请求 GitHub Releases 最新版，展示版本、发布时间、更新日志
   3.「立即更新」：程序自行完成

```text
GitHub 下载二进制 → SHA256 校验 → 备份当前二进制为 .bak → 原子替换
```

1. 弹窗确认「立即重启」：进程就地 exec 切换到新版本（PID 不变，端口无缝接管），
   重启后需重新登录后台；也可点「取消」稍后到面板手动重启。

## 三、更新源与安全

- 更新源固定为 GitHub Releases（仓库 `wmwlwmwl/LumeIDC`）。

- 安装包必须通过 `.sha256` 侧车校验才会替换，校验失败不触碰原文件。

- 校验通过后才执行备份 → 替换；替换失败自动恢复备份，原程序不受影响。

- 每次升级在运行目录留下一份 `lumeidc.bak`，不自动清理（数据保全优先）。

### 限流说明

GitHub 匿名 API 限流 60 次/小时（按出口 IP 计）。「检查更新」只在手动点击时触发，
无后台轮询；若多台机器同 IP 频繁触发返回 403，页面上会提示稍后再试。

## 四、回退到上一版本

任选其一：

```bash
# 方式一：就地切换回备份
cp /opt/lumeidc/lumeidc.bak /opt/lumeidc/lumeidc && docker restart lumeidc
# 宝塔
cp /www/wwwroot/lumeidc/lumeidc.bak /www/wwwroot/lumeidc/lumeidc && supervisorctl restart lumeidc
```

```bash
# 方式二：从 GitHub Release 下载旧版资产，手动替换后重启
```

## 五、约束与限制

- 仅支持 **Linux 服务器上的预编译二进制部署**（1Panel 挂载目录 / 宝塔运行目录可写即可）。

- Windows、`go run`、源码 `go build` 后手动运行的部署不可用（页面会提示）。

- 首次启动时新版本自动执行未应用的数据库迁移；跨多个版本升级也无需手动迁移。

- 若 Release 没有当前运行架构的产物，会提示「没有找到适用于 xx/xx 的安装包」。

