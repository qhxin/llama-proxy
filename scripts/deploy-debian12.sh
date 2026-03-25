#!/bin/bash
#
# llama-proxy Debian 12 部署脚本
# 适用于 2核CPU/2GB内存/3Mbps带宽 的VPS环境
#

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 配置变量
INSTALL_DIR="/opt/llama-proxy"
CONFIG_DIR="/etc/llama-proxy"
LOG_DIR="/var/log/llama-proxy"
SERVICE_USER="llama-proxy"
BINARY_URL="https://github.com/yourusername/llama-proxy/releases/latest/download/llama-proxy-linux-amd64.tar.gz"

echo -e "${GREEN}================================${NC}"
echo -e "${GREEN}  llama-proxy Debian 12 部署脚本${NC}"
echo -e "${GREEN}================================${NC}"
echo ""

# 检查root权限
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}错误: 请使用root权限运行此脚本${NC}"
    exit 1
fi

# 检查Debian版本
if ! grep -q "VERSION_ID=\"12\"" /etc/os-release 2>/dev/null; then
    echo -e "${YELLOW}警告: 此脚本针对Debian 12优化，其他版本可能不兼容${NC}"
    read -p "是否继续? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

echo -e "${GREEN}[1/8] 系统更新...${NC}"
apt-get update
apt-get upgrade -y

echo -e "${GREEN}[2/8] 安装依赖...${NC}"
apt-get install -y \
    curl \
    wget \
    tar \
    ufw \
    fail2ban \
    htop \
    iftop \
    nload

# 创建系统优化配置
echo -e "${GREEN}[3/8] 配置系统优化...${NC}"

# 内核参数优化（针对低内存和低带宽）
cat > /etc/sysctl.d/99-llama-proxy.conf << 'EOF'
# 网络优化
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 65535
net.ipv4.tcp_max_syn_backlog = 65535
net.ipv4.tcp_fin_timeout = 30
net.ipv4.tcp_keepalive_time = 1200
net.ipv4.tcp_max_tw_buckets = 5000

# TCP BBR拥塞控制算法（内核4.9+支持）
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr

# 内存优化
vm.swappiness = 10
vm.vfs_cache_pressure = 50

# 文件描述符限制
fs.file-max = 1000000
EOF

sysctl -p /etc/sysctl.d/99-llama-proxy.conf

# 系统限制配置
cat > /etc/security/limits.d/99-llama-proxy.conf << 'EOF'
llama-proxy soft nofile 65535
llama-proxy hard nofile 65535
llama-proxy soft nproc 65535
llama-proxy hard nproc 65535
EOF

echo -e "${GREEN}[4/8] 创建用户和目录...${NC}"

# 创建专用用户
if ! id "$SERVICE_USER" &>/dev/null; then
    useradd -r -s /bin/false -M -U "$SERVICE_USER"
fi

# 创建目录
mkdir -p "$INSTALL_DIR" "$CONFIG_DIR" "$LOG_DIR"
chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR" "$CONFIG_DIR" "$LOG_DIR"

echo -e "${GREEN}[5/8] 下载和安装 llama-proxy...${NC}"

# 下载二进制文件（如果本地没有，从GitHub下载）
if [ -f /tmp/llama-proxy-linux-amd64.tar.gz ]; then
    echo "使用本地二进制文件..."
    tar -xzf /tmp/llama-proxy-linux-amd64.tar.gz -C "$INSTALL_DIR"
else
    echo "从GitHub下载..."
    curl -L -o /tmp/llama-proxy.tar.gz "$BINARY_URL" || {
        echo -e "${YELLOW}警告: 下载失败，请手动上传二进制文件到 /tmp/llama-proxy-linux-amd64.tar.gz${NC}"
        exit 1
    }
    tar -xzf /tmp/llama-proxy.tar.gz -C "$INSTALL_DIR"
fi

# 创建符号链接
ln -sf "$INSTALL_DIR/llama-proxy" /usr/local/bin/llama-proxy

# 验证安装
if ! llama-proxy version; then
    echo -e "${RED}错误: 安装验证失败${NC}"
    exit 1
fi

echo -e "${GREEN}[6/8] 生成配置文件...${NC}"

# 生成随机密钥
SECRET_KEY=$(openssl rand -base64 32)

# 服务端配置
cat > "$CONFIG_DIR/config.yaml" << EOF
# llama-proxy 服务端配置（Debian 12 优化版）

server:
  listen: ":8080"
  key: "$SECRET_KEY"
  max_concurrent_requests: 30
  compression_level: 17
  enable_metrics: true
  metrics_port: ":9090"

client:
  server_ws: "ws://localhost:8080"
  llama_url: "http://127.0.0.1:8080"
  key: "$SECRET_KEY"
  reconnect_interval: 5s
  max_concurrent_requests: 10
  compression_level: 17

log:
  level: "info"
  format: "text"
  output: "$LOG_DIR/llama-proxy.log"
EOF

chown "$SERVICE_USER:$SERVICE_USER" "$CONFIG_DIR/config.yaml"
chmod 600 "$CONFIG_DIR/config.yaml"

