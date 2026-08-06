# 目录与模块约定

```text
easy-docker-proxy/
├── .ai/                      # 供 AI / 贡献者阅读的架构与约束文档
│   ├── 00_PROJECT.md
│   ├── 01_DESIGN.md
│   ├── 02_STRUCTURE.md
│   └── 03_SECURITY.md
├── cmd/proxy/                # 进程入口
├── internal/
│   ├── config/               # YAML 加载、校验、env 展开、热重载
│   ├── proxy/                # Registry 数据面 ServeHTTP
│   ├── acl/                  # IP 黑白名单
│   ├── ratelimit/            # 限流
│   ├── auth/upstream/        # 上游 token / basic
│   ├── record/               # 拉取事件结构 + 异步队列
│   ├── store/                # SQLite/PG 持久化与聚合
│   ├── admin/                # /-/config /-/reload
│   ├── statsapi/             # /api/v1/* 只读统计
│   ├── web/                  # embed 静态资源挂载
│   └── metrics/              # Prometheus（可选）
├── configs/                  # 示例配置
├── deploy/                   # compose / Caddyfile
├── web/static/               # Stats 前端源文件（可 embed）
├── go.mod
├── LICENSE
└── README.md
```

## 包职责边界

| 包 | 可依赖 | 不可做 |
|----|--------|--------|
| `proxy` | config, acl, ratelimit, auth, record | 直接写 SQL、起 UI 服务器细节 |
| `record` | — | 阻塞调用 store 同步写 |
| `store` | database/sql | HTTP、Docker |
| `admin` / `statsapi` | store, config | 容器管理、任意命令执行 |
| `web` | embed | 业务逻辑 |

## 端口约定

| 端口 | 绑定建议 | 用途 |
|------|----------|------|
| 5000 | `0.0.0.0` 或经反代 | Registry 数据面 |
| 5001 | `127.0.0.1` | Admin + Stats API + 静态页 |

## 配置与数据路径（容器内）

| 路径 | 说明 |
|------|------|
| `/etc/proxy/config.yaml` | 主配置（只读挂载） |
| `/data/proxy.db` | SQLite |
