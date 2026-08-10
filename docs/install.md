# 安装与配置指南

本文是 **easy-docker-proxy** 的完整安装、配置与验收说明。  
产品简介与命令速查见根目录 [README.md](../README.md)；生产运维清单见 [deploy/README.md](../deploy/README.md)。

---

## 1. 你将得到什么

| 组件 | 默认 | 说明 |
|------|------|------|
| 数据面 | 主机 **:5000** | Registry 代理，客户端 / Caddy 连这里 |
| 管理面 | 容器内 **:5001**，默认**不**映射公网 | 统计网页、API、热重载 |
| 数据 | Docker volume `proxy-data` | 仅 SQLite 元数据，**无镜像层** |

**推荐接入模型（单域名混合）**

1. 对外**你自己的**域名（文档里写的 `reg.a.c` / `reg.example.com` **只是占位示例**）  
2. Docker Hub：客户端 `registry-mirrors` → 该域名，`docker pull nginx`  
3. 其它上游：`docker pull <你的域名>/ghcr.io/owner/app:tag`（路径前缀）  
4. **不要**用 `/etc/hosts` 劫持 `ghcr.io` 等官方域名（易 TLS 失败）

### 域名与 DNS（必做）

文档与配置模板中的域名均为**假想示例**，不会自动生效。你需要：

| 步骤 | 说明 |
|------|------|
| 1. 申请域名 | 在任意域名注册商购买/使用自有域名，例如 `reg.example.com` |
| 2. 添加解析 | A/AAAA 指到代理公网 IP，或 CNAME 到已有主机名；内网可用内网 DNS |
| 3. 写入配置 | `config.yaml` 里 dockerhub 的 `hosts` 填**同一主机名**（不要带 `https://`） |
| 4. 边缘 TLS | 在 Caddy/Nginx 中为该域名申请证书并反代到本机 `:5000` |

解析未生效前，客户端 `registry-mirrors` / `docker pull <域名>/...` 会失败。可用 `ping` / `dig` 确认域名已指向代理机。

---

## 2. 环境要求

- 64 位 Linux 主机（或本机 Docker Desktop）  
- Docker + Docker Compose v2  
- 出站 HTTPS（访问 Hub / GHCR 等上游）  
- （推荐）**自有域名** + 已有 Caddy/Nginx 做 HTTPS；没有也可用 IP:5000 + HTTP 试用  

可选：Go 1.22+（仅源码运行 / 开发）。

---

## 3. 安装（Docker Compose）

### 3.1 获取代码

```bash
git clone https://github.com/AlexWuYh/easy-docker-proxy.git
cd easy-docker-proxy
```

### 3.2 环境变量

```bash
cp .env.example .env
```

编辑 `.env`（**至少**）：

```bash
# 运维 API / metrics（建议随机 ≥32 字节）
PROXY_ADMIN_TOKEN=$(openssl rand -hex 32)

# 统计网页首次管理员（密码 ≥ 8 位；仅库为空时创建一次）
PROXY_WEB_USER=admin
PROXY_WEB_PASSWORD=请换成足够长的密码
```

可选（按需填写，空 = 对该上游公共匿名拉取）：

| 变量 | 用途 |
|------|------|
| `DOCKERHUB_USER` / `DOCKERHUB_TOKEN` | Docker Hub 限流 / 私有 |
| `GITHUB_USER` / `GITHUB_TOKEN` | GHCR（PAT 需 `read:packages`） |
| `QUAY_USER` / `QUAY_TOKEN` | Quay |
| `GCR_USER` / `GCR_TOKEN` | GCR |
| `NVCR_USER` / `NVCR_TOKEN` | NGC（用户名常为 `$oauthtoken`） |
| `ELASTIC_USER` / `ELASTIC_TOKEN` | Elastic 镜像 |
| `PROXY_PULL_USER` / `PROXY_PULL_PASSWORD` | 客户端 `docker login` 本代理（`pull_auth`） |
| `EDGE_HTTP_PORT` / `EDGE_HTTPS_PORT` | 仅使用 compose 自带 Caddy 时（默认 8080/8443） |

