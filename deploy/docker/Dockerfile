# 多阶段构建：一个镜像同时支持 API 与 Worker（通过 build arg 选择入口二进制）。
ARG BINARY=api

FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/gooncall ./cmd/${BINARY}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /out/gooncall /usr/local/bin/gooncall
COPY configs /app/configs
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["gooncall"]
