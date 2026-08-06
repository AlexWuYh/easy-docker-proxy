# 多仓库镜像加速代理 — 设计方案

> **项目**：easy-docker-proxy  
> **定位**：轻量、安全、可运维的 Registry V2 多上游 pull-through 代理  
> **参考**：Docker-Proxy 的数据面设计（Host 路由、服务端换 Token、流式转发、双端口）  
> **刻意砍掉**：容器管理、Docker socket、复杂运维面板、本地镜像缓存  

---

## 1. 目标与非目标

### 1.1 目标

| ID | 目标 | 说明 |
|----|------|------|
| G1 | 多仓库加速 | 一个进程代理 Docker Hub / GHCR / Quay / K8s / MCR / NVCR 等 |
| G2 | 零镜像缓存 | 层与 manifest **流式转发、不落盘**；本地只存**拉取记录与聚合统计** |
| G3 | 简单可视化 | Web 页展示拉取量、热门镜像、客户端、错误率等 |
| G4 | 安全默认 | 管理面内网隔离、强密钥、无默认弱口令、无 docker.sock |
| G5 | 易部署 | `docker compose` 一键起；配置以 YAML 为主 |

### 1.2 非目标（明确不做）

- 容器启停 / 日志 / 更新（不挂 `docker.sock`）
- 本地磁盘 / 对象存储镜像缓存
- 镜像 push / 私有 registry 托管
- 网络诊断、文档 CMS、多用户 RBAC（首版）
- 在 Web 上热改上游账号密码以外的复杂配置（首版以文件配置 + 可选热重载为主）

---

## 2. 总体架构

```
                         公网 / 内网客户端
                    docker pull hub.example.com/...
                                │
                    ┌───────────▼───────────┐
                    │  Edge (Caddy/Nginx)     │
                    │  · TLS 终结             │
                    │  · 按子域名反代          │
                    │  · 限流 / 访问日志       │
                    │  · 禁止管理路径外泄      │
                    └───────────┬───────────┘
                                │ Host + X-Forwarded-*
                    ┌───────────▼───────────┐
                    │  proxy (Go，数据面)     │  :5000  仅 registry API
                    │  · Host → 上游路由      │
                    │  · Token 换发 + 缓存    │
                    │  · 流式转发 blob        │
                    │  · IP ACL / 限速        │
                    │  · 异步写拉取事件        │
                    └─────┬───────────┬─────┘
                          │           │
              内网 only    │           │ 异步 batch
                    ┌─────▼─────┐ ┌───▼────────────┐
                    │ admin API │ │ SQLite / PG     │
                    │ :127.0.0  │ │ pull_events     │
                    │ .1:5001   │ │ daily_stats     │
                    │ token 鉴权│ └────────┬───────┘
                    └─────┬─────┘          │
                          │                │ 只读查询
                    ┌─────▼────────────────▼───────┐
                    │  stats-ui（轻量 Web）           │
                    │  · 静态前端 + 少量 API          │
                    │  · 只读统计 / 分析              │
                    │  · 可选 Basic/Token 保护        │
                    └──────────────────────────────┘
                                │
                          上游 Registry
                     (Docker Hub / GHCR / …)
```

### 2.1 进程与职责

| 组件 | 语言建议 | 职责 |
|------|----------|------|
| **proxy** | Go | Registry V2 只读代理；写拉取事件；可选本机管理 API |
| **stats-ui** | Go 同进程内嵌 *或* 极简静态站 + 同二进制 | 展示统计；**无写敏感配置、无 Docker** |
| **edge** | Caddy / Nginx | TLS、子域名路由、Host 改写 |
| **db** | SQLite（默认）/ PostgreSQL（可选） | 仅事件与聚合，**不存镜像层** |

**推荐部署形态（MVP）**：单个 Go 二进制内含 proxy + 内嵌 stats API + 静态前端，compose 里再加 Caddy。

---

## 3. 从 Docker-Proxy 继承与舍弃

### 3.1 继承（数据面精华）

| 设计点 | 做法 |
|--------|------|
| Host 路由 | `Host` / 受信 `X-Forwarded-Host` → 某条 `registries[]` |
| 服务端 Token | 上游 401 后解析 `WWW-Authenticate`，代理侧换 Bearer 再重试 |
| 不转发客户端凭证 | 去掉客户端 `Authorization`，避免凭证串扰 |
| 剥离上游挑战 | 响应删除 `WWW-Authenticate`，客户端不直连外网 auth |
| 流式转发 | 固定 buffer 读写，不缓冲整层 |
| 方法限制 | 仅 `GET` / `HEAD`（+ `/v2/` 握手） |
| 双端口 | 数据面 `:5000`，管理/stats 内网或同源受控路径 |
| 密码脱敏 | 管理读配置时 password → mask；写回用 sentinel 保留 |
| 日志分级 | quiet / normal（跳过 blob）/ debug |

