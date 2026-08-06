# 里程碑 — easy-docker-proxy

| ID | 名称 | 状态 | 验收摘要 |
|----|------|------|----------|
| M0 | 脚手架与设计文档 | **done** | 可 `go build`；存在 AGENTS.md、.ai、示例配置 |
| M1 | 数据面代理 | planned | Host 路由、Token 换发、流式、ACL、限流、热重载 |
| M2 | 拉取记录 | planned | 异步事件 + SQLite + 日聚合 + 清理 |
| M3 | Stats Web | planned | 只读看板 + Token 鉴权 |
| M4 | 部署硬化 | planned | trusted proxies、compose 文档、可选 Prometheus |

## 状态枚举

`planned` | `in_progress` | `done` | `cancelled`

---

## M0 — 脚手架与设计文档

**状态：done**

### 验收

- [x] 仓库结构与 `internal/*` 占位包  
- [x] `.ai/` 设计与安全文档  
- [x] `AGENTS.md`、`README.md`、`LICENSE`（MIT）  
- [x] `configs/config.example.yaml`、`deploy/*`  
- [x] `go build ./cmd/proxy` 成功  

---

## M1 — 数据面代理

**状态：planned**

### 目标

可运行的多上游 Registry V2 **只读**代理。

### 范围内

- Host / 受信 X-Forwarded-Host 路由  
- 上游 Token 换发与缓存  
- manifest/blob 流式转发（不落盘镜像）  
- IP ACL、基础限流  
- 配置加载与热重载  
- `PROXY_ADMIN_TOKEN` fail-closed（admin 相关）  

### 非目标

- 拉取记录落库、Stats UI、容器管理  

### 验收标准

- [ ] 至少配置 2 个上游时可按 Host 转发  
- [ ] 大 blob 流式、无镜像缓存目录  
- [ ] 仅 GET/HEAD（+ `/v2/`）  
- [ ] 单元测试覆盖路由与 ACL 关键路径  
- [ ] 更新设计/结构文档中与实现一致的部分  

---

## M2 — 拉取记录

**状态：planned**

见 `.ai/01_DESIGN.md` §5。验收：拉镜像后 DB 有事件与聚合；写库不阻塞 hot path。

---

## M3 — Stats Web

**状态：planned**

见 `.ai/01_DESIGN.md` §6。验收：鉴权后可看总览/Top/错误；无 token 401。

---

## M4 — 部署硬化

**状态：planned**

见 `.ai/01_DESIGN.md` §8–§9。

---

## 变更记录

| 日期 | 说明 |
|------|------|
| 2026-08-06 | 初始化 M0–M4；M0 完成 |
