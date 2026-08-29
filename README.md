# LumeIDC 半成品

免费开源的 IDC 财务管理系统（Go + PostgreSQL）。

## 特性

- 单二进制部署，模板与迁移全部内嵌
- 安装向导：浏览器打开即装，装完自动锁定
- 用户端：注册/登录、产品购买、账单支付、我的服务、自动续期
- 服务生命周期：到期停机 → 30 天宽限后删除
- 支付：模拟网关（测试用）、易支付协议（后台可配置）
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

登录管理后台 `/admin` → 支付网关，填写易支付的网关地址、商户 PID、密钥即可。未配置时用户支付页使用模拟网关（直接确认到账，仅供测试）。

异步回调地址为 `{base_url}/pay/notify/epay`，请确保易支付平台能访问。

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
internal/handler      HTTP 处理器 + 模板
internal/repo         数据访问（全参数化）
internal/service      业务逻辑（订单/支付/服务）
internal/gateway      支付网关接口与实现
internal/cron         定时任务（到期停机/删除）
db/migrations         SQL 迁移文件
```

## License

MIT
