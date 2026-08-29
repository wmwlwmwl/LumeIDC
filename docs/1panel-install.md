# LumeIDC 在 1Panel 中的安装流程

## 前置条件

- 1Panel 面板已安装，可创建 Go 应用
- 已有一台可用的 PostgreSQL 数据库（本机或远程均可），并提前建好空库
- 服务器防火墙 / 云安全组已放行 8080 端口

## 一、创建应用

1Panel → 容器 → 应用商店/编排（或「网站 → Go 应用」，视 1Panel 版本而定），按以下填写：

| 项目 | 填写值 | 说明 |
|---|---|---|
| 名称 | `lume` | 任意 |
| 运行目录 | `/lume` | 必须是仓库根目录（能看到 `go.mod`、`cmd/`、`internal/`），不要进子目录 |
| 启动命令 | `go build -o lumeidc ./cmd/lumeidc && ./lumeidc` | 必须编译后**运行**，只写 `go build` 会编译完就退出导致容器循环重启 |
| 端口映射 | 主机 `8080` → 容器 `8080` | 与程序默认监听 `:8080` 一致 |

> 启动命令也可以简写为 `go run ./cmd/lumeidc`，首次启动稍慢。

## 二、持久化（重要）

安装完成后程序会在运行目录写入 `config.yaml`。如果 `/lume` 是容器临时层，重启即丢、回到安装向导。务必添加挂载：

```
宿主机目录（如 /opt/1panel/apps/lume/data） → /lume
```

## 三、执行安装向导

浏览器打开：

```
http://服务器IP:8080/install
```

按表单填写：

- **数据库地址**：不要填 `localhost`（那指向容器自身）。本机 PG 填宿主机内网 IP 或网关地址；远程 PG 填远程域名/IP
- **端口**：默认 5432
- **库名 / 用户 / 密码**：提前在 PG 中建好的空库与账号
- **管理员账号 / 密码**：密码至少 8 位
- **Base URL**：`http://服务器IP:8080`（有域名填域名）

提交成功会显示完成页面。

## 四、重启应用（必做）

安装向导进程只注册了 `/install` 路由，**不会热切换**成完整应用，此时访问其他页面是 `404 page not found`，属正常现象。

在 1Panel 中重启应用，或：

```bash
docker restart lume
```

重启后访问：

```
http://服务器IP:8080/login
```

## 五、常见问题排查

| 现象 | 原因 | 解决 |
|---|---|---|
| 8080 打不开（CONNECTION_REFUSED） | 容器崩溃退出，看日志 `docker logs --tail 50 lume` | 按日志处理 |
| 容器循环重启、日志只有编译输出 | 启动命令只写了 `go build` | 改为 `go build ... && ./lumeidc` |
| `读取配置失败: 生产环境请使用 SSL 数据库连接...` | 安装器生成的 DSN 带 `sslmode=disable`，配置校验拒绝远程明文连接 | 编辑 `/lume/config.yaml` 末尾加一行 `allow_insecure_db: true`，重启容器 |
| 重启后又回到安装向导 | `/lume` 未持久化，`config.yaml` 丢失 | 添加宿主机目录挂载到 `/lume`，重新安装 |
| 安装时数据库连接失败 | DB 地址填了 `localhost` 或容器无法连通 | 改填宿主机 IP / 远程地址，确认 PG 监听与访问权限 |
| `dial tcp ...:5432` 启动失败 | 重启后数据库不可达 | 检查 DB 是否在运行、地址是否可从容器内访问 |

## 附：本地编译运行（非 1Panel）

```bash
go build -o lumeidc ./cmd/lumeidc
./lumeidc
# 打开 http://localhost:8080/install
```
