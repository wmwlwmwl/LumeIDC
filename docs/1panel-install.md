# LumeIDC 在 1Panel 中的二进制包安装流程

本文适用于使用预编译 LumeIDC 二进制包，在 1Panel 的「创建 Go 应用」页面部署。

二进制包已经编译完成，不需要安装 Go，也不需要执行 `go build`。

## 前置条件

- 已安装 1Panel，并进入「容器 → 创建 Go 应用」页面

- 已准备 LumeIDC 二进制包，且二进制架构与服务器匹配

- 已准备可用的 PostgreSQL 数据库（本机或远程均可）

- 已准备一个空数据库、数据库用户和密码

- 已将域名解析到服务器，例如：`yun.662662.xyz → 服务器IP`

检查服务器架构：

```bash
uname -m
```

常见架构：

```text
x86_64  → linux-amd64
aarch64 → linux-arm64
```

## 一、上传二进制包

建议在宿主机创建持久化目录：

```text
/opt/lumeidc
```

将二进制文件上传到：

```text
/opt/lumeidc/lumeidc
```

设置执行权限：

```bash
chmod 755 /opt/lumeidc/lumeidc
```

建议目录结构：

```text
/opt/lumeidc/
├── lumeidc
└── config.yaml
```

首次启动前可以没有 `config.yaml`，安装向导会自动生成；但运行目录必须是持久化目录。

## 二、在 1Panel 创建 Go 应用

进入：

```text
1Panel → 容器 → 创建 Go 应用
```

按照页面填写：

| 配置项  | 填写内容           | 说明          |
| ---- | -------------- | ----------- |
| 名称   | `lumeidc`      | 可自定义        |
| 应用   | `Go`           | 选择 Go       |
| 版本   | `1.26`         | 按面板实际可用版本选择 |
| 运行目录 | `/opt/lumeidc` | 容器内工作目录     |
| 启动命令 | `./lumeidc`    | 直接运行二进制     |
| 容器名称 | `lumeidc`      | 可自定义，建议保持一致 |

> 页面中的 Go 版本主要用于选择运行环境。使用二进制包时不会重新编译源码。

### 环境变量

安装向导默认监听所有网卡的 `:8080`（全接口），容器端口映射可直接转发，**无需设置环境变量**即可从公网访问安装页。

> 若想给安装阶段改用其它端口（如 `18090`），可在此添加环境变量 `INSTALL_LISTEN=:18090`；
> 安装完成重启后正式程序读取 `config.yaml` 中的 `listen: ":8080"`，该变量不再需要（保留也无副作用）。

### 启动命令

二进制包必须填写：

```bash
./lumeidc
```

不要填写：

```bash
go build -o lumeidc ./cmd/lumeidc
```

也不要只填写：

```bash
go build
```

因为二进制包已经编译完成，启动命令需要实际运行程序。

### 端口设置

在「端口」标签中添加：

```text
容器端口：8080
主机端口：8080
协议：TCP
```

如果后续只通过 1Panel 的网站反向代理访问，也可以不把 8080 暴露到公网，但必须保证 Nginx 或 1Panel 代理可以访问应用端口。

### 挂载设置

在「挂载」标签中添加：

```text
宿主机路径：/opt/lumeidc
容器内路径：/opt/lumeidc
读写权限：读写
```

这样安装后生成的 `config.yaml` 会保存在宿主机：

```text
/opt/lumeidc/config.yaml
```

容器重建或重启后不会丢失。

> 运行目录和挂载后的容器目录必须一致。若运行目录填写 `/opt/lumeidc`，挂载目标也应填写 `/opt/lumeidc`。

## 三、首次启动和安装向导

创建并启动应用后，打开（安装向导默认监听 :8080 全接口，无需额外配置；打不开时见第 7 节排查）：

```text
http://服务器IP:8080/install
```

如果已经配置域名反向代理，也可以打开：

```text
http://你的域名/install
```

安装向导中填写：

```text
数据库地址：127.0.0.1
数据库端口：5432
数据库名：lumeidc
数据库用户：lumeidc
数据库密码：你的 PostgreSQL 密码
```

如果 PostgreSQL 在另一台服务器：

```text
数据库地址：远程 PostgreSQL 的域名或 IP
数据库端口：5432
```

同时设置：

- 管理员用户名

- 管理员密码（至少 8 位）

> 站点地址无需在安装时填写：安装完成后在「后台 → 站点设置 → 站点地址」配置，
> 留空则按用户访问的域名自动推断（支付回调、实名认证回调、邮件链接均以此为前缀）。

提交成功后，程序会执行数据库迁移并创建管理员账号，然后**自动切换为完整应用**，无需手动重启。等待几秒后浏览器会收到完成页，点击链接直接访问：

```text
http://你的域名/login
```

或：

```text
http://服务器IP:8080/login
```