echo -e "${GREEN}[7/8] 创建systemd服务...${NC}"

# 创建服务文件
cat > /etc/systemd/system/llama-proxy.service << 'EOF'
[Unit]
Description=llama-proxy WebSocket proxy server
Documentation=https://github.com/yourusername/llama-proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=llama-proxy
Group=llama-proxy
WorkingDirectory=/opt/llama-proxy
ExecStart=/usr/local/bin/llama-proxy server --config /etc/llama-proxy/config.yaml
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=5
StartLimitInterval=60s
StartLimitBurst=3

# 资源限制（针对2核/2GB环境）
MemoryLimit=1800M
CPUQuota=200%
TasksMax=100

# Go运行时优化
Environment="GOGC=100"
Environment="GOMEMLIMIT=1536MiB"
Environment="GOMAXPROCS=2"
Environment="GOTRACEBACK=crash"

# 安全加固
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/log/llama-proxy /etc/llama-proxy
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictRealtime=true
RestrictNamespaces=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable llama-proxy

echo -e "${GREEN}[8/8] 配置防火墙...${NC}"

# 配置UFW防火墙
ufw default deny incoming
ufw default allow outgoing

# 允许SSH（防止被锁在外面）
ufw allow ssh

# 允许llama-proxy端口（根据配置调整）
ufw allow 8080/tcp

# 可选：允许监控端口
# ufw allow 9090/tcp

# 启用防火墙（如果未启用）
if ! ufw status | grep -q "Status: active"; then
    echo "y" | ufw enable
fi

echo -e "${GREEN}================================${NC}"
echo -e "${GREEN}     部署完成！${NC}"
echo -e "${GREEN}================================${NC}"
echo ""
echo -e "${YELLOW}重要信息:${NC}"
echo "  配置文件: $CONFIG_DIR/config.yaml"
echo "  日志文件: $LOG_DIR/llama-proxy.log"
echo "  服务名称: llama-proxy"
echo "  加密密钥: $SECRET_KEY"
echo ""
echo -e "${YELLOW}启动服务:${NC}"
echo "  systemctl start llama-proxy"
echo ""
echo -e "${YELLOW}查看状态:${NC}"
echo "  systemctl status llama-proxy"
echo "  journalctl -u llama-proxy -f"
echo ""
echo -e "${YELLOW}监控端点:${NC}"
echo "  http://$(curl -s -4 icanhazip.com):8080/health"
echo "  http://$(curl -s -4 icanhazip.com):8080/stats"
echo ""
echo -e "${RED}安全提醒:${NC}"
echo "  1. 请保存上面的加密密钥，客户端配置需要用到"
echo "  2. 建议修改默认SSH端口并禁用root登录"
echo "  3. 建议配置TLS证书使用WSS协议"
echo "  4. 查看优化指南: $CONFIG_DIR/OPTIMIZATION.md"
echo ""

# 保存密钥到文件
echo "$SECRET_KEY" > "$CONFIG_DIR/.secret_key"
chmod 600 "$CONFIG_DIR/.secret_key"
chown "$SERVICE_USER:$SERVICE_USER" "$CONFIG_DIR/.secret_key"
echo -e "${GREEN}密钥已保存到: $CONFIG_DIR/.secret_key${NC}"

