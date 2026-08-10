# easy-docker-proxy

**Docker 镜像仓库代理**：把 Docker Hub、GHCR、Quay 等请求接到本机代理，按 Host **自动匹配上游**并流式转发。  
**客户端镜像名保持原样**（与不走代理时相同），不要求改写成其它域名。不缓存镜像层，只记拉取元数据，并提供简单统计网页。

适合：内网 / 机房统一加速入口、要看拉取统计、又不想上带容器管理的重型面板。

## 能做什么

| 能力 | 说明 |
|------|------|
| 仓库代理 | 流量进代理后，按 Host 匹配上游并转发 |
| 镜像名不改 | `docker pull` / Dockerfile / K8s 仍用原始引用 |
| 多上游 | 同一代理可服务 Hub、GHCR 等多个源 |
| 上游鉴权在服务端 | 代理完成 Token 换发；客户端一般不必配上游密码 |
| 零镜像缓存 | 不落盘镜像层，仅 SQLite 元数据与统计 |
| 统计控制台 | 拉取量、流量、热门镜像、客户端、错误与事件 |
| 可选拉取鉴权 | 可要求先 `docker login` 才能经代理拉取 |
| 安全边界 | **不**挂载 Docker socket，**不**管理容器，管理口默认不暴露公网 |

## 不能做什么

- 不做镜像层本地缓存 / 离线仓库  
- 不做容器创建、启停、日志、exec  
- 不做网页里改 YAML 配置（改配置请编辑文件后热重载或重启）  

## 怎么工作

```text
  客户端（镜像名完全不变）
    docker pull nginx:alpine
    docker pull ghcr.io/owner/app:tag
           │
           │  通过 mirrors / 内网 DNS 等
           │  把请求送到代理（域名或 IP:端口）
           ▼
  ┌────────────────────────────┐
  │  代理数据面                │
  │  Host → 匹配 registries    │──► 真实上游
  └────────────────────────────┘
```

| 角色 | 职责 |
|------|------|
| **客户端** | 继续使用**原始**镜像名；只需把「访问路径」指到代理（见下） |
| **代理** | 看请求 Host，选 `upstream`，代拉真实仓库 |
| **上游** | registry-1.docker.io、ghcr.io 等；凭据在 `registries[].auth` |

代理**对外发布**：可用 **域名（推荐）** 或 **IP:端口**。  
客户端侧要保证：发出去的 Registry 请求实际连到这台代理（而不是直连公网上游）。

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
# 确认 registries[].hosts 为客户端会带到请求里的 Host
# （官方仓库主机名，和/或 mirrors 使用的代理域名 / IP）
```

### 2. 启动

```bash
docker compose -f deploy/docker-compose.yaml up -d --build
```

默认只发布 **数据面 5000**，**不**发布管理面 5001。

### 3. 客户端怎么用（核心）

#### 原则

> **`docker pull` 的镜像引用保持与直连上游时完全一致，不做任何改写。**  
> 需要改的是「流量怎么到代理」，不是「镜像名叫什么」。

```bash
# 以下写法与未部署代理时相同
docker pull nginx:alpine
docker pull library/nginx:alpine
docker pull docker.io/library/nginx:alpine
docker pull ghcr.io/owner/image:tag
```

```dockerfile
FROM nginx:alpine
FROM ghcr.io/owner/image:tag
```

```yaml
image: nginx:alpine
```

#### 如何把流量送到代理

代理配置里 `hosts` 写的是：**请求到达代理时 HTTP Host 会是什么**（用来匹配上游）。  
常见两种接入方式：

---

**方式 A — Docker Hub：`registry-mirrors`（最常见）**

镜像名仍是 `nginx:alpine` 等，由 Docker 守护进程把 **Hub 拉取** 转到代理。

1. 代理配置：`configs/config.docker.yaml` 默认 `default: dockerhub`，**开箱即可**接 `registry-mirrors`（即使 Host 是代理 IP）。示意：

```yaml
default: dockerhub   # mirrors 开箱；严格多仓库 DNS 可改为 ""
registries:
  - name: dockerhub
    hosts:
      - "registry-1.docker.io"
      - "docker.io"
      # - "10.0.0.8:5000"   # 若 default 为空则必须写上 mirrors 地址
    upstream: "https://registry-1.docker.io"
    auth:
      type: token
      username: "${DOCKERHUB_USER}"
      password: "${DOCKERHUB_TOKEN}"
```

2. 客户端 `/etc/docker/daemon.json`（Linux）：

```json
{
  "registry-mirrors": ["http://10.0.0.8:5000"],
  "insecure-registries": ["10.0.0.8:5000"]
}
```

HTTPS 代理则写 `https://registry.corp.com`，并去掉对应 insecure 项。  
然后 `systemctl restart docker`。

