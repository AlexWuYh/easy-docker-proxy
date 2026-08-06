# Multi-stage build for easy-docker-proxy
FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod ./
# COPY go.sum ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/proxy ./cmd/proxy

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
  && adduser -D -H -u 65532 nonroot
USER nonroot
WORKDIR /app
COPY --from=build /out/proxy /app/proxy
EXPOSE 5000
# Admin/stats should bind 127.0.0.1 inside the container or stay unpublished
ENTRYPOINT ["/app/proxy"]
CMD ["-config", "/etc/proxy/config.yaml"]
