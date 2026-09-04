# LumeIDC 二进制包部署指南

本文介绍使用预编译二进制包在宝塔面板部署 LumeIDC 的两种方式：

1. 宝塔「Go 项目」
2. 宝塔「Supervisor 管理器」

二进制包不需要安装 Go，也不需要执行 `go build`。

---

## 一、部署前准备

假设二进制文件放在：

```text
/www/wwwroot/lumeidc/lumeidc
```

目录结构：

```text
/www/wwwroot/lumeidc/
├── lumeidc
└── config.yaml
```

首次运行时还没有 `config.yaml`，程序会启动安装向导；安装完成后会自动生成该文件。

### 上传二进制文件

在宝塔文件管理器中创建目录：

```text
/www/wwwroot/lumeidc
```

上传二进制文件，并重命名为：

```text
/www/wwwroot/lumeidc/lumeidc
```

设置执行权限：

```bash
chmod 755 /www/wwwroot/lumeidc/lumeidc
```

如果使用 `www` 用户运行：

```bash
chown -R www:www /www/wwwroot/lumeidc
```

> 如果二进制文件放在 `/root` 目录下，`www` 用户可能没有权限访问。建议放到 `/www/wwwroot/lumeidc` 或 `/opt/lumeidc`。

### 检查服务器架构

```bash
uname -m
```

常见架构对应关系：

```text
x86_64  → linux-amd64
aarch64 → linux-arm64
```

二进制包必须与服务器 CPU 架构匹配。

---

## 二、方法一：宝塔「Go 项目」

部分宝塔版本的 Go 项目功能支持直接运行已编译的二进制文件。

### 1. 打开添加项目页面

进入：

```text
宝塔面板 → 网站 → Go 项目 → 添加 Go 项目
```

### 2. 填写项目参数

| 配置项 | 填写内容 |
|---|---|
| 项目执行文件 | `/www/wwwroot/lumeidc/lumeidc` |
| 项目名称 | `lumeidc` |
| 项目端口 | `8080` |
| 放行端口 | 不勾选 |
| 执行命令 | `./lumeidc` |
| 环境变量 | 无 |
| 运行用户 | `www` 或 `root` |
| 开机启动 | 勾选 |
| 项目备注 | `LumeIDC IDC 财务管理系统` |
| 绑定域名 | `yun.662662.xyz` |

### 3. 运行目录

项目运行目录必须是：

```text
/www/wwwroot/lumeidc
```

不能把运行目录设置为 `/www/wwwroot`，也不能把二进制文件所在目录设置错。

程序会从当前运行目录读取：

```text
config.yaml
```

所以最终配置文件应位于：

```text
/www/wwwroot/lumeidc/config.yaml
```

### 4. 关于「项目执行文件」

这里选择的是二进制文件本身：

```text
/www/wwwroot/lumeidc/lumeidc
```

不是：

```text
/www/wwwroot/lumeidc
```

也不是源码目录。

### 5. 启动命令

填写：

```bash
./lumeidc
```

不要填写：

```bash
go build -o lumeidc ./cmd/lumeidc && ./lumeidc
```

因为二进制包已经编译完成，不需要再次编译。

### 6. 首次安装

启动项目后访问：

```text
http://yun.662662.xyz/install
```

安装向导中填写：

```text
数据库地址：127.0.0.1
数据库端口：5432
数据库名：lumeidc
数据库用户：lumeidc
数据库密码：你的 PostgreSQL 密码
```

然后设置管理员账号和密码。

> 站点地址无需在安装时填写：安装完成后在「后台 → 站点设置 → 站点地址」配置，
> 留空则按用户访问的域名自动推断（支付回调、实名认证回调、邮件链接均以此为前缀）。

---

## 三、方法二：宝塔 Supervisor 管理器

如果宝塔「Go 项目」页面不能选择普通二进制文件，建议使用 Supervisor。

### 1. 安装 Supervisor

进入：

```text
宝塔面板 → 软件商店 → Supervisor 管理器
```

点击安装。

也可以在终端执行：

```bash
apt-get update
apt-get install -y supervisor
systemctl enable --now supervisor
```

### 2. 添加守护进程

进入：

```text
宝塔面板 → Supervisor 管理器 → 添加守护进程
```

填写：

| 配置项 | 填写内容 |
|---|---|
| 名称 | `lumeidc` |
| 启动命令 | `/www/wwwroot/lumeidc/lumeidc` |
| 运行目录 | `/www/wwwroot/lumeidc` |
| 运行用户 | `www` 或 `root` |
| 自动启动 | 开启 |
| 自动重启 | 开启 |
| 停止信号 | `TERM` |
| 日志目录 | `/www/wwwroot/lumeidc/logs` |

启动命令必须使用绝对路径：

```bash
/www/wwwroot/lumeidc/lumeidc
```

### 3. 创建日志目录

```bash
mkdir -p /www/wwwroot/lumeidc/logs
chown -R www:www /www/wwwroot/lumeidc
chmod 755 /www/wwwroot/lumeidc/lumeidc
```

如果使用 `root` 用户运行，则不需要执行 `chown`。

### 4. 手动测试二进制

在添加 Supervisor 前，可以先测试：

```bash
cd /www/wwwroot/lumeidc
./lumeidc
```

看到类似日志表示程序已启动：

```text
LumeIDC 未安装，安装向导已启动
```

按 `Ctrl+C` 停止后，再交给 Supervisor 管理。

### 5. 查看运行状态

```bash
supervisorctl status
```

正常状态类似：

```text
lumeidc    RUNNING
```

查看日志：

```bash
tail -f /www/wwwroot/lumeidc/logs/lumeidc.log
```

