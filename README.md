# easy-docker-proxy

用一个代理加速多个 Docker 镜像仓库（Docker Hub、GHCR、Quay 等），**不缓存镜像层**，只记录谁拉了什么，并提供简单的统计网页。

适合：内网 / 机房需要统一加速入口、想看拉取统计、又不想部署带容器管理功能的重型面板。

## 能做什么

| 能力 | 说明 |
|------|------|
| 多仓库加速 | 按访问域名路由到不同上游（如 `hub.example.com` → Docker Hub） |
| 客户端无感 | 代理在服务端完成上游 Token 换发，一般不用在客户端配上游密码 |
| 零镜像缓存 | 只做流式转发，磁盘只存拉取元数据（SQLite） |
| 统计控制台 | 浏览器登录查看拉取量、流量、热门镜像、客户端、错误与事件 |
| 可选拉取鉴权 | 可要求 `docker login` 才能拉；账号与网页登录账号相互独立 |
| 安全边界 | **不**挂载 Docker socket，**不**管理容器，管理口默认不暴露公网 |

## 不能做什么

- 不做镜像层本地缓存 / 离线仓库  
- 不做容器创建、启停、日志、exec  
- 不做网页里改 YAML 配置（改配置请编辑文件后热重载或重启）  

## 怎么工作

```text
  docker pull hub.example.com/library/nginx:alpine
                 │
                 ▼
         ┌───────────────┐
         │ 数据面 :5000  │  ← 可对客户端 / 边缘 TLS 暴露
         │ 按 Host 转发  │
         └───────┬───────┘
                 ▼
         registry-1.docker.io 等上游

         ┌───────────────┐
         │ 管理面 :5001  │  ← 仅本机或内网：Stats / API / metrics
         └───────────────┘
```

- **数据面**：Registry V2 只读（GET/HEAD）  
- **管理面**：统计网页、账号管理、运维 API；生产环境不要映射到公网  

---

## 五分钟上手（Docker Compose，推荐）

### 1. 准备配置

```bash
git clone https://github.com/AlexWuYh/easy-docker-proxy.git
cd easy-docker-proxy

cp .env.example .env
# 编辑 .env，至少设置：
#   PROXY_ADMIN_TOKEN=$(openssl rand -hex 32)
#   PROXY_WEB_PASSWORD=你的网页登录密码   # 至少 8 位，首次启动创建管理员

cp configs/config.docker.yaml configs/config.yaml
# 编辑 configs/config.yaml：把 hub.example.com 等改成你的域名
```

### 2. 启动

```bash
docker compose -f deploy/docker-compose.yaml up -d --build
```

默认只发布 **数据面 5000**，**不**发布管理面 5001。

### 3. 让客户端认识域名

任选其一：

- 内网 DNS：把 `hub.example.com`、`ghcr.example.com` 等指到代理机器  
- 本机测试：在 `/etc/hosts` 写 `127.0.0.1 hub.example.com`  

（公网生产建议再加 TLS 边缘，见 [deploy/README.md](./deploy/README.md)。）

### 4. 拉取镜像

```bash
docker pull hub.example.com/library/nginx:alpine
docker pull ghcr.example.com/owner/image:tag
```

自检：

```bash
curl -H 'Host: hub.example.com' http://127.0.0.1:5000/v2/
# 期望：HTTP 200
```

### 5. 打开统计网页

临时在本机访问管理面时，在 `deploy/docker-compose.yaml` 中取消注释：

```yaml
ports:
  - "127.0.0.1:5001:5001"
```

然后浏览器打开：

```text
http://127.0.0.1:5001/stats/login.html
```

使用 `.env` 里的 `PROXY_WEB_USER` / `PROXY_WEB_PASSWORD` 登录。  
可看：总览、镜像列表、事件、账号（管理员可管理 Web 用户与 Docker 拉取账号）。

---

## 本地用 Go 跑（开发 / 试用）

需要 Go 1.22+。

```bash
cp configs/config.example.yaml configs/config.yaml
# macOS 上 :5000 常被 AirPlay 占用，可把 listen 改成 ":15000"

export PROXY_ADMIN_TOKEN="$(openssl rand -hex 32)"
export PROXY_WEB_USER=admin
export PROXY_WEB_PASSWORD='your-long-password'

go run ./cmd/proxy -config configs/config.yaml
```

| 端口 | 用途 |
|------|------|
| `listen`（默认 `:5000`） | 镜像拉取数据面 |
| `admin_listen`（默认 `127.0.0.1:5001`） | 统计与运维 |

