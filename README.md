# easy-docker-proxy

多上游 Docker Registry **加速代理**：不缓存镜像层，只记拉取记录，带简单统计页。  
客户端 **镜像名与直连上游时相同**（`docker pull nginx` / `ghcr.io/...`），通过 mirrors 或 DNS 把流量指到本代理。

- 无 Docker socket / 无容器管理  
- 数据面与管理面分离（管理口默认不暴露公网）

## 快速部署

```bash
git clone https://github.com/AlexWuYh/easy-docker-proxy.git
cd easy-docker-proxy

cp .env.example .env
# 必填：PROXY_ADMIN_TOKEN、PROXY_WEB_PASSWORD（≥8 位）
# 可选上游账号：DOCKERHUB_*、GITHUB_*、QUAY_* 等（见 .env.example）

cp configs/config.docker.yaml configs/config.yaml
docker compose -f deploy/docker-compose.yaml up -d --build
```

默认只映射主机 **5000**（数据面，避免占用 80/443）。管理面不映射。  
已有 Caddy 时：只跑 proxy，在现有 Caddy 里反代到 `127.0.0.1:5000` 即可（[配置说明](./deploy/README.md#4-使用已有-caddy推荐)、[片段示例](./deploy/caddy.external.example)）。

## 客户端接入

镜像引用**不要改**：

```bash
docker pull nginx:alpine
docker pull ghcr.io/owner/image:tag
```

### Docker Hub（推荐）

在客户端配置 mirrors，指向代理（将 `10.0.0.8` 换成实际地址）：

```json
// /etc/docker/daemon.json
{
  "registry-mirrors": ["http://10.0.0.8:5000"],
  "insecure-registries": ["10.0.0.8:5000"]
}
```

`systemctl restart docker` 后直接 `docker pull nginx:alpine`。  
仓库自带的 `config.docker.yaml` 已设 `default: dockerhub`，mirrors 可直接用。

> `registry-mirrors` 只覆盖 Docker Hub。HTTPS 镜像源则写 `https://...`，并视情况去掉 `insecure-registries`。

### 多仓库（GHCR 等）

1. 配置里 `registries[].hosts` 写官方主机名（如 `ghcr.io`）  
2. 内网 DNS（或 hosts）把这些名字解析到代理  
3. 仍用原名拉取：`docker pull ghcr.io/owner/image:tag`

内网 HTTP 时，客户端需把代理地址加入 `insecure-registries`。HTTPS / 证书问题见 [deploy/README.md](./deploy/README.md)。

### 自检

```bash
curl -H 'Host: registry-1.docker.io' http://127.0.0.1:5000/v2/
# 200 即可
```

## 统计网页

compose 中临时打开本机管理口：

```yaml
ports:
  - "127.0.0.1:5001:5001"
```

浏览器打开 `http://127.0.0.1:5001/stats/login.html`，用 `.env` 中的 Web 账号登录。

## 配置

完整可编辑模板：

- [configs/config.docker.yaml](./configs/config.docker.yaml) / [configs/config.example.yaml](./configs/config.example.yaml)  
- 环境变量清单：[.env.example](./.env.example)  

### 内置上游（均已写出 hosts / upstream / auth）

| name | 典型镜像前缀 | 鉴权环境变量（可选） |
|------|----------------|----------------------|
| dockerhub | `nginx`、`library/...` | `DOCKERHUB_USER` / `DOCKERHUB_TOKEN` |
| ghcr | `ghcr.io/...` | `GITHUB_USER` / `GITHUB_TOKEN` |
| quay | `quay.io/...` | `QUAY_USER` / `QUAY_TOKEN` |
| gcr | `gcr.io/...` | `GCR_USER` / `GCR_TOKEN` |
| k8s | `registry.k8s.io/...` | 默认 anonymous |
| mcr | `mcr.microsoft.com/...` | 默认 anonymous |
| nvcr | `nvcr.io/...` | `NVCR_USER` / `NVCR_TOKEN`（用户名常为 `$oauthtoken`） |
| elastic | `docker.elastic.co/...` | `ELASTIC_USER` / `ELASTIC_TOKEN` |
| ecr-public | `public.ecr.aws/...` | 默认 anonymous |

`auth.type`：`token`（常见）/ `anonymous` / `basic`。变量未设置时按公共匿名处理。

| 你想… | 改什么 |
|--------|--------|
| 上游账号 / 私有库 | 对应 `registries[].auth` + `.env` |
| 关掉某个上游 | 该项 `enabled: false` |
| 限制谁能拉 | `access_control` 或 `pull_auth` |
| 网页 / 运维 | `PROXY_WEB_*` / `PROXY_ADMIN_TOKEN`（仅 Bearer，无 `?token=`） |

```bash
# 热重载（不重开数据库）
curl -X POST -H "Authorization: Bearer $PROXY_ADMIN_TOKEN" \
  http://127.0.0.1:5001/-/reload
```

### 可选：经代理拉取需登录

```yaml
pull_auth:
  mode: required   # off | optional | required
```

```bash
PROXY_PULL_USER=puller
PROXY_PULL_PASSWORD='your-pull-password'
docker login 10.0.0.8:5000 -u puller -p 'your-pull-password'
docker pull nginx:alpine
```

与网页账号、上游仓库密码均相互独立。

## 端口与公网

| 端口 | 用途 |
|------|------|
| **5000** | 数据面（默认对外；mirrors / 现有 Caddy 反代目标） |
| **5001** | 管理 / Stats（默认不映射公网） |
| 现有 Caddy | 推荐：反代到本机 5000（见 deploy 文档） |
| **8080 / 8443** | 仅无现成边缘时：`compose --profile edge` |

1. 不要把 5001 绑到公网  
2. 建议白名单和/或 `pull_auth.required`  
3. `trusted_proxies` 只填真实反代  

详见 [deploy/README.md](./deploy/README.md)。

## 本地开发（Go 1.22+）

```bash
cp configs/config.example.yaml configs/config.yaml
export PROXY_ADMIN_TOKEN="$(openssl rand -hex 32)"
export PROXY_WEB_PASSWORD='your-long-password'
go run ./cmd/proxy -config configs/config.yaml

CGO_ENABLED=0 go test ./...
```

## 致谢

数据面设计借鉴了 [dqzboy/Docker-Proxy](https://github.com/dqzboy/Docker-Proxy)（Host 路由、服务端 Token、流式转发）。本项目不是其 fork，也不含容器管理能力。

## License

[MIT](./LICENSE)
