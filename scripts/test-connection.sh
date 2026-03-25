#!/bin/bash
#
# llama-proxy 连接测试脚本
# 用于验证服务端和客户端配置是否正确
#

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${GREEN}================================${NC}"
echo -e "${GREEN}  llama-proxy 连接测试${NC}"
echo -e "${GREEN}================================${NC}"
echo ""

# 检查参数
if [ $# -lt 1 ]; then
    echo "用法: $0 <服务端地址> [端口号]"
    echo "示例: $0 your-vps-ip"
    echo "       $0 your-vps-ip 8080"
    exit 1
fi

SERVER_IP="$1"
PORT="${2:-8080}"

echo -e "${BLUE}测试目标: $SERVER_IP:$PORT${NC}"
echo ""

# 测试1: 网络连通性
echo -e "${YELLOW}[1/6] 测试网络连通性...${NC}"
if ping -c 1 -W 3 "$SERVER_IP" > /dev/null 2>&1; then
    echo -e "${GREEN}✓ 网络连通性正常${NC}"
else
    echo -e "${RED}✗ 无法ping通服务器${NC}"
    echo "请检查网络连接和防火墙设置"
fi
echo ""

# 测试2: 端口连通性
echo -e "${YELLOW}[2/6] 测试端口连通性...${NC}"
if command -v nc &> /dev/null; then
    if nc -zv "$SERVER_IP" "$PORT" 2>&1 | grep -q "succeeded"; then
        echo -e "${GREEN}✓ 端口 $PORT 可访问${NC}"
    else
        echo -e "${RED}✗ 端口 $PORT 无法访问${NC}"
        echo "请检查服务端是否运行，以及防火墙设置"
    fi
elif command -v curl &> /dev/null; then
    if curl -s -o /dev/null -w "%{http_code}" "http://$SERVER_IP:$PORT/health" | grep -q "200"; then
        echo -e "${GREEN}✓ 端口 $PORT 可访问，服务运行正常${NC}"
    else
        echo -e "${RED}✗ 端口 $PORT 无法访问${NC}"
    fi
else
    echo -e "${YELLOW}! 未找到nc或curl，跳过端口测试${NC}"
fi
echo ""

# 测试3: HTTP API
echo -e "${YELLOW}[3/6] 测试HTTP API...${NC}"
if command -v curl &> /dev/null; then
    HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "http://$SERVER_IP:$PORT/health" 2>/dev/null || echo "000")
    
    if [ "$HTTP_STATUS" = "200" ]; then
        echo -e "${GREEN}✓ HTTP API 正常 (状态码: $HTTP_STATUS)${NC}"
        
        # 获取健康状态
        HEALTH=$(curl -s "http://$SERVER_IP:$PORT/health" 2>/dev/null)
        echo "  响应: $HEALTH"
    else
        echo -e "${RED}✗ HTTP API 异常 (状态码: $HTTP_STATUS)${NC}"
        echo "请检查服务端日志: journalctl -u llama-proxy -f"
    fi
else
    echo -e "${YELLOW}! 未找到curl，跳过HTTP测试${NC}"
fi
echo ""

# 测试4: WebSocket连接
echo -e "${YELLOW}[4/6] 测试WebSocket连接...${NC}"
if command -v websocat &> /dev/null; then
    echo -e "${BLUE}使用 websocat 测试...${NC}"
    # 这里只能测试连接，不能测试认证
    timeout 3 websocat "ws://$SERVER_IP:$PORT/ws?key=test" 2>/dev/null && {
        echo -e "${GREEN}✓ WebSocket 端口可连接${NC}"
    } || {
        echo -e "${YELLOW}! WebSocket 连接测试受限（需要有效密钥）${NC}"
    }
elif command -v wscat &> /dev/null; then
    echo -e "${BLUE}使用 wscat 测试...${NC}"
    timeout 3 wscat -c "ws://$SERVER_IP:$PORT/ws?key=test" 2>/dev/null && {
        echo -e "${GREEN}✓ WebSocket 端口可连接${NC}"
    } || {
        echo -e "${YELLOW}! WebSocket 连接测试受限（需要有效密钥）${NC}"
    }
else
    echo -e "${YELLOW}! 未找到WebSocket客户端工具${NC}"
    echo "建议安装测试工具: npm install -g wscat"
fi
echo ""

# 测试5: 检查本地llama-server
echo -e "${YELLOW}[5/6] 检查本地llama-server...${NC}"
LLAMA_PORT="${3:-8080}"
LLAMA_URL="http://127.0.0.1:$LLAMA_PORT"

if curl -s "$LLAMA_URL/health" > /dev/null 2>&1 || \
   curl -s -o /dev/null -w "%{http_code}" "$LLAMA_URL" | grep -q "200\|404"; then
    echo -e "${GREEN}✓ 本地llama-server 可访问 (端口: $LLAMA_PORT)${NC}"
else
    echo -e "${RED}✗ 本地llama-server 无法访问${NC}"
    echo "请确保 llama-server 已启动:"
    echo "  ./llama-server -m your-model.gguf --port $LLAMA_PORT"
fi
echo ""

# 测试6: 端到端测试
echo -e "${YELLOW}[6/6] 端到端测试...${NC}"
echo -e "${BLUE}发送测试请求...${NC}"

# 创建测试请求
TEST_REQUEST='{
    "model": "test",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 10
}'

# 发送请求（这需要有客户端连接）
RESPONSE=$(curl -s -w "\n%{http_code}" \
    -X POST \
    -H "Content-Type: application/json" \
    -d "$TEST_REQUEST" \
    "http://$SERVER_IP:$PORT/v1/chat/completions" 2>/dev/null || echo -e "\n000")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" = "200" ]; then
    echo -e "${GREEN}✓ 端到端测试通过${NC}"
    echo "  响应: $BODY"
elif [ "$HTTP_CODE" = "503" ]; then
    echo -e "${YELLOW}! 服务端无可用客户端连接${NC}"
    echo "请确保客户端已启动并连接到服务端"
elif [ "$HTTP_CODE" = "000" ]; then
    echo -e "${RED}✗ 无法连接到服务端${NC}"
else
    echo -e "${YELLOW}! 请求返回状态码: $HTTP_CODE${NC}"
    echo "  响应: $BODY"
fi
echo ""

echo -e "${GREEN}================================${NC}"
echo -e "${GREEN}     测试完成${NC}"
echo -e "${GREEN}================================${NC}"
echo ""
echo -e "${YELLOW}故障排除建议:${NC}"
echo ""
echo "1. 如果端口无法访问:"
echo "   - 检查服务端是否运行: systemctl status llama-proxy"
echo "   - 检查防火墙: ufw status"
echo "   - 检查云服务安全组设置"
echo ""
echo "2. 如果WebSocket连接失败:"
echo "   - 检查密钥是否正确"
echo "   - 查看服务端日志: journalctl -u llama-proxy -n 50"
echo ""
echo "3. 如果本地llama-server无法访问:"
echo "   - 确认 llama-server 已启动"
echo "   - 检查端口是否正确"
echo ""
echo "4. 如果端到端测试失败:"
echo "   - 确保客户端已连接到服务端"
echo "   - 检查客户端日志: ~/.local/share/llama-proxy/llama-proxy.log"
echo ""