### 3.2 舍弃（攻击面与复杂度）

| 砍掉 | 原因 |
|------|------|
| HubCMD-UI 全套（Docker 管理、网络测试、文档 CMS…） | 与加速无关且高危 |
| 兼容层双路由 | 易漏鉴权 |
| 自建密码重置 / 明文验证码 | 安全债 |
| 内存-only 流量统计作唯一来源 | 改为持久化拉取记录，可分析 |
| 无条件信任 `X-Forwarded-For` | 改为 trusted proxies |

---

## 4. 核心：Registry 代理设计

### 4.1 请求路径

```
Client → Edge → Proxy.ServeHTTP
  1. 解析 client IP（仅从 trusted proxy 取 XFF）
  2. ACL（白/黑名单）
  3. 限流（IP / 全局限流）
  4. resolveRegistry(Host)
  5. path 匹配：
       GET/HEAD /v2/                     → 本地 200 + API-Version
       GET/HEAD /v2/<name>/manifests/<ref>
       GET/HEAD /v2/<name>/blobs/<digest>
       GET/HEAD /v2/<name>/tags/list
       （可选）/v2/<name>/referrers/<digest>
       其他 → 404
  6. 构造上游 URL = upstream + path + query
  7. doUpstream；若 401 且 auth≠anonymous → getToken → retry
  8. 流式 copy body → client
  9. 异步 emit PullEvent（见 §5）
```

### 4.2 路由配置模型（YAML）

见仓库 `configs/config.example.yaml`。

**校验规则（加载配置时 fail-closed）**：

- `upstream` 必须 `https://`（开发可显式允许 http）
- `upstream` host 建议在内置/可配置 **允许列表** 内
- `admin_listen` 若绑定非 loopback，必须已设置强 token
- `access_control.mode=whitelist` 时名单不能为空
- `PROXY_ADMIN_TOKEN` 未设置时 admin/stats 拒绝服务

### 4.3 Token 缓存

- Key：`registry|realm|service|scope`
- Value：token + `expires_at`（提前 60s 失效）
- 热重载配置时清空 cache

### 4.4 Docker Hub 库名兼容

- **不改写** path，原样转发
- 统计落库时规范化：`library/nginx` → 展示名可标注官方镜像

---

## 5. 拉取记录（只存元数据，不存镜像）

### 5.1 设计原则

- **不存** layer 内容、不存 manifest body
- **只存** 可分析字段：谁、何时、从哪、拉了什么、结果、体量
- 写路径必须 **异步、可丢弃、不阻塞 pull**

### 5.2 事件粒度

| 事件类型 | 何时记录 | 用途 |
|----------|----------|------|
| `manifest` | manifests 请求结束 | 热门镜像、tag 趋势 |
| `blob` | blobs 请求结束 | 流量、带宽 |
| `tags` | tags/list | 可选，可默认不记或采样 |

**推荐默认**：记录 **manifest 全量** + **blob 字节计入聚合**。

### 5.3 表结构（SQLite）

```sql
CREATE TABLE pull_events (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  ts            INTEGER NOT NULL,
  client_ip     TEXT    NOT NULL,
  registry      TEXT    NOT NULL,
  host          TEXT,
  event_type    TEXT    NOT NULL,
  repository    TEXT    NOT NULL,
  reference     TEXT,
  method        TEXT,
  status        INTEGER,
  bytes         INTEGER DEFAULT 0,
  duration_ms   INTEGER,
  user_agent    TEXT,
  error         TEXT
);

CREATE INDEX idx_events_ts ON pull_events(ts);
CREATE INDEX idx_events_repo ON pull_events(repository, ts);
CREATE INDEX idx_events_ip ON pull_events(client_ip, ts);
CREATE INDEX idx_events_reg ON pull_events(registry, ts);

CREATE TABLE stats_daily (
  day           TEXT NOT NULL,
  registry      TEXT NOT NULL,
  repository    TEXT NOT NULL,
  pulls         INTEGER DEFAULT 0,
  bytes_total   INTEGER DEFAULT 0,
  errors        INTEGER DEFAULT 0,
  PRIMARY KEY (day, registry, repository)
);

CREATE TABLE stats_daily_client (
  day           TEXT NOT NULL,
  client_ip     TEXT NOT NULL,
  pulls         INTEGER DEFAULT 0,
  bytes_total   INTEGER DEFAULT 0,
  PRIMARY KEY (day, client_ip)
);
```

### 5.4 「一次 pull」如何计数