3. 拉取（**名称不变**）：

```bash
docker pull nginx:alpine
```

> `registry-mirrors` **只作用于 Docker Hub**。GHCR / Quay 等请用方式 B。

---

**方式 B — 多仓库：内网 DNS / hosts 把官方 Registry 主机指到代理**

镜像名仍带官方前缀（`ghcr.io/...`、`quay.io/...`），解析落到代理后，Host 仍是官方名，代理据此匹配上游。

1. 代理 `hosts` 使用**官方主机名**（与镜像引用一致）：

```yaml
registries:
  - name: dockerhub
    hosts: ["registry-1.docker.io", "docker.io"]
    upstream: "https://registry-1.docker.io"
  - name: ghcr
    hosts: ["ghcr.io"]
    upstream: "https://ghcr.io"
```

2. 内网 DNS 或客户端 hosts 示例：

```text
10.0.0.8  registry-1.docker.io docker.io ghcr.io
```

3. 拉取（**名称不变**）：

```bash
docker pull nginx:alpine
docker pull ghcr.io/owner/image:tag
```

HTTPS 注意：浏览器/Docker 会校验证书是否匹配 `ghcr.io` 等官方名。内网常见做法是：

- 代理前用企业证书做 TLS 终结，或  
- 内网仅 HTTP + 按需 `insecure-registries`  

详见 [deploy/README.md](./deploy/README.md)。

---

**代理侧可选：拉取鉴权**

若开启 `pull_auth`，客户端对**实际连上的 Registry 主机**做 `docker login`（mirrors 场景 login 代理地址；DNS 劫持场景 login `ghcr.io` 等——凭证由代理校验，不会代替你登录上游）。

#### 小结

| 项目 | 说明 |
|------|------|
| 镜像名 | **不改**，与直连上游相同 |
| 代理对外 | 域名或 IP:端口 均可 |
| Hub 接入 | 优先 `registry-mirrors` |
| 多上游接入 | 官方主机名写入 `hosts` + DNS/解析指到代理 |
| 上游账号 | 只配在代理 `registries[].auth` |

自检：

```bash
curl -H 'Host: registry-1.docker.io' http://127.0.0.1:5000/v2/
# 期望：HTTP 200
```

### 4. 打开统计网页

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

## 配置上游仓库（重点）

每一条 `registries`：**到达代理的 Host 是什么 → 转发到哪个真实仓库**。  
客户端镜像名仍用官方写法；`hosts` 填请求里会出现的 Host（官方仓库主机名，和/或 mirrors 用的代理域名/IP）。

### 一条 registry 的字段

```yaml
registries:
  - name: dockerhub          # 内部名称；可被全局 default 引用
    enabled: true
    hosts:                   # 请求 Host 匹配列表（可多个）
      - "registry-1.docker.io"  # DNS 劫持 / 原样 Host
      - "docker.io"
      - "10.0.0.8:5000"         # registry-mirrors 指向的代理地址
    upstream: "https://registry-1.docker.io"   # 真实上游，建议 https
    auth:
      type: token            # token | anonymous | basic（见下表）
      username: "${DOCKERHUB_USER}"
      password: "${DOCKERHUB_TOKEN}"
    token_cache_ttl: 3600
    # insecure_skip_verify: false
```

| 字段 | 含义 |
|------|------|
| `hosts` | 用于匹配入站请求的 Host：官方 Registry 主机名，和/或 mirrors 用的域名、IP:端口 |
| `upstream` | 匹配成功后，代理去请求的真实 Registry URL |
| `auth` | **代理 → 上游** 的凭据（不是网页登录，也不是客户端 pull_auth） |
| `default`（全局） | 未命中任何 `hosts` 时回落的 `name`；mirrors 常用 `dockerhub`；公网慎用开放回落 |

### 上游鉴权 `auth.type`（代理 → 上游）

| type | 何时用 | username / password |
|------|--------|---------------------|
| **`token`** | Docker Hub、GHCR、Quay 等标准 Bearer / OAuth 换票（最常见） | 可选。**不填** = 匿名拉公共镜像；**填写** = 提高 Hub 限流配额、拉私有库 |
| **`anonymous`** | 上游完全不要求登录 | 忽略 |
| **`basic`** | 上游只要 HTTP Basic（少数私有 Registry） | 必填，直接作为 Basic 发给上游 |

**Docker Hub 示例（推荐用环境变量，勿写死密码）：**

```yaml
# configs/config.yaml
auth:
  type: token
  username: "${DOCKERHUB_USER}"
  password: "${DOCKERHUB_TOKEN}"
```

