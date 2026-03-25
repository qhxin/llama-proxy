# llama-proxy Makefile

# 版本信息
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# 构建参数
LDFLAGS := -X main.Version=$(VERSION) \
           -X main.BuildTime=$(BUILD_TIME) \
           -X main.GitCommit=$(GIT_COMMIT)

# 检测操作系统以设置正确的二进制文件名
ifeq ($(OS),Windows_NT)
    BINARY_NAME := llama-proxy.exe
    RM := del /Q
    RMDIR := rmdir /S /Q
else
    BINARY_NAME := llama-proxy
    RM := rm -f
    RMDIR := rm -rf
endif

# 默认目标
.DEFAULT_GOAL := build

# 构建目标
.PHONY: build build-linux build-windows build-darwin build-all clean test install

build:
	@echo "Building llama-proxy..."
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/llama-proxy
	@echo "Build complete: bin/$(BINARY_NAME)"

build-linux:
	@echo "Building for Linux..."
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/llama-proxy-linux-amd64 ./cmd/llama-proxy
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/llama-proxy-linux-arm64 ./cmd/llama-proxy

build-windows:
	@echo "Building for Windows..."
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/llama-proxy-windows-amd64.exe ./cmd/llama-proxy

build-darwin:
	@echo "Building for macOS..."
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/llama-proxy-darwin-amd64 ./cmd/llama-proxy
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/llama-proxy-darwin-arm64 ./cmd/llama-proxy

build-all: build-linux build-windows build-darwin
	@echo "All platforms built"

clean:
	@echo "Cleaning..."
ifeq ($(OS),Windows_NT)
	@if exist bin $(RMDIR) bin
	@if exist release $(RMDIR) release
else
	$(RMDIR) bin/
	$(RMDIR) release/
endif
	go clean

test:
	@echo "Running tests..."
	go test -v ./...

test-race:
	@echo "Running tests with race detector..."
	go test -race -v ./...

install: build
	@echo "Installing..."
ifeq ($(OS),Windows_NT)
	@echo "Please manually copy bin\$(BINARY_NAME) to your desired location"
	@echo "Or add bin\ directory to your PATH"
else
	cp bin/$(BINARY_NAME) $(GOPATH)/bin/$(BINARY_NAME) 2>/dev/null || cp bin/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
endif

# 依赖管理
.PHONY: deps deps-update

deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

deps-update:
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy

# 代码检查
.PHONY: lint fmt vet

lint:
	@echo "Running linter..."
	golangci-lint run ./...

fmt:
	@echo "Formatting code..."
	go fmt ./...

vet:
	@echo "Running go vet..."
	go vet ./...

# 配置生成
.PHONY: config

config:
	@echo "Generating example config..."
ifeq ($(OS),Windows_NT)
	@if not exist etc mkdir etc
	.\bin\$(BINARY_NAME) config example > etc\config.example.yaml
else
	mkdir -p etc
	./bin/$(BINARY_NAME) config example > etc/config.example.yaml
endif

# 运行
.PHONY: run-server run-client

run-server: build
	@echo "Running server..."
ifeq ($(OS),Windows_NT)
	.\bin\$(BINARY_NAME) server --key $(LLAMA_PROXY_KEY)
else
	./bin/$(BINARY_NAME) server --key $(LLAMA_PROXY_KEY)
endif

run-client: build
	@echo "Running client..."
ifeq ($(OS),Windows_NT)
	.\bin\$(BINARY_NAME) client --server $(LLAMA_PROXY_SERVER) --llama $(LLAMA_PROXY_LLAMA) --key $(LLAMA_PROXY_KEY)
else
	./bin/$(BINARY_NAME) client --server $(LLAMA_PROXY_SERVER) --llama $(LLAMA_PROXY_LLAMA) --key $(LLAMA_PROXY_KEY)
endif

# 发布
.PHONY: release

release: clean build-all
	@echo "Creating release archives..."
	mkdir -p release
	tar -czf release/llama-proxy-$(VERSION)-linux-amd64.tar.gz -C bin llama-proxy-linux-amd64
	tar -czf release/llama-proxy-$(VERSION)-linux-arm64.tar.gz -C bin llama-proxy-linux-arm64
	zip -j release/llama-proxy-$(VERSION)-windows-amd64.zip bin/llama-proxy-windows-amd64.exe
	tar -czf release/llama-proxy-$(VERSION)-darwin-amd64.tar.gz -C bin llama-proxy-darwin-amd64
	tar -czf release/llama-proxy-$(VERSION)-darwin-arm64.tar.gz -C bin llama-proxy-darwin-arm64
	@echo "Release archives created in release/"

# Docker
.PHONY: docker-build docker-push

DOCKER_IMAGE ?= llama-proxy
DOCKER_TAG ?= $(VERSION)

docker-build:
	@echo "Building Docker image..."
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):latest .

docker-push: docker-build
	@echo "Pushing Docker image..."
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)
	docker push $(DOCKER_IMAGE):latest

# 帮助
.PHONY: help

help:
	@echo "Available targets:"
	@echo "  build          - Build the binary for current platform"
	@echo "  build-linux    - Build for Linux (amd64, arm64)"
	@echo "  build-windows  - Build for Windows (amd64)"
	@echo "  build-darwin   - Build for macOS (amd64, arm64)"
	@echo "  build-all      - Build for all platforms"
	@echo "  clean          - Clean build artifacts"
	@echo "  test           - Run tests"
	@echo "  test-race      - Run tests with race detector"
	@echo "  install        - Install binary to GOPATH/bin or /usr/local/bin"
	@echo "  deps           - Download dependencies"
	@echo "  deps-update    - Update dependencies"
	@echo "  fmt            - Format code"
	@echo "  vet            - Run go vet"
	@echo "  config         - Generate example config"
	@echo "  run-server     - Run server (requires LLAMA_PROXY_KEY env var)"
	@echo "  run-client     - Run client (requires env vars)"
	@echo "  release        - Create release archives"
	@echo "  docker-build   - Build Docker image"
	@echo "  help           - Show this help"