## 五、配置域名反向代理

建议使用 1Panel 网站功能代理到 LumeIDC。

进入：

```text
1Panel → 网站 → 创建网站
```

绑定域名：

```text
yun.662662.xyz
```

然后配置反向代理，目标地址填写：

```text
http://127.0.0.1:8080
```

访问链路：

```text
浏览器
  ↓
yun.662662.xyz:80
  ↓
1Panel / Nginx
  ↓
127.0.0.1:8080
  ↓
LumeIDC 二进制程序
  ↓
PostgreSQL
```

如果 1Panel 与 LumeIDC 在不同容器中，`127.0.0.1` 可能指向代理容器自身。此时应填写 LumeIDC 容器名称或同一网络中的服务地址，例如：

```text
http://lumeidc:8080
```

具体地址以 1Panel 的容器网络配置为准。

## 六、数据库准备

如果 PostgreSQL 尚未创建数据库，可以执行：

```bash
sudo -u postgres psql
```

执行：

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

## 七、常见问题排查

### 1. 容器启动后立即退出

检查启动命令是否为：

```bash
./lumeidc
```

不要填写只有编译动作的命令：

```bash
go build
```

查看日志：

```bash
docker logs --tail 100 lumeidc
```

### 2. `exec format error`

说明二进制架构与服务器架构不匹配。

检查：

```bash
uname -m
file /opt/lumeidc/lumeidc
```

重新下载对应架构的二进制包。

### 3. `permission denied`

设置执行权限：

```bash
chmod 755 /opt/lumeidc/lumeidc
```

如果容器以非 root 用户运行，还要确保目录可读写：

```bash
chown -R 1000:1000 /opt/lumeidc
```

具体 UID 以 1Panel 应用运行用户为准。

### 4. 8080 端口打不开

安装向导默认监听全接口 `:8080`；若打不开，先查容器状态与日志，再按下面步骤检查端口监听与安全组。

检查容器状态：

```bash
docker ps
```

查看日志：

```bash
docker logs --tail 100 lumeidc
```

检查端口监听：

```bash
ss -ltnp | grep 8080
```

同时检查：

- 服务器防火墙

- 云服务器安全组

- 1Panel 端口映射

- 应用是否正常运行

### 5. 域名显示 Nginx 默认页

检查：

- DNS 是否解析到当前服务器

- 1Panel 网站是否绑定了正确域名

- 反向代理目标是否为 `http://127.0.0.1:8080`

- 是否存在其他网站抢占该域名

- Nginx 配置是否已重新加载

### 6. 直接访问 8080 正常，域名访问失败

先测试应用：

```bash
curl -I http://127.0.0.1:8080/login
```

如果返回 `200`，说明 LumeIDC 正常，问题通常在反向代理或域名配置。

### 7. 安装完成后仍然显示 404

新版安装成功后是同进程自动切换，无需重启。若仍停留在 404，说明自动切换失败
（如运行目录不可写导致 `config.yaml` 缺失），先看第 8 条；确认挂载正常后手动重启：

```bash
docker restart lumeidc
```

### 8. 重启后又回到安装向导

检查挂载是否正确：

```bash
ls -l /opt/lumeidc/config.yaml
```

如果文件不存在，说明运行目录没有持久化，或者宿主机目录与容器目录挂载错误。

确认：

```text
宿主机：/opt/lumeidc
容器内：/opt/lumeidc
```

并且运行目录也是：

```text
/opt/lumeidc
```

### 9. 远程数据库连接失败

不要填写：

```text
localhost
```

容器中的 `localhost` 指向容器自身。

应填写：

- PostgreSQL 服务器内网 IP

- PostgreSQL 服务器公网 IP

- PostgreSQL 域名

同时检查 PostgreSQL 的监听地址、防火墙和 `pg_hba.conf`。

## 八、升级二进制包

升级前备份配置：

```bash
cp -a /opt/lumeidc/config.yaml /opt/lumeidc/config.yaml.bak
```

停止应用：

```bash
docker stop lumeidc
```

替换二进制文件：

```bash
cp lumeidc /opt/lumeidc/lumeidc
chmod 755 /opt/lumeidc/lumeidc
```

重新启动：

```bash
docker start lumeidc
```

如果使用 1Panel 管理，建议直接在面板中重启应用。

## 九、最终目录结构

```text
/opt/lumeidc/
├── lumeidc
└── config.yaml
```

二进制部署的最终流程：

```text
上传二进制文件
→ 设置执行权限
→ 1Panel 创建 Go 应用
→ 启动命令填写 ./lumeidc
→ 挂载持久化目录
→ 映射 8080 端口
→ 打开 /install
→ 初始化 PostgreSQL 和管理员
→ 自动切换为完整应用
→ 配置域名反向代理
→ 访问 /login
```

