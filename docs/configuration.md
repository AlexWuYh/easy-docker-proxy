# 配置参考

主配置文件是 `configs/config.yaml`，由 `configs/config.example.yaml`（本地运行）或 `configs/config.docker.yaml`（Compose）复制而来。`${ENV_NAME}` 占位符在启动或重载时从环境变量展开；不要把密码、Token 写入 YAML。

## 快速决策

| 目标 | 设置 |
|---|---|
| Docker Hub mirrors 无前缀 | `default: dockerhub`，并把入口域名写入 dockerhub 的 `hosts` |
| 同域名访问其他仓库 | 为 registry 配置 `path_prefixes`，例如 `ghcr.io` |
| 匿名拉取 | `pull_auth.mode: off`（仍受 ACL/限流约束） |
| 登录后拉取 | `pull_auth.mode: optional` 或 `required`，账号使用 `pull_users` |
| 禁止未知 Host 回落 | `default: ""` |
| 仅允许指定客户端 | `access_control.mode: whitelist` 并填写 `whitelist` |

## 主要字段

- `server.listen`：数据面，默认 `:5000`。`server.admin_listen`：Stats/Admin，默认 `127.0.0.1:5001`，不要公网发布。
- `trusted_proxies`：只填写真实反向代理 peer 或其明确网段。不要填写整个 RFC1918 或 Docker bridge gateway，否则客户端可伪造 XFF。
- `default`：无路径前缀且 Host 未命中时的 registry 名称；公网部署建议留空。
- `rate_limit`：按解析后的客户端 IP 限流。只有来自 `trusted_proxies` 的连接才会使用 XFF/X-Real-IP。
- `upstream_allowlist`：默认开启，限制 `registries[].upstream` 的 hostname；私有上游需显式加入 allowlist。
- `pull_auth`：数据面 Docker Basic 认证，和 Web 账号完全独立。
- `storage.dsn`：SQLite 文件路径。默认启用 WAL；数据目录需要可写。
- `metrics.enabled`：启用后在管理端 `GET /metrics`，仍需鉴权。

## Registry 路由

```yaml
default: dockerhub
registries:
  - name: dockerhub
    hosts: ["reg.example.com"]
    path_prefixes: ["docker.io"]
    upstream: "https://registry-1.docker.io"
    auth: {type: token, username: "${DOCKERHUB_USER}", password: "${DOCKERHUB_TOKEN}"}
  - name: ghcr
    path_prefixes: ["ghcr.io"]
    upstream: "https://ghcr.io"
    auth: {type: token, username: "${GITHUB_USER}", password: "${GITHUB_TOKEN}"}
```

Hub mirrors 使用 `docker pull nginx`；非 Hub 使用 `docker pull reg.example.com/ghcr.io/owner/app:tag`。代理不会自动改写客户端镜像名。

## 热重载

修改 YAML 后执行 `POST /-/reload`（admin 鉴权）或向进程发送 `SIGHUP`。重载覆盖路由、ACL、限流、上游和 Token 缓存；SQLite DSN 和账号库路径需要重启。
