# 部署指南 — easy-docker-proxy

> **首次安装与逐项配置**请先读 **[docs/install.md](../docs/install.md)**。  
> 本文侧重架构、安全清单与运维（重载 / 备份）。

## 架构

```text
客户端
  · mirrors:  docker pull nginx                 ──┐
  · 路径前缀: docker pull <你的域名>/ghcr.io/…   ┤
                                                  ▼
              Caddy https://<你的域名>  →  127.0.0.1:5000  proxy
管理面 :5001 默认不映射；勿 DNS 劫持 ghcr.io（TLS 易失败）
```

> 文档中的 `reg.a.c` / `reg.example.com` 为**示例占位**。域名需自行申请，并添加 A/AAAA 或 CNAME 解析到代理主机，再在 Caddy 与 `config.yaml` 的 `hosts` 中填写真实主机名。

- **不**挂载 `docker.sock`；镜像层不落盘  
- TLS：**优先已有 Caddy** 反代到 **5000**；compose `edge` 仅备选（8080/8443）

## 快速部署（VPS）

### 1. 准备密钥与配置

```bash
cd /path/to/easy-docker-proxy
cp .env.example .env
# 生成强 token
echo "PROXY_ADMIN_TOKEN=$(openssl rand -hex 32)" >> .env

cp configs/config.docker.yaml configs/config.yaml
# 确认 hosts 为官方 Registry 主机名，和/或 registry-mirrors 使用的代理地址
# 公网环境建议 access_control.mode: whitelist
```

### 2. 仅数据面

```bash
docker compose -f deploy/docker-compose.yaml up -d --build
curl -H 'Host: registry-1.docker.io' http://127.0.0.1:5000/v2/
```

### 3. 客户端：单域名混合模式

```bash
# A. Hub（mirrors → https://你的域名）
docker pull nginx:alpine

# B. 其它上游（路径前缀；将 reg.example.com 换成真实域名）
docker pull reg.example.com/ghcr.io/owner/app:tag
docker pull reg.example.com/docker.io/library/nginx:latest
```

**不要**把 `ghcr.io` 等写进 `/etc/hosts` 指到代理（证书对不上）。

mirrors 示例（域名须已解析）：

```json
{ "registry-mirrors": ["https://reg.example.com"] }
```

上游凭据：`registries[].auth`。详见 [README.md](../README.md)、[docs/install.md](../docs/install.md)。

### 4. 使用已有 Caddy（推荐）

**真实域名** → 数据面 **5000**；**不要** `compose --profile edge`（除非没有现成 Caddy）。

```bash
docker compose -f deploy/docker-compose.yaml up -d --build
curl -sS -o /dev/null -w '%{http_code}\n' -H 'Host: reg.example.com' http://127.0.0.1:5000/v2/
```

`trusted_proxies` 含 Caddy 出口（同机保留 `127.0.0.1/32`）。

完整片段：[caddy.external.example](./caddy.external.example)（站点名改成你的域名）。

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

| 项 | 说明 |
|----|------|
| 反代目标 | **5000**（数据面），不是 5001 |
| Host | 保留 `{host}`，与 config `hosts` / 默认路由一致 |
| 超时 | blob 用 `read_timeout 0` |
| 证书 | 只给**你的入口域名**配证，无需 ghcr.io 证书 |

可选自带 Caddy：`--profile edge`（默认宿主机 8080/8443）。

### 5. Stats / Metrics（仅本机或内网）

默认 **不** 发布 5001。临时调试可在 compose 中打开：

```yaml
ports:
  - "127.0.0.1:5001:5001"
```

```bash
# .env 中配置 PROXY_WEB_PASSWORD（及可选 PROXY_WEB_USER）后启动
open "http://127.0.0.1:5001/stats/login.html"
# 浏览器登录 Web 控制台；拉取账号在「账号」页单独管理

# API 也可用 PROXY_ADMIN_TOKEN：
export TOKEN=$(grep PROXY_ADMIN_TOKEN .env | cut -d= -f2-)
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:5001/api/v1/summary?range=7d
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:5001/metrics
```

### 拉取鉴权（可选）

默认 `pull_auth.mode: off`（匿名 pull）。若需 `docker login`：

1. 配置 `pull_auth.mode: optional` 或 `required`  
2. 用 `PROXY_PULL_PASSWORD` 引导首个拉取账号，或在控制台「账号 → Docker 拉取账号」创建  
3. `docker login <proxy-host> -u puller -p '...'`

## 安全清单

| 项 | 建议 |
|----|------|
| Admin 端口 | 不对 `0.0.0.0` 发布；仅 loopback / VPN / 内网反代 |
| `PROXY_ADMIN_TOKEN` | 随机 ≥32 字节；勿写入 git |
| `trusted_proxies` | 仅包含真实边缘（Caddy/Nginx）网段 |
| `access_control` | 公网建议 `whitelist`（或开启 `pull_auth.required`） |
| `default` | 公网建议留空：未知 Host → 404；勿与 `ACL off` 叠加成开放加速器 |
| `upstream_allowlist` | 默认开启；自定义上游需加入 hosts |
| `rate_limit` | 默认开启 |
| `/-/config` / `/-/reload` | 仅 **admin** 会话或 `PROXY_ADMIN_TOKEN`；viewer 无权限 |
| docker.sock | **禁止**挂载 |

### trusted_proxies 说明

仅当连接 peer IP 落在 `trusted_proxies` 时，才会信任：

- `X-Forwarded-For` / `X-Real-IP`（客户端 IP）
- `X-Forwarded-Host`（Host 路由）

错误配置会导致 IP ACL 失真或 Host 被伪造。

### upstream_allowlist 说明

加载配置时校验每个 `registries[].upstream` 的 hostname。  
默认允许 Docker Hub / GHCR / Quay / k8s / MCR / NVCR 等常见公共 registry。  
私有仓库：把 hostname 写入 `upstream_allowlist.hosts`，或（不推荐）`enabled: false`。

## 热重载

修改挂载的 `config.yaml` 后：

```bash
docker kill -s HUP $(docker compose -f deploy/docker-compose.yaml ps -q proxy)
# 或（需 admin：PROXY_ADMIN_TOKEN 或 web admin 会话）
curl -X POST -H "Authorization: Bearer $TOKEN" http://127.0.0.1:5001/-/reload
```

热重载范围：路由 / ACL / 限流 / 上游与 token 缓存。  
**不会**重开 SQLite；改 `storage.dsn` 或账号库路径需重启容器。

## 备份

只需备份 volume `proxy-data`（或 `/data/proxy.db`）与 `config.yaml` / `.env`（密钥）。

## 健康检查

镜像内置 `HEALTHCHECK`：请求容器内 `http://127.0.0.1:5000/v2/`。
