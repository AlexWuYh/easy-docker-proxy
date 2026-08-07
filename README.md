# easy-docker-proxy

轻量、安全的 **多上游 Docker Registry 加速代理**。

- **零镜像缓存**：manifest / blob 流式转发，不落盘
- **只记拉取记录**：异步写入元数据与日聚合，便于统计
- **简单 Stats 页**：只读看板（Pulls、流量、热门镜像、客户端、错误）
- **安全默认**：无 Docker socket、无容器管理、管理面本机监听 + 强 Token

> 状态：**M0–M4 已完成**（数据面 + 记录 + Stats + 部署硬化）。可选增强见 M5。

## 功能规划

| 阶段 | 状态 | 内容 |
|------|------|------|
| M1 | done | Host 路由、上游 Token 换发、流式代理、ACL、限流、热重载 |
| M2 | done | 拉取事件 + SQLite 日聚合（异步、不阻塞 pull） |
| M3 | done | Stats Web（鉴权只读看板） |
| M4 | done | 部署硬化、upstream allowlist、可选 Prometheus `/metrics` |

## 快速开始

### 依赖

- Go 1.22+
- （可选）Docker / Docker Compose

### 本地运行

```bash
cp configs/config.example.yaml configs/config.yaml
# 建议按域名修改 registries[].hosts；macOS 上 :5000 常被 AirPlay 占用，可改 listen
export PROXY_ADMIN_TOKEN="$(openssl rand -hex 32)"
# 可选：降低 Docker Hub 限流
# export DOCKERHUB_USER=...
# export DOCKERHUB_TOKEN=...

go run ./cmd/proxy -config configs/config.yaml
```

数据面默认 `:5000`，管理面默认 `127.0.0.1:5001`。  
拉取元数据写入 `storage.dsn`（默认 `data/proxy.db`），**不**缓存镜像层。

```bash
# API 版本握手
curl -H 'Host: hub.example.com' http://127.0.0.1:5000/v2/

# 管理面写操作 / 配置查看（需 PROXY_ADMIN_TOKEN 或 web admin 会话；viewer 不可）
curl -H "Authorization: Bearer $PROXY_ADMIN_TOKEN" http://127.0.0.1:5001/-/config
curl -X POST -H "Authorization: Bearer $PROXY_ADMIN_TOKEN" http://127.0.0.1:5001/-/reload

# Stats 控制台（浏览器登录）
# 首次启动需设置：
#   export PROXY_WEB_USER=admin
#   export PROXY_WEB_PASSWORD='your-long-password'
open "http://127.0.0.1:5001/stats/login.html"

# Stats API（登录后使用 session token，或继续用 PROXY_ADMIN_TOKEN）
curl -X POST http://127.0.0.1:5001/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"your-long-password"}'
```

### 客户端拉取鉴权（可选）

默认 **`pull_auth.mode: off`**（与现在一样可匿名 pull）。  
拉取账号与 Web 控制台账号 **相互独立**（表 `pull_users`）。

```yaml
# configs/config.yaml
pull_auth:
  mode: optional   # off | optional | required
  realm: easy-docker-proxy
```

```bash
# 首次可引导创建拉取账号（仅表为空时）
export PROXY_PULL_USER=puller
export PROXY_PULL_PASSWORD='your-pull-password'

# 或在 Web「账号」页（管理员）创建拉取账号后：
docker login hub.example.com -u puller -p 'your-pull-password'
docker pull hub.example.com/library/nginx:alpine
```

| mode | 行为 |
|------|------|
| `off` | 不要求凭证（默认） |
| `optional` | 可不登录；若带了 Basic 则必须正确 |
| `required` | 必须 `docker login` 后才能拉 |

客户端密码 **不会** 转发到上游 Registry。

### 配置

示例见 [`configs/config.example.yaml`](./configs/config.example.yaml)。  
密钥请用环境变量注入，勿提交真实密码。

公网暴露数据面时建议：

1. `access_control.mode: whitelist`（或 `pull_auth.mode: required`）  
2. `default: ""`（未知 Host 直接 404，避免成为开放 pull-through）  
3. 不要把 `admin_listen` / 5001 映射到公网

### Docker Compose

详见 [`deploy/README.md`](./deploy/README.md)。

```bash
cp .env.example .env          # 设置强 PROXY_ADMIN_TOKEN
cp configs/config.docker.yaml configs/config.yaml
# 编辑 hosts / 域名；公网建议开启 access_control whitelist

docker compose -f deploy/docker-compose.yaml up -d --build

# 可选：TLS 边缘（需 DNS + 编辑 deploy/Caddyfile）
docker compose -f deploy/docker-compose.yaml --profile edge up -d
```

**注意**：compose 默认只发布 **5000**（数据面），**不**发布管理面 5001。

## 客户端用法

前置：DNS 将 `hub.example.com` 等指到代理（或本机 + `/etc/hosts`），边缘 TLS 反代保留正确 `Host`。

```bash
docker pull hub.example.com/library/nginx:alpine
docker pull ghcr.example.com/owner/image:tag
```

## 项目结构

```text
cmd/proxy/     # 入口
internal/      # 业务包（proxy / store / stats / …）
configs/       # 示例与 Docker 配置
deploy/        # Dockerfile / compose / Caddy / 部署说明
internal/web/  # embed Stats 前端
```

## 文档

| 文档 | 说明 |
|------|------|
| [`deploy/README.md`](./deploy/README.md) | 部署、安全清单、trusted proxies / allowlist |
| [`configs/config.example.yaml`](./configs/config.example.yaml) | 配置示例 |
| [`configs/config.docker.yaml`](./configs/config.docker.yaml) | 容器部署用配置 |

## 致谢 / Acknowledgments

本项目在 **数据面设计** 上借鉴了 [**dqzboy/Docker-Proxy**](https://github.com/dqzboy/Docker-Proxy) 的优秀思路，包括但不限于：

- 按请求 `Host` 路由到多上游 Registry
- 服务端完成 Token 换发，客户端无感
- 流式转发、不落盘
- 数据面与管理面端口分离

**项目地址**：[https://github.com/dqzboy/Docker-Proxy](https://github.com/dqzboy/Docker-Proxy)

感谢 [dqzboy](https://github.com/dqzboy) 及 Docker-Proxy 贡献者们的开源工作。

> easy-docker-proxy **不是** Docker-Proxy 的 fork。我们刻意未引入其 Web 管理面板中的容器管理、Docker socket 挂载等能力，并在鉴权、拉取记录持久化、统计展示等方面采用不同的产品边界与安全默认值。

## License

[MIT](./LICENSE)
