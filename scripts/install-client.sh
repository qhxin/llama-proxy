#!/bin/bash
#
# llama-proxy 客户端安装脚本
# 支持 Linux、macOS 和 Windows (WSL)
#

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# 安装路径
INSTALL_DIR="$HOME/.local/bin"
CONFIG_DIR="$HOME/.config/llama-proxy"
LOG_DIR="$HOME/.local/share/llama-proxy"

# 检测OS和架构
detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)
    
    case "$ARCH" in
        x86_64|amd64)
            ARCH="amd64"
            ;;
        arm64|aarch64)
            ARCH="arm64"
            ;;
        *)
            echo -e "${RED}不支持的架构: $ARCH${NC}"
            exit 1
            ;;
    esac
    
    case "$OS" in
        linux)
            PLATFORM="linux"
            ;;
        darwin)
            PLATFORM="darwin"
            ;;
        *)
            echo -e "${RED}不支持的操作系统: $OS${NC}"
            exit 1
            ;;
    esac
    
    BINARY_NAME="llama-proxy-${PLATFORM}-${ARCH}"
}

echo -e "${GREEN}================================${NC}"
echo -e "${GREEN}  llama-proxy 客户端安装脚本${NC}"
echo -e "${GREEN}================================${NC}"
echo ""

# 检测平台
detect_platform
echo -e "${BLUE}检测到平台: $PLATFORM/$ARCH${NC}"

# 创建目录
echo -e "${GREEN}[1/5] 创建目录...${NC}"
mkdir -p "$INSTALL_DIR" "$CONFIG_DIR" "$LOG_DIR"

# 下载二进制文件
echo -e "${GREEN}[2/5] 下载 llama-proxy...${NC}"

GITHUB_REPO="yourusername/llama-proxy"
BINARY_URL="https://github.com/${GITHUB_REPO}/releases/latest/download/${BINARY_NAME}.tar.gz"

echo "下载地址: $BINARY_URL"

if command -v curl &> /dev/null; then
    curl -L -o /tmp/llama-proxy.tar.gz "$BINARY_URL" 2>/dev/null || {
        echo -e "${YELLOW}下载失败，尝试备用方法...${NC}"
        # 备用：尝试使用wget
        if command -v wget &> /dev/null; then
            wget -O /tmp/llama-proxy.tar.gz "$BINARY_URL" 2>/dev/null || {
                echo -e "${RED}下载失败，请手动下载二进制文件${NC}"
                echo "下载地址: $BINARY_URL"
                exit 1
            }
        else
            echo -e "${RED}需要 curl 或 wget 来下载文件${NC}"
            exit 1
        fi
    }
elif command -v wget &> /dev/null; then
    wget -O /tmp/llama-proxy.tar.gz "$BINARY_URL" 2>/dev/null || {
        echo -e "${RED}下载失败，请手动下载二进制文件${NC}"
        echo "下载地址: $BINARY_URL"
        exit 1
    }
else
    echo -e "${RED}需要 curl 或 wget 来下载文件${NC}"
    exit 1
fi

# 解压
echo -e "${GREEN}[3/5] 安装二进制文件...${NC}"
tar -xzf /tmp/llama-proxy.tar.gz -C "$INSTALL_DIR"
chmod +x "$INSTALL_DIR/llama-proxy"

# 验证安装
if ! "$INSTALL_DIR/llama-proxy" version; then
    echo -e "${RED}安装验证失败${NC}"
    exit 1
fi

# 添加到PATH
echo -e "${GREEN}[4/5] 配置环境...${NC}"
SHELL_RC=""
if [ -n "$BASH_VERSION" ]; then
    SHELL_RC="$HOME/.bashrc"
elif [ -n "$ZSH_VERSION" ]; then
    SHELL_RC="$HOME/.zshrc"
fi

if [ -n "$SHELL_RC" ] && [ -f "$SHELL_RC" ]; then
    if ! grep -q "$INSTALL_DIR" "$SHELL_RC"; then
        echo "export PATH=\"$INSTALL_DIR:\$PATH\"" >> "$SHELL_RC"
        echo -e "${YELLOW}已添加 $INSTALL_DIR 到 PATH${NC}"
        echo -e "${YELLOW}请运行 'source $SHELL_RC' 或重新打开终端${NC}"
    fi
