# 多阶段构建：Agent 服务与本地账号管理命令共用一个镜像。
# Go 依赖代理与校验库可覆盖：国内网络默认 proxy.golang.org 不可达时，
# 构建时传 --build-arg GOPROXY=https://goproxy.cn,direct （必要时 GOSUMDB=off）。
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org

FROM golang:1.25-alpine AS builder
ARG GOPROXY
ARG GOSUMDB
WORKDIR /src

COPY go.mod go.sum ./
RUN GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB} go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/gooncall ./cmd/agent \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/gooncall-admin ./cmd/agentadmin

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /out/gooncall /usr/local/bin/gooncall
COPY --from=builder /out/gooncall-admin /usr/local/bin/gooncall-admin
# 镜像只携带脱敏模板；真实 config.env 由 .dockerignore 排除，运行时挂载。
COPY configs/config.example.env /app/configs/config.example.env
COPY docs /app/docs
WORKDIR /app
EXPOSE 8091
ENTRYPOINT ["gooncall"]