如果宝塔 Supervisor 使用了其他日志路径，以面板中显示的路径为准。

---

## 四、配置 Nginx 反向代理

无论使用「Go 项目」还是 Supervisor，都需要让域名转发到 LumeIDC 的 `8080` 端口。

### 方案 A：使用宝塔网站反向代理

进入：

```text
宝塔面板 → 网站 → yun.662662.xyz → 设置 → 反向代理
```

添加代理：

```text
代理名称：lumeidc
目标 URL：http://127.0.0.1:8080
发送域名：$host
```

保存并重载 Nginx。

### 方案 B：手动添加 Nginx 配置

创建配置文件：

```text
/www/server/panel/vhost/nginx/lumeidc.conf
```

内容：

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name yun.662662.xyz;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        proxy_read_timeout 60s;
    }
}
```

检查配置：

```bash
/www/server/nginx/sbin/nginx \
  -t \
  -c /www/server/nginx/conf/nginx.conf
```

重载宝塔 Nginx：

```bash
kill -HUP $(pgrep -xo nginx)
```

> 宝塔使用的是 `/www/server/nginx`，不要使用系统 Nginx 的 `/usr/sbin/nginx` 进行重载。

---

## 五、数据库准备

LumeIDC 使用 PostgreSQL。

如果数据库还没有创建，可以执行：

```bash
sudo -u postgres psql
```

在 PostgreSQL 中执行：

```sql
CREATE USER lumeidc WITH PASSWORD '修改为强密码';
CREATE DATABASE lumeidc OWNER lumeidc;
\q
```

如果用户或数据库已经存在，不要重复创建。

安装向导中的数据库信息：

```text
数据库地址：127.0.0.1
数据库端口：5432
数据库名：lumeidc
数据库用户：lumeidc
数据库密码：创建用户时设置的密码
```

---

## 六、访问安装向导

启动程序后访问：

```text
http://yun.662662.xyz/install
```

填写：

```text
数据库地址：127.0.0.1
数据库端口：5432
数据库名：lumeidc
数据库用户：lumeidc
数据库密码：PostgreSQL 用户密码
管理员用户名：自行设置
管理员密码：至少 8 位
```

站点地址无需填写，安装完成后在「后台 → 站点设置 → 站点地址」配置即可（留空自动推断）。
安装成功后，程序会生成：

```text
/www/wwwroot/lumeidc/config.yaml
```

然后访问：

```text
http://yun.662662.xyz/login
```

---

## 七、两种方式对比

| 项目 | Go 项目 | Supervisor |
|---|---|---|
| 是否支持二进制 | 取决于宝塔版本 | 支持 |
| 是否需要 Go | 不需要 | 不需要 |
| 配置难度 | 较简单 | 简单 |
| 兼容性 | 依赖面板实现 | 较好 |
| 推荐程度 | 可以先尝试 | 更推荐 |
| 适合场景 | 宝塔支持执行文件选择 | 所有宝塔版本 |

### 推荐选择

如果「Go 项目」页面可以选择：

```text
/www/wwwroot/lumeidc/lumeidc
```

可以直接使用「Go 项目」。

如果无法选择或启动后循环重启，使用：

```text
Supervisor 管理器
```

---

## 八、常见问题

### 1. Permission denied

检查权限：

```bash
chmod 755 /www/wwwroot/lumeidc/lumeidc
chown -R www:www /www/wwwroot/lumeidc
```

### 2. Exec format error

说明二进制架构不匹配。检查服务器架构：

```bash
uname -m
```

重新下载对应版本。

### 3. 访问 8080 正常，域名访问失败

检查：

```bash
curl -I http://127.0.0.1:8080/login
```

如果正常，检查 Nginx 反向代理配置和域名 DNS。

### 4. 访问域名显示 Nginx 默认页

检查：

- `server_name` 是否为正确域名
- 宝塔 Nginx 配置是否已经重载
- 是否存在其他站点抢占该域名
- DNS 是否解析到当前服务器

### 5. 重启后回到安装页面

通常是运行目录不可写，或 `config.yaml` 没有保存到持久化目录。

检查：

```bash
ls -l /www/wwwroot/lumeidc/config.yaml
```

### 6. 安装完成后页面 404

新版安装成功后是同进程自动切换，无需重启。若仍停留在 404（多为运行目录不可写、
`config.yaml` 未生成，先看第 5 条），确认目录正常后手动重启：

```bash
supervisorctl restart lumeidc
```

或者在宝塔「Go 项目」页面重启项目。

---

## 九、最终访问链路

```text
浏览器
  ↓
yun.662662.xyz:80
  ↓
宝塔 Nginx
  ↓
127.0.0.1:8080
  ↓
LumeIDC 二进制程序
  ↓
PostgreSQL
```

推荐最终结构：

```text
/www/wwwroot/lumeidc/
├── lumeidc
├── config.yaml
└── logs/
```

## 十、在线更新

后台自带的**系统更新**页可完成版本升级，无需下载、无需手动停进程：

1. 登录后台 → `系统设置 → 系统更新`
2. 「检查更新」查看最新版本与更新日志
3. 「立即更新」：程序自动下载、SHA256 校验、备份到 `.bak`、替换二进制
4. 确认弹窗后「确定」就地重启生效（重启后需重新登录）

手动重启用备选命令：

```bash
supervisorctl restart lumeidc
```

或宝塔 Go 项目面板重启。

> 在线更新要求运行目录（`/www/wwwroot/lumeidc`，Supervisor 的 process.d 需要）可写二进制；
> 换成源码 `go build` 运行的部署无法使用，请按第一节手动替换。

发布流程、版本号约定与回退方法见 [release.md](release.md)。