可选：设置 `DOCKERHUB_USER` / `DOCKERHUB_TOKEN` 减轻 Docker Hub 匿名限流。

---

## 配置要点

完整字段见：

- [configs/config.example.yaml](./configs/config.example.yaml) — 本地示例  
- [configs/config.docker.yaml](./configs/config.docker.yaml) — 容器路径  
- [.env.example](./.env.example) — 密钥与引导账号  

| 你想… | 改什么 |
|--------|--------|
| 增加 / 修改加速域名 | `registries[].hosts` 与 DNS |
| 换上游地址 | `registries[].upstream` |
| 公网防滥用 | `access_control.mode: whitelist`，或开启下方拉取鉴权 |
| 禁止「随便 Host 都能当加速」 | `default: ""`（未知域名 → 404） |
| 网页管理员 | 环境变量 `PROXY_WEB_USER` / `PROXY_WEB_PASSWORD`（仅库为空时引导一次） |
| 运维 API / metrics 备用令牌 | `PROXY_ADMIN_TOKEN` |

密钥请用环境变量注入，**不要**写进仓库。

配置改完后：

```bash
# Compose：向进程发 SIGHUP，或
curl -X POST -H "Authorization: Bearer $PROXY_ADMIN_TOKEN" http://127.0.0.1:5001/-/reload
```

热重载会更新路由 / ACL / 限流 / 上游，**不会**重开数据库；改库路径需重启。  
`/-/config` 与 `/-/reload` 仅 **管理员** 或 ops token 可用（viewer 只读统计）。

---

## 可选：客户端拉取鉴权

默认 **匿名可拉**（`pull_auth.mode: off`）。若只想让有账号的人通过代理拉镜像：

```yaml
pull_auth:
  mode: required   # 或 optional：匿名可拉，但若带了密码则必须正确
  realm: easy-docker-proxy
```

创建拉取账号（与网页登录账号**不是**同一套）：

```bash
# 首次可在 .env 中设置（表为空且密码 ≥8 位时自动创建）
PROXY_PULL_USER=puller
PROXY_PULL_PASSWORD='your-pull-password'
```

或在网页 **账号 → Docker 拉取账号** 中管理。客户端：

```bash
docker login hub.example.com -u puller -p 'your-pull-password'
docker pull hub.example.com/library/nginx:alpine
```

客户端密码只用于访问**本代理**，不会转发给上游 Registry。

---

## 公网部署注意

1. **只暴露数据面**（或经 Caddy/Nginx 终结 TLS）；**不要**把 5001 绑到 `0.0.0.0` 公网。  
2. 建议 `access_control` 白名单，和/或 `pull_auth.mode: required`。  
3. 建议 `default: ""`，避免未知 Host 变成开放加速器。  
4. `trusted_proxies` 只填真实反代网段，否则客户端 IP / Host 可能被伪造。  

更完整的清单、Caddy 边缘、备份与指标说明见 **[deploy/README.md](./deploy/README.md)**。

---

## 常见问题

**拉不动 / 超时？**  
确认 DNS 或 hosts、边缘是否保留原始 `Host`、防火墙是否放行数据面端口。

**Hub 报限流？**  
配置 `DOCKERHUB_USER` / `DOCKERHUB_TOKEN`（或上游 PAT）。

**网页打不开 / 登不进？**  
确认已映射 `127.0.0.1:5001`、设置了 `PROXY_WEB_PASSWORD` 且是**首次**空库引导；密码至少 8 位。

**只想看统计、不能改配置？**  
正确：网页只做展示与账号管理，改代理规则请编辑 `config.yaml` 后重载。

**和 Docker-Proxy 什么关系？**  
数据面思路借鉴了 [Docker-Proxy](https://github.com/dqzboy/Docker-Proxy)，但本项目**不是**其 fork，也**不包含**容器管理能力。

---

## 文档与构建

```bash
# 构建
go build -o bin/proxy ./cmd/proxy

# 测试（macOS 建议关闭 CGO）
CGO_ENABLED=0 go test ./...
```

| 文档 | 内容 |
|------|------|
| [deploy/README.md](./deploy/README.md) | Compose、TLS 边缘、安全清单、热重载与备份 |
| [configs/config.example.yaml](./configs/config.example.yaml) | 配置项注释 |
| [LICENSE](./LICENSE) | MIT |

---

## 致谢

数据面设计借鉴了 [**dqzboy/Docker-Proxy**](https://github.com/dqzboy/Docker-Proxy) 的优秀实践：Host 路由、服务端 Token 换发、流式转发、数据面与管理面分离。感谢作者与贡献者。

## License

[MIT](./LICENSE)