```bash
# .env 或 shell
export DOCKERHUB_USER='your-dockerhub-username'
export DOCKERHUB_TOKEN='dckr_pat_xxxx'   # 使用 Access Token，不要用账户登录密码
```

**GHCR 私有镜像示例：**

```yaml
- name: ghcr
  hosts: ["ghcr.io"]
  upstream: "https://ghcr.io"
  auth:
    type: token
    username: "${GITHUB_USER}"       # 任意非空用户名即可，常用自己的 GitHub 用户名
    password: "${GITHUB_TOKEN}"      # PAT，需 read:packages
```

**完全匿名公共库：**

```yaml
auth:
  type: anonymous
# 或 type: token 且不填 username/password
```

**私有 Basic 上游：**

```yaml
auth:
  type: basic
  username: "${REGISTRY_USER}"
  password: "${REGISTRY_PASSWORD}"
```

### 两类「登录」不要混

| | 配置位置 | 谁用 | 作用 |
|--|----------|------|------|
| **上游鉴权** | `registries[].auth` | 代理进程访问 Hub/GHCR… | 限流、私有镜像 |
| **客户端拉取鉴权** | `pull_auth` + `pull_users` | 员工 `docker login 你的域名` | 谁能通过**本代理**拉镜像 |
| **网页控制台** | `PROXY_WEB_*` / `web_users` | 浏览器 | 看统计、管账号 |

### 其它常用项

完整字段见 [configs/config.example.yaml](./configs/config.example.yaml)、[configs/config.docker.yaml](./configs/config.docker.yaml)、[.env.example](./.env.example)。

| 你想… | 改什么 |
|--------|--------|
| 增加一个上游 | 追加 `registries`，`hosts` 写对应官方主机名（及接入方式相关 Host） |
| Hub 用 mirrors | `registry-mirrors` 指向代理；`hosts` 含代理地址和/或设 `default: dockerhub` |
| 公网防滥用 | `access_control.mode: whitelist`，或 `pull_auth.mode: required` |
| 禁止未知 Host 回落 | `default: ""` |
| 网页管理员 | `PROXY_WEB_USER` / `PROXY_WEB_PASSWORD`（空库首次引导） |
| 运维 API / metrics | `PROXY_ADMIN_TOKEN` |

密钥用环境变量 / `.env`，**不要**提交真值。改完配置后：

```bash
curl -X POST -H "Authorization: Bearer $PROXY_ADMIN_TOKEN" http://127.0.0.1:5001/-/reload
# 或 Compose: docker kill -s HUP $(docker compose -f deploy/docker-compose.yaml ps -q proxy)
```

热重载：路由 / ACL / 限流 / 上游与 token 缓存。**不**重开数据库。  
`/-/config`、`/-/reload` 仅 **admin** 或 ops token。

---

## 可选：客户端拉取鉴权（访问本代理）

默认 **匿名可拉**（`pull_auth.mode: off`）。若只允许有账号的人走代理：

```yaml
pull_auth:
  mode: required   # off | optional | required
  realm: easy-docker-proxy
```

```bash
# 首次引导（pull_users 表为空且密码 ≥8 位）
PROXY_PULL_USER=puller
PROXY_PULL_PASSWORD='your-pull-password'
```

或在网页 **账号 → Docker 拉取账号** 管理。客户端对**实际连上的 Registry 主机**登录（mirrors 则 login 代理地址），镜像名仍不改写：

```bash
docker login 10.0.0.8:5000 -u puller -p 'your-pull-password'
docker pull nginx:alpine
```

此密码只校验本代理，**不会**转发给上游。

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
1. 流量是否真的到了代理（`registry-mirrors` / DNS 是否生效）。  
2. 代理 `hosts` 是否包含请求里的 Host（官方名或 mirrors 地址）。  
3. 防火墙、HTTP `insecure-registries`、反代是否保留 Host。

**HTTP 访问报证书 / HTTPS 错误？**  
数据面未上 TLS 时，在客户端 `insecure-registries` 加入代理地址；DNS 劫持官方名时证书更棘手，见上文方式 B。

**Hub 报限流 / 私有库 401？**  
在对应 `registries[].auth` 配置 `type: token` 与 PAT（`${ENV}` 注入），这是代理访问上游用的，不是客户端密码。

**为什么不能改镜像名成自己的域名？**  
可以改，但不是本项目推荐用法。推荐 **镜像名与直连上游一致**，只改接入（mirrors / DNS）。

**网页打不开 / 登不进？**  
确认已映射 `127.0.0.1:5001`、设置了 `PROXY_WEB_PASSWORD` 且是**首次**空库引导；密码至少 8 位。

**只想看统计、不能改配置？**  
正确：网页只做展示与账号管理；改路由/上游请编辑 `config.yaml` 后重载。

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
