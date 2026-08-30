# 多阶段构建：一个镜像同时支持 API 与 Worker（通过 build arg 选择入口二进制）。
# Go 依赖代理与校验库可覆盖：国内网络默认 proxy.golang.org 不可达时，
# 构建时传 --build-arg GOPROXY=https://goproxy.cn,direct （必要时 GOSUMDB=off）。
ARG BINARY=api
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org

FROM golang:1.25-alpine AS builder
# ARG 需在阶段内重新声明才能被 RUN 引用（FROM 前的 ARG 不会传播进构建阶段）。
ARG BINARY
ARG GOPROXY
ARG GOSUMDB
WORKDIR /src

COPY go.mod go.sum ./
RUN GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB} go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/gooncall ./cmd/${BINARY}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /out/gooncall /usr/local/bin/gooncall
COPY configs /app/configs
# RAG 知识库（runbook 等）与 Agent 提示词：配置 LLM 后运行时会读取。
COPY docs /app/docs
COPY prompts /app/prompts
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["gooncall"]
