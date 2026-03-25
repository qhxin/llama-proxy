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

### 2. 服务端部署（VPS）

```bash
# 生成配置文件
./llama-proxy config example > server.yaml

# 编辑配置，设置强密码
vim server.yaml

# 启动服务端
./llama-proxy server --config server.yaml

# 或使用命令行参数
./llama-proxy server -l :8080 -k "your-secret-key-32-bytes-long!"
```

### 3. 客户端运行（本地）

```bash
# 生成配置文件
./llama-proxy config example > client.yaml

# 编辑配置，设置服务端地址和相同密钥
vim client.yaml

# 启动客户端
./llama-proxy client --config client.yaml

# 或使用命令行参数
./llama-proxy client -s ws://your-vps:8080 --llama http://127.0.0.1:8080 -k "your-secret-key-32-bytes-long!"
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

### 服务端配置

```yaml
server:
  listen: ":8080"              # HTTP API监听地址
  key: "your-secret-key"       # 加密密钥（至少16字符）
  max_concurrent_requests: 50  # 最大并发请求数
  compression_level: 15        # zstd压缩级别（1-22）
  enable_metrics: false        # 启用监控端点
```

### 客户端配置

```yaml
client:
  server_ws: "ws://vps:8080"   # 服务端WebSocket地址
  llama_url: "http://127.0.0.1:8080" # 本地llama-server
  key: "your-secret-key"       # 必须与服务器相同
  reconnect_interval: 5s       # 重连间隔
  max_concurrent_requests: 10  # 最大并发请求数
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
# 检查网络连通性
curl -v http://your-vps:8080/health

# 检查防火墙
sudo ufw status
sudo iptables -L -n | grep 8080
```

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

## 开发

### 构建

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
