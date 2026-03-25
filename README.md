# llama-proxy

[![Go Version](https://img.shields.io/badge/go-1.21+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

WebSocket代理程序，用于实现云端AI应用程序通过加密压缩通道访问本地llama-server大模型。

## 功能特性

- **双模式运行**: 支持服务端（VPS）和客户端（本地）两种模式
- **端到端加密**: 使用 AES-256-GCM 加密算法保护数据传输
- **数据压缩**: 使用 zstd 压缩算法减少带宽占用（压缩率60-80%）
- **流式传输**: 完整支持 OpenAI 流式 API（Server-Sent Events）
- **自动重连**: 客户端断线后自动重连，支持指数退避策略
- **资源优化**: 针对低配置VPS（2核/2GB/3Mbps）极致优化
- **并发控制**: 内置信号量限制并发请求数，防止OOM
- **对象池**: 使用 sync.Pool 复用内存，降低GC压力

## 架构图

```
┌─────────────────────────────────────────────────────────────────────┐
│                           云端 VPS                                   │
│  ┌──────────────┐      WebSocket        ┌──────────────┐            │
│  │ AI应用程序   │  ←───加密+压缩──────→  │ llama-proxy  │            │
│  │ (OpenAI API)│                       │   server     │            │
│  └──────────────┘                       └──────┬───────┘            │
│                                                 │                    │
└─────────────────────────────────────────────────┼────────────────────┘
                                                  │
                                          ┌───────▼───────┐
                                          │   Internet    │
                                          └───────┬───────┘
                                                  │
┌─────────────────────────────────────────────────┼────────────────────┐
│                         本地机器                 │                    │
│  ┌──────────────┐      WebSocket        ┌───────┴───────┐            │
│  │ llama-server │  ←───解密+解压──────→ │ llama-proxy   │            │
│  │ (本地模型)   │                       │   client      │            │
│  └──────────────┘                       └───────────────┘            │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
```

## 快速开始

### 1. 下载安装

#### Linux/macOS (本地编译)

```bash
# 克隆仓库
git clone https://github.com/yourusername/llama-proxy.git
cd llama-proxy

# 编译
make build

# 或者下载预编译版本
wget https://github.com/yourusername/llama-proxy/releases/download/v1.0.0/llama-proxy-linux-amd64.tar.gz
tar -xzf llama-proxy-linux-amd64.tar.gz
```

#### Windows (本地为 Linux 服务器交叉编译)

```powershell
# 在 Windows PowerShell 中执行

# 方法1: 使用脚本一键构建 Windows + Linux 版本
.\build-linux-on-windows.ps1

# 方法2: 手动构建 Linux 版本（用于上传到服务器）
$env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
go build -ldflags "-s -w" -o bin/llama-proxy-linux-amd64 ./cmd/llama-proxy
# 构建完成后清理环境变量
Remove-Item Env:\GOOS; Remove-Item Env:\GOARCH; Remove-Item Env:\CGO_ENABLED

# 上传到 Linux 服务器
scp bin/llama-proxy-linux-amd64 user@your-server:~/llama-proxy/
```

**注意**: 在 Windows 上直接运行 `make build-linux` 会失败，因为 Makefile 的环境变量语法在 Windows 上不兼容。请使用上述 PowerShell 方法。

### 2. 服务端部署（VPS）

创建服务端配置文件 `server.yaml`：

```yaml
server:
  listen: ":8080"              # HTTP API 监听地址
  ws_port: ":18081"            # WebSocket 独立端口（可选，与 listen 相同时共用端口）
  key: "your-secret-key-here"  # 加密密钥（至少16字符）
  max_concurrent_requests: 50  # 最大并发请求数
  compression_level: 15       # zstd压缩级别（1-22）

# 可以包含客户端配置（服务端会忽略）
client:
  server_ws: "ws://localhost:18081"
  llama_url: "http://127.0.0.1:8080"
  key: "your-secret-key-here"

log:
  level: "info"
```

启动服务端：

```bash
# 使用配置文件启动
./llama-proxy server --config server.yaml

# 或使用命令行参数
./llama-proxy server -l :8080 --ws-port :18081 -k "your-secret-key-32-bytes-long!"
```

**端口说明**:
- `listen`: HTTP API 端口，供 AI 应用程序调用
- `ws_port`: WebSocket 端口，供客户端连接（可选，默认与 listen 共用）

### 3. 客户端运行（本地）

创建客户端配置文件 `client.yaml`：

```yaml
client:
  server_ws: "ws://your-vps-ip:18081"  # 服务端 WebSocket 地址
  llama_url: "http://127.0.0.1:8080"    # 本地 llama-server 地址
  key: "your-secret-key-here"           # 加密密钥（必须与服务端相同）
  reconnect_interval: 5s                 # 断线重连间隔
  max_concurrent_requests: 10            # 最大并发请求数
  compression_level: 15                  # zstd压缩级别

# 可以包含服务端配置（客户端会忽略）
server:
  listen: ":8080"
  key: "your-secret-key-here"

log:
  level: "info"
```

启动客户端：

```bash
# 使用配置文件启动
./llama-proxy client --config client.yaml

# 或使用命令行参数
./llama-proxy client -s ws://your-vps:18081 --llama http://127.0.0.1:8080 -k "your-secret-key-32-bytes-long!"
```

**Windows 客户端**:
```powershell
.\llama-proxy.exe client --config client.yaml
```

### 4. 配置AI应用程序

将OpenAI API base URL指向你的VPS：

```python
# Python OpenAI SDK示例
import openai

openai.api_base = "http://your-vps-ip:8080/v1"
openai.api_key = "dummy-key"  # 代理不验证API key

response = openai.ChatCompletion.create(
    model="llama2",
    messages=[{"role": "user", "content": "Hello"}],
    stream=True  # 推荐使用流式传输
)

for chunk in response:
    print(chunk.choices[0].delta.get("content", ""), end="")
```

## 性能数据

针对 2核CPU / 2GB内存 / 3Mbps带宽 VPS 测试：

| 指标 | 数值 |
|------|------|
| 空闲内存占用 | ~50MB |
| 50并发内存占用 | ~300MB |
| 请求延迟 | +30-100ms（取决于网络） |
| 带宽节省 | 60-80%（JSON文本压缩率） |
| 最大并发请求 | 50（可配置） |

## 配置说明

### 统一配置文件（推荐）

可以在一个配置文件中同时包含服务端和客户端配置，程序会根据运行模式自动选择相应部分：

```yaml
# 服务端配置（server 命令使用）
server:
  listen: ":8080"              # HTTP API监听地址
  ws_port: ":18081"            # WebSocket监听地址（可选）
  key: "your-secret-key"       # 加密密钥（至少16字符）
  max_concurrent_requests: 50  # 最大并发请求数
  compression_level: 15        # zstd压缩级别（1-22）
  enable_metrics: false        # 启用监控端点
  metrics_port: ":9090"       # 监控端点端口

# 客户端配置（client 命令使用）
client:
  server_ws: "ws://vps:18081"         # 服务端WebSocket地址
  llama_url: "http://127.0.0.1:8080"  # 本地llama-server
  key: "your-secret-key"              # 必须与服务器相同
  reconnect_interval: 5s              # 重连间隔
  max_concurrent_requests: 10          # 最大并发请求数
  compression_level: 15              # zstd压缩级别

# 日志配置（server 和 client 都使用）
log:
  level: "info"     # 日志级别: debug, info, warn, error, fatal
  format: "text"    # 日志格式: text, json
  output: "stdout"  # 输出: stdout, stderr, 或文件路径
```

### 单独配置文件

如果只需要服务端或客户端配置，可以只保留相应部分：

**仅服务端**:
```yaml
server:
  listen: ":8080"
  ws_port: ":18081"
  key: "your-secret-key"
  max_concurrent_requests: 50
  compression_level: 15

log:
  level: "info"
```

**仅客户端**:
```yaml
client:
  server_ws: "ws://vps:18081"
  llama_url: "http://127.0.0.1:8080"
  key: "your-secret-key"
  max_concurrent_requests: 10
  compression_level: 15

log:
  level: "info"
```

### 环境变量

所有配置都支持通过环境变量覆盖：

```bash
# 服务端
export LLAMA_PROXY_SERVER_KEY="your-secret-key"
export LLAMA_PROXY_SERVER_LISTEN=":8080"
export LLAMA_PROXY_SERVER_MAX_CONCURRENT=50

# 客户端
export LLAMA_PROXY_CLIENT_KEY="your-secret-key"
export LLAMA_PROXY_CLIENT_SERVER_WS="ws://your-vps:8080"
export LLAMA_PROXY_CLIENT_LLAMA_URL="http://127.0.0.1:8080"

# 日志
export LLAMA_PROXY_LOG_LEVEL="info"
```

## 系统服务（systemd）

### 服务端 systemd 配置

创建 `/etc/systemd/system/llama-proxy.service`：

```ini
[Unit]
Description=llama-proxy server
After=network.target

[Service]
Type=simple
User=llama-proxy
Group=llama-proxy
WorkingDirectory=/opt/llama-proxy
ExecStart=/usr/local/bin/llama-proxy server --config /etc/llama-proxy/config.yaml
Restart=always
RestartSec=5

# 资源限制（针对2核/2GB环境优化）
MemoryLimit=1800M
CPUQuota=200%
TasksMax=100

# Go运行时优化
Environment="GOGC=100"
Environment="GOMEMLIMIT=1536MiB"
Environment="GOMAXPROCS=2"

# 安全加固
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/log/llama-proxy

[Install]
WantedBy=multi-user.target
```

启用服务：

```bash
sudo systemctl daemon-reload
sudo systemctl enable llama-proxy
sudo systemctl start llama-proxy
sudo systemctl status llama-proxy
```

### 客户端 systemd 配置（用户级）

创建 `~/.config/systemd/user/llama-proxy.service`：

```ini
[Unit]
Description=llama-proxy client
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%h/bin/llama-proxy client --config %h/.config/llama-proxy/config.yaml
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
```

启用服务：

```bash
systemctl --user daemon-reload
systemctl --user enable llama-proxy
systemctl --user start llama-proxy
```

## 安全建议

1. **强密码**: 使用至少16字符的随机密钥
2. **防火墙**: 限制VPS端口访问，只允许必要IP
3. **TLS加密**: 生产环境使用WSS（WebSocket Secure）
4. **定期更换**: 定期更换加密密钥
5. **fail2ban**: 防止暴力破解尝试

## 故障排除

### 常见问题

**Q: 客户端无法连接到服务端**

```bash
# 1. 检查服务端是否运行
ps aux | grep llama-proxy

# 2. 检查端口监听状态（服务端）
ss -tlnp | grep -E "8080|18081"

# 3. 检查网络连通性（客户端）
curl -v http://your-vps:8080/health
curl -v http://your-vps:18081/health

# 4. 检查防火墙（服务端）
sudo ufw status
sudo ufw allow 8080/tcp
sudo ufw allow 18081/tcp

# 5. 检查云服务商安全组
# 登录控制台确认入方向放行相应端口
```

**常见原因**:
1. 服务端未启动或启动失败
2. 防火墙/安全组未放行端口
3. 服务端配置了 `127.0.0.1:8080`（仅本地监听），应改为 `:8080`
4. 客户端连接的 `server_ws` 端口与服务端 `ws_port` 不一致

**Q: 内存占用过高**
```yaml
# 降低并发限制
server:
  max_concurrent_requests: 20  # 从50降低到20
  compression_level: 3       # 降低压缩级别
```

**Q: 流量超出限制**
```yaml
# 提高压缩级别
compression_level: 19

# 强制使用流式传输（减少单次传输数据量）
```

**Q: 连接频繁断开**
```yaml
# 调整心跳间隔和超时
client:
  reconnect_interval: 3s  # 缩短重连间隔
```

### 调试模式

```bash
# 启用debug日志
./llama-proxy server --key "xxx" --log-level debug

# 查看连接统计
curl http://localhost:8080/stats
```

## Windows 开发特别提示

### 从 Windows 开发到 Linux 部署

**场景**: 在 Windows 笔记本上开发，部署到 Linux VPS

```powershell
# 1. 编辑代码后，构建 Linux 版本
$env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
go build -ldflags "-s -w" -o bin/llama-proxy-linux-amd64 ./cmd/llama-proxy
Remove-Item Env:\GOOS; Remove-Item Env:\GOARCH; Remove-Item Env:\CGO_ENABLED

# 2. 上传到服务器
scp bin/llama-proxy-linux-amd64 user@vps:~/llama-proxy/

# 3. SSH 到服务器重启服务
ssh user@vps "cd llama-proxy && sudo systemctl restart llama-proxy"
```

### 避免环境变量污染

交叉编译后务必清理环境变量，否则后续的本机构建会生成错误格式的二进制：

```powershell
# 错误：编译后没有清理，导致后续构建的 .exe 是 Linux 格式
$env:GOOS = 'linux'; go build -o bin/llama-proxy-linux-amd64 ./cmd/llama-proxy
go build -o bin/llama-proxy.exe ./cmd/llama-proxy  # ❌ 这个 exe 实际上是 Linux 格式！

# 正确：每次交叉编译后清理环境变量
$env:GOOS = 'linux'; go build -o bin/llama-proxy-linux-amd64 ./cmd/llama-proxy
Remove-Item Env:\GOOS
go build -o bin/llama-proxy.exe ./cmd/llama-proxy  # ✓ 正确的 Windows exe
```

## 开发

### 构建

#### Linux/macOS

```bash
# 本地构建
make build

# 交叉编译所有平台
make build-all

# 运行测试
make test

# 代码检查
make lint
```

#### Windows

```powershell
# 构建 Windows 版本（当前平台）
go build -ldflags "-s -w" -o bin/llama-proxy.exe ./cmd/llama-proxy

# 交叉编译 Linux 版本（用于服务器）
$env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
go build -ldflags "-s -w" -o bin/llama-proxy-linux-amd64 ./cmd/llama-proxy
Remove-Item Env:\GOOS; Remove-Item Env:\GOARCH; Remove-Item Env:\CGO_ENABLED

# 交叉编译 macOS 版本
$env:GOOS = 'darwin'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
go build -ldflags "-s -w" -o bin/llama-proxy-darwin-amd64 ./cmd/llama-proxy
Remove-Item Env:\GOOS; Remove-Item Env:\GOARCH; Remove-Item Env:\CGO_ENABLED
```

**注意**: Windows 上 `make build-linux` 会失败，因为 Make 使用 Unix 环境变量语法 (`GOOS=linux go build`)，这在 Windows PowerShell 中不生效。

### 项目结构

```
llama-proxy/
├── cmd/llama-proxy/       # 程序入口
├── internal/
│   ├── client/            # 客户端实现
│   ├── config/            # 配置管理
│   ├── crypto/            # 加密压缩
│   ├── logger/            # 日志
│   ├── protocol/          # 通信协议
│   └── server/            # 服务端实现
├── go.mod
├── Makefile
└── README.md
```

## 许可证

MIT License

## 贡献

欢迎提交Issue和PR！

## 致谢

- [gorilla/websocket](https://github.com/gorilla/websocket) - WebSocket实现
- [klauspost/compress](https://github.com/klauspost/compress) - zstd压缩库
- [cobra](https://github.com/spf13/cobra) - CLI框架