fi

# 生成配置文件
echo -e "${GREEN}[5/5] 生成配置文件...${NC}"

cat > "$CONFIG_DIR/config.yaml" << EOF
# llama-proxy 客户端配置

client:
  # 服务端WebSocket地址（替换为你的VPS地址）
  server_ws: "ws://your-vps-ip:8080"
  
  # 本地llama-server地址
  llama_url: "http://127.0.0.1:8080"
  
  # 加密密钥（从服务端获取）
  key: "YOUR_SECRET_KEY_HERE"
  
  # 重连间隔
  reconnect_interval: 5s
  
  # 最大并发请求数
  max_concurrent_requests: 10
  
  # 压缩级别
  compression_level: 17

log:
  level: "info"
  format: "text"
  output: "$LOG_DIR/llama-proxy.log"
EOF

echo ""
echo -e "${GREEN}================================${NC}"
echo -e "${GREEN}     安装完成！${NC}"
echo -e "${GREEN}================================${NC}"
echo ""
echo -e "${YELLOW}下一步操作:${NC}"
echo ""
echo "1. 编辑配置文件:"
echo -e "   ${BLUE}nano $CONFIG_DIR/config.yaml${NC}"
echo ""
echo "2. 设置服务端地址和密钥:"
echo "   - server_ws: 替换为你的VPS地址"
echo "   - key: 替换为服务端生成的密钥"
echo ""
echo "3. 启动客户端:"
echo -e "   ${BLUE}llama-proxy client --config $CONFIG_DIR/config.yaml${NC}"
echo ""
echo "4. 或使用命令行参数:"
echo -e "   ${BLUE}llama-proxy client -s ws://your-vps:8080 --llama http://127.0.0.1:8080 -k YOUR_KEY${NC}"
echo ""
echo -e "${YELLOW}日志文件: $LOG_DIR/llama-proxy.log${NC}"
echo ""

# 创建systemd用户服务（仅Linux）
if [ "$PLATFORM" = "linux" ] && command -v systemctl &> /dev/null; then
    echo -e "${GREEN}创建用户级systemd服务...${NC}"
    
    mkdir -p "$HOME/.config/systemd/user"
    
    cat > "$HOME/.config/systemd/user/llama-proxy.service" << EOF
[Unit]
Description=llama-proxy client
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/llama-proxy client --config $CONFIG_DIR/config.yaml
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
EOF

    echo -e "${YELLOW}systemd用户服务已创建${NC}"
    echo "启用服务:"
    echo -e "   ${BLUE}systemctl --user daemon-reload${NC}"
    echo -e "   ${BLUE}systemctl --user enable llama-proxy${NC}"
    echo -e "   ${BLUE}systemctl --user start llama-proxy${NC}"
    echo ""
fi

# 创建启动脚本
cat > "$INSTALL_DIR/llama-proxy-start.sh" << EOF
#!/bin/bash
# llama-proxy 客户端启动脚本

CONFIG_FILE="\${1:-$CONFIG_DIR/config.yaml}"

if [ ! -f "\$CONFIG_FILE" ]; then
    echo "错误: 配置文件不存在: \$CONFIG_FILE"
    echo "请先编辑配置文件: $CONFIG_DIR/config.yaml"
    exit 1
fi

# 检查配置
echo "检查配置文件..."
$INSTALL_DIR/llama-proxy config validate --config "\$CONFIG_FILE" || {
    echo "配置验证失败，请检查配置文件"
    exit 1
}

echo "启动 llama-proxy 客户端..."
$INSTALL_DIR/llama-proxy client --config "\$CONFIG_FILE"
EOF

chmod +x "$INSTALL_DIR/llama-proxy-start.sh"

echo -e "${GREEN}启动脚本已创建: $INSTALL_DIR/llama-proxy-start.sh${NC}"
echo ""
echo "快速启动:"
echo -e "   ${BLUE}llama-proxy-start.sh${NC}"
echo ""

# 清理临时文件
rm -f /tmp/llama-proxy.tar.gz
