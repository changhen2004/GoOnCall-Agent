SHELL := /bin/bash
.DEFAULT_GOAL := help

BINARY_API := bin/gooncall-api
BINARY_WORKER := bin/gooncall-worker

.PHONY: help build test test-unit test-integration vet fmt lint run run-worker tidy up down clean

help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## 构建 API 与 Worker 二进制
	@mkdir -p bin
	go build -buildvcs=false -o $(BINARY_API) ./cmd/api
	go build -buildvcs=false -o $(BINARY_WORKER) ./cmd/worker

run: ## 本地运行 API
	go run ./cmd/api

run-worker: ## 本地运行 Worker
	go run ./cmd/worker

demo: ## 运行端到端 Demo
	./scripts/demo.sh

test: test-unit ## 运行单元测试
test-unit: ## 运行全部单元测试
	go test ./... -count=1

test-integration: ## 运行集成测试（需要 docker compose 环境）
	go test ./tests/integration/... -count=1 -tags=integration

vet: ## go vet
	go vet ./...

fmt: ## gofmt 格式化
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

lint: ## golangci-lint（如已安装）
	@golangci-lint run 2>/dev/null || echo "golangci-lint not installed"

tidy: ## 整理依赖
	go mod tidy

up: ## 启动 docker compose 基础设施
	docker compose up -d postgres redis rabbitmq prometheus alertmanager qdrant grafana

down: ## 停止 docker compose
	docker compose down

clean: ## 清理构建产物
	rm -rf bin coverage.out
