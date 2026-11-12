SHELL := /bin/bash
.DEFAULT_GOAL := help

BINARY_AGENT := bin/gooncall-agent
BINARY_ADMIN := bin/gooncall-agentadmin
BINARY_KNOWLEDGE := bin/gooncall-knowledge
BINARY_RAGEVAL := bin/gooncall-rageval

.PHONY: help build build-agent run-agent admin knowledge-import rag-eval test vet fmt lint tidy up down clean

help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## 构建 Agent、账号管理、知识导入与评测命令
	@mkdir -p bin
	go build -buildvcs=false -o $(BINARY_AGENT) ./cmd/agent
	go build -buildvcs=false -o $(BINARY_ADMIN) ./cmd/agentadmin
	go build -buildvcs=false -o $(BINARY_KNOWLEDGE) ./cmd/knowledgeimport
	go build -buildvcs=false -o $(BINARY_RAGEVAL) ./cmd/rageval

build-agent: ## 只构建 Agent 服务
	@mkdir -p bin
	go build -buildvcs=false -o $(BINARY_AGENT) ./cmd/agent

run-agent: ## 本地运行 Agent 服务（默认 :8091）
	go run ./cmd/agent

admin: ## 本地账号管理（需在 -- 后传子命令）
	go run ./cmd/agentadmin

knowledge-import: ## 导入 Runbook 知识库（需启用 RAG）
	go run ./cmd/knowledgeimport

rag-eval: ## 运行 Runbook 检索评测（需启用 RAG）
	go run ./cmd/rageval

test: ## 运行全部测试
	go test ./... -count=1

test-race: ## 运行关键并发测试
	go test -race ./internal/auth/... ./internal/session/... ./internal/api/agent/... -count=1

vet: ## go vet
	go vet ./...

fmt: ## gofmt 格式化
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

lint: ## golangci-lint（如已安装）
	@golangci-lint run 2>/dev/null || echo "golangci-lint not installed"

tidy: ## 整理依赖
	go mod tidy

up: ## 启动本地基础设施（Qdrant / RabbitMQ / Prometheus）
	docker compose up -d qdrant rabbitmq prometheus

down: ## 停止本地基础设施
	docker compose down

clean: ## 清理构建产物
	rm -rf bin coverage.out