完整清单与注释见仓库根目录 [.env.example](../.env.example)。

### 3.3 业务配置

```bash
cp configs/config.docker.yaml configs/config.yaml
```

**必改一项**：把**你已申请并完成解析**的入口主机名写进 dockerhub 的 `hosts`（与 Caddy 站点名一致；模板里的 `reg.example.com` 请整段替换）：

```yaml
registries:
  - name: dockerhub
    hosts:
      - "reg.example.com"    # ← 换成真实域名；勿带 https://
    path_prefixes:
      - "docker.io"
    # ...
```

其它上游一般只需 `path_prefixes`，不必再写 `hosts`。

### 3.4 启动

在仓库**根目录**执行：

```bash
docker compose -f deploy/docker-compose.yaml up -d --build
```

查看日志：

```bash
docker compose -f deploy/docker-compose.yaml logs -f proxy
```

### 3.5 自检

```bash
# 数据面握手（Host 用你的入口域名）
curl -sS -o /dev/null -w '%{http_code}\n' \
  -H 'Host: reg.example.com' http://127.0.0.1:5000/v2/
# Host 换成你的真实域名；期望：200
```

---

## 4. HTTPS / Caddy

### 4.1 使用已有 Caddy（推荐）

只跑本仓库的 **proxy**，**不要** `compose --profile edge`。

在现有 Caddyfile 中增加（完整可复制文件：[deploy/caddy.external.example](../deploy/caddy.external.example)）。  
将下面的 `reg.example.com` 换成**你的真实域名**（须已解析到本机）：

```caddy
reg.example.com {
	reverse_proxy 127.0.0.1:5000 {
		header_up Host {host}
		header_up X-Forwarded-Host {host}
		header_up X-Forwarded-Proto {scheme}
		header_up X-Real-IP {remote_host}
		header_up X-Forwarded-For {remote_host}
		transport http {
			read_timeout 0
			write_timeout 0
		}
	}
}
```

要点：

| 项 | 说明 |
|----|------|
| 反代目标 | **5000**（数据面），不是 5001 |
| Host | 保留客户端 Host，与 `hosts` / 默认路由一致 |
| 超时 | 大层传输建议 `read_timeout 0` |
| 证书 | 只给**你的入口域名**配证即可（Caddy 自动 HTTPS 或自备证书） |
| `trusted_proxies` | 同机需含 `127.0.0.1/32`（示例配置已有） |

重载 Caddy 后，客户端 mirrors 使用 `https://<你的域名>`。

### 4.2 无现成边缘时（可选）

```bash
docker compose -f deploy/docker-compose.yaml --profile edge up -d --build
```

默认映射 **8080/8443**（避免占 80/443）。配置见 [deploy/Caddyfile](../deploy/Caddyfile)。

### 4.3 仅 HTTP 试用（内网）

不配 TLS 时，客户端：

```json
{
  "registry-mirrors": ["http://10.0.0.8:5000"],
  "insecure-registries": ["10.0.0.8:5000"]
}
```

路径前缀示例：`docker pull 10.0.0.8:5000/ghcr.io/owner/app:tag`（需 insecure）。

---

## 5. 客户端配置

下列命令中的 `reg.example.com` 均请换成你的真实域名（与 DNS、`hosts`、Caddy 一致）。

### 5.1 Docker Hub（mirrors）

```json
{
  "registry-mirrors": ["https://reg.example.com"]
}
```

```bash
sudo systemctl restart docker   # Linux
docker pull nginx:alpine
```

服务端 `default: dockerhub` 时，无路径前缀的拉取走 Hub。

### 5.2 路径前缀（多上游）

格式：`<你的入口主机>/<上游主机名>/<命名空间>/...`

