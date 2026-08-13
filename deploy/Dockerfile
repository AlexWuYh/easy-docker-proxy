# Multi-stage build for easy-docker-proxy (static binary, non-root).
FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/proxy ./cmd/proxy

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata curl \
  && adduser -D -H -u 65532 nonroot \
  && mkdir -p /data /etc/proxy \
  && chown -R nonroot:nonroot /data
USER nonroot
WORKDIR /app
COPY --from=build /out/proxy /app/proxy
# Data plane (admin/stats bind separately; do not publish admin to the public internet)
EXPOSE 5000
VOLUME ["/data"]
# Probe admin /healthz (unauthenticated). Do not use data-plane /v2/ —
# pull_auth.required and IP whitelist would mark a healthy process unhealthy.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -fsS -o /dev/null http://127.0.0.1:5001/healthz || exit 1
ENTRYPOINT ["/app/proxy"]
CMD ["-config", "/etc/proxy/config.yaml"]