# 创建日志轮转配置
cat > /etc/logrotate.d/llama-proxy << EOF
$LOG_DIR/*.log {
    daily
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    create 0644 llama-proxy llama-proxy
    postrotate
        systemctl reload llama-proxy || true
    endscript
}
EOF

echo -e "${GREEN}日志轮转配置已创建${NC}"

# 创建优化指南
cat > "$CONFIG_DIR/OPTIMIZATION.md" << 'EOF'
# llama-proxy Debian 12 优化指南

## 系统级优化（已自动配置）

### 1. 内核参数 (/etc/sysctl.d/99-llama-proxy.conf)

已配置的优化项：
- TCP BBR拥塞控制算法（提高带宽利用率）
- 增加连接队列大小（支持更多并发）
- 优化TCP保活和超时设置
- 降低swap使用倾向

### 2. 资源限制 (/etc/security/limits.d/99-llama-proxy.conf)

- 文件描述符: 65535
- 进程数: 65535

### 3. Systemd服务限制

- 内存限制: 1800M（保护系统内存）
- CPU限制: 200%（2核满载）
- 任务数限制: 100

### 4. Go运行时优化

```bash
GOGC=100          # 垃圾回收触发阈值
GOMEMLIMIT=1536MiB # Go内存限制（比系统限制略低）
GOMAXPROCS=2      # 限制使用2核
```

## 手动优化建议

### 1. 启用Swap（如果未启用）

```bash
# 创建1GB swap文件
fallocate -l 1G /swapfile
chmod 600 /swapfile
mkswap /swapfile
swapon /swapfile

# 添加到fstab
echo '/swapfile none swap sw 0 0' >> /etc/fstab

# 验证
swapon --show
free -h
```

### 2. 配置fail2ban防止暴力破解

编辑 /etc/fail2ban/jail.local：

```ini
[DEFAULT]
bantime = 3600
findtime = 600
maxretry = 3

[sshd]
enabled = true
port = ssh
filter = sshd
logpath = /var/log/auth.log
maxretry = 3
```

```bash
systemctl restart fail2ban
fail2ban-client status
```

### 3. 监控资源使用

```bash
# 安装监控脚本
apt-get install -y python3-pip
pip3 install psutil

# 创建监控脚本
cat > /usr/local/bin/llama-proxy-monitor.py << 'PYEOF'
#!/usr/bin/env python3
import psutil
import sys

processes = [p for p in psutil.process_iter(['pid', 'name', 'memory_info', 'cpu_percent']) 
             if 'llama-proxy' in p.info['name']]

if not processes:
    print("llama-proxy not running")
    sys.exit(1)

for p in processes:
    mem_mb = p.info['memory_info'].rss / 1024 / 1024
    cpu = p.info['cpu_percent']
    print(f"PID: {p.info['pid']}, Memory: {mem_mb:.1f}MB, CPU: {cpu:.1f}%")
PYEOF

chmod +x /usr/local/bin/llama-proxy-monitor.py
```

### 4. 网络优化检查

```bash
# 检查BBR是否启用
sysctl net.ipv4.tcp_congestion_control
# 应该输出: net.ipv4.tcp_congestion_control = bbr

# 检查系统负载
cat /proc/loadavg

# 检查网络连接数
ss -s
```

### 5. 流量监控

```bash
# 使用nload监控实时流量
nload

# 使用iftop查看连接详情
iftop -i eth0
```

## 故障排除

### 内存不足 (OOM)

如果看到 "Out of memory" 错误：

1. 降低并发限制：
   ```yaml
   server:
     max_concurrent_requests: 20  # 从30降到20
   ```

2. 降低压缩级别：
   ```yaml
   server:
     compression_level: 3  # 从17降到3
   ```

3. 增加swap空间（见上文）

### 流量超出限额

如果接近200GB/月限额：

1. 提高压缩级别：
   ```yaml
   server:
     compression_level: 19  # 最大压缩
   ```

2. 监控流量使用：
   ```bash
   # 查看iptables流量统计
   iptables -L -v -n | grep 8080
   ```

3. 设置流量警报（可选）

### CPU使用率过高

如果CPU持续满载：

1. 降低压缩级别（减少CPU消耗）
2. 限制并发请求数
3. 检查是否有异常流量

### 连接不稳定

1. 检查网络质量：
   ```bash
   mtr -n your-client-ip
   ```

2. 调整重连参数：
   ```yaml
   client:
     reconnect_interval: 3s  # 缩短重连间隔
   ```

3. 检查防火墙设置：
   ```bash
   ufw status
   iptables -L -n
   ```

## 安全加固

### 1. SSH安全配置

编辑 /etc/ssh/sshd_config：

```
Port 2222                    # 修改默认端口
PermitRootLogin no           # 禁止root登录
PasswordAuthentication no    # 禁用密码登录（使用密钥）
MaxAuthTries 3
ClientAliveInterval 300
ClientAliveCountMax 2
```

```bash
systemctl restart sshd
```

### 2. 配置TLS（生产环境强烈建议）

使用 Let's Encrypt 免费证书：

```bash
apt-get install -y certbot

# 获取证书（需要配置DNS）
certbot certonly --standalone -d your-domain.com

# 配置WSS（修改config.yaml）
# 使用反向代理如Nginx或Caddy处理TLS
```

### 3. 定期更新

```bash
# 创建自动更新脚本
cat > /usr/local/bin/update-llama-proxy.sh << 'EOF'
#!/bin/bash
cd /opt/llama-proxy
systemctl stop llama-proxy

# 备份当前版本
cp llama-proxy llama-proxy.backup.$(date +%Y%m%d)

# 下载新版本
wget -O llama-proxy-new.tar.gz "$BINARY_URL"
tar -xzf llama-proxy-new.tar.gz

# 测试新版本
if ./llama-proxy version; then
    systemctl start llama-proxy
    echo "Update successful"
else
    cp llama-proxy.backup.* llama-proxy
    systemctl start llama-proxy
    echo "Update failed, rolled back"
fi
EOF

chmod +x /usr/local/bin/update-llama-proxy.sh

# 添加到crontab（每月检查更新）
echo "0 3 1 * * /usr/local/bin/update-llama-proxy.sh >> /var/log/llama-proxy-update.log 2>&1" | crontab -
```

## 性能基准测试

在2核/2GB/3Mbps环境下，预期性能：

| 指标 | 目标值 |
|------|--------|
| 空闲内存 | < 100MB |
| 最大并发 | 50 请求 |
| 延迟增加 | < 100ms |
| 带宽节省 | 60-80% |
| 稳定性 | > 99.9% |

EOF

chown "$SERVICE_USER:$SERVICE_USER" "$CONFIG_DIR/OPTIMIZATION.md"

echo -e "${GREEN}优化指南已创建: $CONFIG_DIR/OPTIMIZATION.md${NC}"