```bash
docker pull reg.example.com/ghcr.io/owner/app:tag
docker pull reg.example.com/quay.io/prometheus/prometheus:latest
docker pull reg.example.com/docker.io/library/nginx:latest
docker pull reg.example.com/registry.k8s.io/pause:3.9
```

| 路径前缀 | 上游 name（配置） |
|----------|-------------------|
| （无前缀 / mirrors） | `default` → 通常 dockerhub |
| `docker.io` | dockerhub |
| `ghcr.io` | ghcr |
| `quay.io` | quay |
| `gcr.io` | gcr |
| `registry.k8s.io` / `k8s.gcr.io` | k8s |
| `mcr.microsoft.com` | mcr |
| `nvcr.io` | nvcr |
| `docker.elastic.co` | elastic |
| `public.ecr.aws` | ecr-public |

### 5.3 可选：强制 login 本代理

`config.yaml`：

```yaml
pull_auth:
  mode: required   # off | optional | required
```

`.env` 首次引导（表空且密码 ≥8）：

```bash
PROXY_PULL_USER=puller
PROXY_PULL_PASSWORD=your-pull-password
```

```bash
docker login reg.example.com -u puller -p 'your-pull-password'
```

与网页账号、上游 Hub/GHCR 密码**不是**同一套。也可在统计页「账号 → Docker 拉取账号」管理。

---

## 6. 配置文件说明（`config.yaml`）

模板：

- 容器：[configs/config.docker.yaml](../configs/config.docker.yaml)  
- 本地 Go：[configs/config.example.yaml](../configs/config.example.yaml)  

密钥统一用 `${ENV}`，由 compose 注入或 shell `export`。

### 6.1 `server`

| 字段 | 默认 | 说明 |
|------|------|------|
| `listen` | `:5000` | 数据面 |
| `admin_listen` | 容器 `0.0.0.0:5001` / 本地 `127.0.0.1:5001` | 管理面 |
| `write_timeout` | `0` | 大层流式建议 0 |

### 6.2 `default`

无 `path_prefixes` 命中时的上游 **name**。  
mirrors 场景请保持 `dockerhub`；设为 `""` 则未匹配前缀且未命中 Host 时 404。

### 6.3 `registries[]`（核心）

| 字段 | 说明 |
|------|------|
| `name` | 内部名；统计页「上游」显示此值 |
| `enabled` | `false` 关闭该上游 |
| `hosts` | 可选；统一入口域名写在 **dockerhub** 上即可 |
| `path_prefixes` | 客户端路径前缀，如 `ghcr.io` |
| `upstream` | 真实 Registry 根 URL（https） |
| `auth.type` | `token` / `anonymous` / `basic` |
| `auth.username` / `password` | 代理访问上游用；可用 `${VAR}` |
| `token_cache_ttl` | 上游 Bearer 缓存秒数 |
| `insecure_skip_verify` | 仅调试上游自签证书 |

**鉴权类型**

| type | 何时用 |
|------|--------|
| `token` | Hub / GHCR / Quay / NGC 等（最常见）；账号空 = 公共匿名 |
| `anonymous` | 明确不向上游带凭据 |
| `basic` | 少数私有仓 HTTP Basic |

### 6.4 安全相关

| 段 | 说明 |
|----|------|
| `access_control` | `off` / `whitelist` / `blacklist`；公网建议 whitelist |
| `trusted_proxies` | 仅信任的反代网段才解析 XFF / X-Forwarded-Host |
| `upstream_allowlist` | 限制可配置的上游主机名（防 SSRF） |
| `rate_limit` | 按客户端 IP 限流 |
| `pull_auth` | 客户端拉本代理是否要 Basic |

### 6.5 存储与管理

| 段 | 说明 |
|----|------|
| `storage.dsn` | SQLite 路径（容器内默认 `/data/proxy.db`） |
| `admin.token_env` | 默认读 `PROXY_ADMIN_TOKEN` |
| `metrics.enabled` | 管理面 `/metrics`（需鉴权） |

### 6.6 热重载

修改挂载的 `config.yaml` 后：