| 指标 | 定义 |
|------|------|
| **Pulls** | `event_type=manifest` 且 `status∈[200,299]` |
| **Traffic** | 所有响应 `bytes` 之和 |
| **Errors** | `status≥400` 或上游失败 |

### 5.5 写入流水线

```
proxy handler → channel (buffered)
                    → batch writer (每 200 条或 1s)
                    → INSERT + upsert 聚合表
```

- 进程退出：flush
- 后台 job：按 `event_retention_days` 清理明细
- 可选 Prometheus 计数器

### 5.6 隐私

- `client_ip` 可配置截断 / 哈希
- 日志与事件中 **禁止** 出现 password、Bearer token

---

## 6. 简单 Web：统计与分析

### 6.1 Dashboard（只读）

1. 总览：今日 Pulls、今日流量、7 日 Pulls、错误率、活跃客户端
2. 趋势：近 7/30 日 pulls & bytes
3. 按 Registry 占比
4. 热门镜像 Top N
5. 活跃客户端 Top N
6. 最近失败
7. （可选）最近成功 manifest 事件

### 6.2 API

| 路径 | 说明 |
|------|------|
| `GET /stats/` | 静态页 |
| `GET /api/v1/summary?range=7d` | 总览 |
| `GET /api/v1/timeseries?range=30d&metric=pulls` | 时序 |
| `GET /api/v1/top/repos?limit=20&range=7d` | 热门镜像 |
| `GET /api/v1/top/clients?limit=20&range=7d` | 客户端 |
| `GET /api/v1/errors?limit=50` | 最近错误 |
| `GET /api/v1/events?limit=50` | 最近事件 |
| `GET /metrics` | Prometheus（可选） |
| `GET /healthz` | 存活 |

鉴权：`PROXY_ADMIN_TOKEN`，Header `Authorization: Bearer <token>`。

### 6.3 前端

MVP：单页 + Chart.js + 原生 JS，`embed.FS` 打进二进制。

---

## 7. 管理面（刻意做小）

| 能力 | 方式 |
|------|------|
| 改路由 / ACL / 上游账号 | 改 YAML + `SIGHUP` 或 `POST /-/reload` |
| 看配置（脱敏） | `GET /-/config`（需 token） |
| 健康检查 | `GET /healthz` |

Admin 默认 `127.0.0.1:5001`，切勿映射公网。

---

## 8. 安全设计

| 项 | 策略 |
|----|------|
| 管理/统计鉴权 | 强制强 token；未设置 fail-closed |
| 网络 | 数据面可公网；admin/stats 内网 |
| Client IP | 仅 trusted_proxies 解析 XFF |
| 上游 | https + host allowlist |
| 限流 | 按 IP |
| 镜像 | 不挂 docker.sock；非 root 跑容器 |

---

## 9. 部署

- compose：proxy + Caddy
- 仅暴露数据面端口；stats 经内网域名或 VPN
- 客户端：`docker pull hub.example.com/library/nginx:alpine`

---

## 10. 模块划分

```text
cmd/proxy/main.go
internal/
  config/
  proxy/
  acl/
  ratelimit/
  auth/upstream/
  record/
  store/
  admin/
  statsapi/
  web/
  metrics/
configs/config.example.yaml
deploy/
web/static/
.ai/                 # AI / 架构文档
```

---

## 11. 实施阶段

| 阶段 | 交付 |
|------|------|
| **M1 数据面** | Host 路由、Token、流式、ACL、限流、热重载 |
| **M2 记录** | 异步事件 + SQLite + 日聚合 + 清理 |
| **M3 Stats Web** | 总览 + 趋势 + Top + 错误 + Token 鉴权 |
| **M4 硬化** | trusted proxies、allowlist、文档、Prometheus |
| **M5（可选）** | 告警 Webhook、PG、白名单 Web 编辑 |

---

## 12. 成功标准（MVP Done）

1. 至少 4 个上游子域名可 pull  
2. 服务端无镜像层目录增长（仅 db + 日志）  
3. 拉取记录可查  
4. Stats 有鉴权；无容器管理、无 docker.sock  
5. 管理端口不暴露公网  
6. compose 可在一台 VPS 较快完成部署  

---

## 13. 总结

| 维度 | 方案选择 |
|------|----------|
| 核心 | Go 多仓库 Registry V2 只读代理，零镜像缓存 |
| 记录 | 异步 pull_events + 日聚合 |
| 展示 | 内嵌轻量 Stats 页 |
| 配置 | YAML + env 密钥 + 热重载 |
| 安全 | 管理面本机、强 token、受信代理、ACL、限流 |
| 相对 Docker-Proxy | 保留数据面精髓，砍掉管理面板攻击面 |