```bash
# 需先映射 127.0.0.1:5001 或在容器网络内执行
curl -X POST -H "Authorization: Bearer $PROXY_ADMIN_TOKEN" \
  http://127.0.0.1:5001/-/reload
```

或：

```bash
docker kill -s HUP $(docker compose -f deploy/docker-compose.yaml ps -q proxy)
```

重载范围：路由 / ACL / 限流 / 上游与 Token 缓存。  
**不**重开数据库；改 DSN 需重启容器。  
`/-/config`、`/-/reload` 仅 **admin** 或 ops token（**不支持** `?token=` 查询参数）。

---

## 7. 统计网页

### 7.1 打开端口（本机调试）

`deploy/docker-compose.yaml` 中取消注释：

```yaml
ports:
  - "127.0.0.1:5001:5001"
```

然后：

```text
http://127.0.0.1:5001/stats/login.html
```

### 7.2 功能

| 页面 | 内容 |
|------|------|
| 分析看板 | 总览 KPI、趋势；**上游仓库**占比 / 下钻 |
| 镜像列表 | 按上游筛选、关键词搜索 |
| 事件 | 最近成功/失败 |
| 账号 | Web 用户；Docker 拉取账号（admin） |

---

## 8. 源码运行（开发）

```bash
cp configs/config.example.yaml configs/config.yaml
export PROXY_ADMIN_TOKEN="$(openssl rand -hex 32)"
export PROXY_WEB_PASSWORD='your-long-password'
# macOS 若 5000 被占用，改 config 中 listen: ":15000"
go run ./cmd/proxy -config configs/config.yaml

CGO_ENABLED=0 go test ./...
```

---

## 9. 验收清单

- [ ] 域名已申请，DNS 已解析到代理机  
- [ ] `config.yaml` `hosts` 与 Caddy 站点名均为该真实域名  
- [ ] `curl -H 'Host: <你的域名>' http://127.0.0.1:5000/v2/` → 200  
- [ ] mirrors 配置后 `docker pull nginx:alpine` 成功  
- [ ] `docker pull <你的域名>/ghcr.io/...` 成功（或可接受的上游错误，非代理 404）  
- [ ] 统计页可登录；看板「上游」有数据  
- [ ] 未把 5001 暴露到公网  
- [ ] 生产已设强 `PROXY_ADMIN_TOKEN` / `PROXY_WEB_PASSWORD`  

---

## 10. 常见问题

**拉镜像 TLS 错误？**  
检查是否错误劫持了 `ghcr.io` 的 DNS/hosts；应只访问**你自己申请并解析**的入口域名。

**mirrors 后仍 404 unknown registry？**  
确认 `default: dockerhub`，或把 mirrors 使用的 Host 写入某条 `hosts`。

**路径前缀仍走错上游？**  
确认 `path_prefixes` 与镜像路径第一段一致（如 `ghcr.io`），改配置后 reload。

**Hub 限流？**  
配置 `DOCKERHUB_USER` + Access Token。

**网页无法登录？**  
首次需设置 `PROXY_WEB_PASSWORD` 且数据库尚无用户；密码 ≥8 位。

**想加私有仓？**  
在 `registries` 增加一项，填 `path_prefixes` / `upstream` / `auth`，必要时把上游 host 加入 `upstream_allowlist.hosts`。

---

## 11. 相关文件索引

| 路径 | 用途 |
|------|------|
| [README.md](../README.md) | 产品简介与速查 |
| [docs/install.md](./install.md) | **本文：安装与配置** |
| [deploy/README.md](../deploy/README.md) | 部署架构、安全清单、备份 |
| [deploy/caddy.external.example](../deploy/caddy.external.example) | 已有 Caddy 片段 |
| [configs/config.docker.yaml](../configs/config.docker.yaml) | 容器配置模板 |
| [configs/config.example.yaml](../configs/config.example.yaml) | 本地配置模板 |
| [.env.example](../.env.example) | 环境变量模板 |
