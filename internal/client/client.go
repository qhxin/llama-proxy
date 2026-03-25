package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/user/llama-proxy/internal/config"
	"github.com/user/llama-proxy/internal/crypto"
	"github.com/user/llama-proxy/internal/protocol"
)

// Client 客户端结构
type Client struct {
	config          *config.ClientConfig
	crypto          *crypto.Crypto
	logger          *logrus.Logger

	// WebSocket连接
	wsConn          *websocket.Conn
	wsURL           string
	wsMu            sync.RWMutex

	// 连接状态
	isConnected     atomic.Bool
	isConnecting    atomic.Bool
	lastPingTime    time.Time

	// 消息通道
	sendChan        chan *protocol.WebSocketMessage
	recvChan        chan *protocol.WebSocketMessage

	// HTTP客户端（用于连接本地llama-server）
	httpClient      *http.Client

	// 流量统计
	bytesIn         atomic.Int64
	bytesOut        atomic.Int64

	// 重连控制
	reconnectCount  atomic.Int32
	stopReconnect   atomic.Bool

	// 生命周期
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
}

// New 创建新客户端
func New(cfg *config.ClientConfig, logger *logrus.Logger) (*Client, error) {
	// 创建加密器
	crypt, err := crypto.New(cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to create crypto: %w", err)
	}

	// 验证WebSocket URL
	wsURL, err := url.Parse(cfg.ServerWS)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}

	// 自动添加 /ws 路径（如果没有）
	if wsURL.Path == "" || wsURL.Path == "/" {
		wsURL.Path = "/ws"
	}

	// 添加认证密钥到URL
	query := wsURL.Query()
	query.Set("key", cfg.Key)
	wsURL.RawQuery = query.Encode()

	ctx, cancel := context.WithCancel(context.Background())

	c := &Client{
		config:     cfg,
		crypto:     crypt,
		logger:     logger,
		wsURL:      wsURL.String(),
		sendChan:   make(chan *protocol.WebSocketMessage, 100),
		recvChan:   make(chan *protocol.WebSocketMessage, 100),
		httpClient: &http.Client{
			Timeout:   5 * time.Minute,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		ctx:         ctx,
		cancel:      cancel,
		lastPingTime: time.Now(),
	}

	return c, nil
}

// Start 启动客户端
func (c *Client) Start() error {
	c.logger.Infof("Starting client, connecting to %s", c.config.ServerWS)
	c.logger.Infof("Local llama-server: %s", c.config.LlamaURL)

	// 启动连接管理
	c.wg.Add(1)
	go c.connectionManager()

	// 启动消息处理
	c.wg.Add(1)
	go c.messageProcessor()

	// 等待初始连接
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case <-time.After(100 * time.Millisecond):
			if c.isConnected.Load() {
				c.logger.Info("Client connected successfully")
				return nil
			}
		case <-timeout.C:
			return fmt.Errorf("connection timeout")
		case <-c.ctx.Done():
			return fmt.Errorf("client stopped during startup")
		}
	}
}

// Stop 停止客户端
func (c *Client) Stop() error {
	c.logger.Info("Stopping client...")
	
	// 停止重连
	c.stopReconnect.Store(true)
	
	// 取消上下文
	c.cancel()

	// 关闭WebSocket连接（这会中断 readPump 和 writePump 的阻塞操作）
	c.wsMu.Lock()
	conn := c.wsConn
	if conn != nil {
		// 设置立即关闭，不等待
		conn.Close()
	}
	c.wsMu.Unlock()

	// 等待一小段时间让协程检测到关闭
	time.Sleep(100 * time.Millisecond)

	// 安全关闭通道（使用 select 防止 panic）
	select {
	case <-c.sendChan:
		// 已经关闭
	default:
		close(c.sendChan)
	}

	// 等待所有协程结束（带超时）
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 正常退出
	case <-time.After(5 * time.Second):
		c.logger.Warn("Client stop timeout, forcing exit")
	}

	// 关闭加密器
	if err := c.crypto.Close(); err != nil {
		c.logger.Errorf("Crypto close error: %v", err)
	}

	c.logger.Info("Client stopped")
	return nil
}

// IsConnected 检查是否已连接
func (c *Client) IsConnected() bool {
	return c.isConnected.Load()
}

// connectionManager 连接管理器（处理重连）
func (c *Client) connectionManager() {
	defer c.wg.Done()

	reconnectDelay := c.config.ReconnectInterval

	for {
		if c.stopReconnect.Load() {
			return
		}

		if !c.isConnected.Load() && !c.isConnecting.Load() {
			c.logger.Infof("Attempting to connect to %s", c.config.ServerWS)
			
			if err := c.connect(); err != nil {
				c.logger.Errorf("Connection failed: %v", err)
				c.reconnectCount.Add(1)

				// 指数退避
				delay := time.Duration(c.reconnectCount.Load()) * reconnectDelay
				if delay > 5*time.Minute {
					delay = 5 * time.Minute
				}

				c.logger.Infof("Reconnecting in %v...", delay)
				
				select {
				case <-time.After(delay):
					continue
				case <-c.ctx.Done():
					return
				}
			} else {
				c.reconnectCount.Store(0)
			}
		}

		select {
		case <-c.ctx.Done():
			return
		case <-time.After(5 * time.Second):
			// 定期检查连接状态
			if c.isConnected.Load() && time.Since(c.lastPingTime) > 2*time.Minute {
				c.logger.Warn("No ping received for 2 minutes, reconnecting...")
				c.disconnect()
			}
		}
	}
}

// connect 建立WebSocket连接
func (c *Client) connect() error {
	c.isConnecting.Store(true)
	defer c.isConnecting.Store(false)

	wsConn, _, err := websocket.DefaultDialer.Dial(c.wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	// 配置连接
	wsConn.SetReadLimit(protocol.MaxMessageSize)

	c.wsMu.Lock()
	c.wsConn = wsConn
	c.wsMu.Unlock()

	c.isConnected.Store(true)
	c.lastPingTime = time.Now()

	// 启动读写协程（使用 WaitGroup 跟踪）
	c.wg.Add(2)
	go func() {
		defer c.wg.Done()
		c.readPump()
	}()
	go func() {
		defer c.wg.Done()
		c.writePump()
	}()

	return nil
}

// disconnect 断开连接
func (c *Client) disconnect() {
	c.wsMu.Lock()
	if c.wsConn != nil {
		c.wsConn.Close()
	}
	c.wsMu.Unlock()

	c.isConnected.Store(false)
}

// readPump 读取WebSocket消息
func (c *Client) readPump() {
	defer c.disconnect()

	for {
		c.wsMu.RLock()
		conn := c.wsConn
		c.wsMu.RUnlock()

		if conn == nil {
			return
		}

		// 设置90秒read deadline，服务端每30秒发送ping，有充足余量
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		conn.SetPongHandler(func(string) error {
			c.lastPingTime = time.Now()
			// 关键：收到pong时重置read deadline
			conn.SetReadDeadline(time.Now().Add(90 * time.Second))
			return nil
		})

		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.logger.Warnf("WebSocket error: %v", err)
			}
			return
		}

		// 解析消息
		msg, err := protocol.Deserialize(data)
		if err != nil {
			c.logger.Errorf("Failed to deserialize message: %v", err)
			continue
		}

		// 处理ping
		if msg.Type == protocol.MessageTypePing {
			pong := protocol.NewPongMessage()
			select {
			case c.sendChan <- pong:
			default:
				protocol.ReleaseMessage(pong)
			}
			protocol.ReleaseMessage(msg)
			c.lastPingTime = time.Now()
			continue
		}

		// 转发到处理通道
		select {
		case c.recvChan <- msg:
		default:
			c.logger.Warn("Receive channel full, dropping message")
			protocol.ReleaseMessage(msg)
		}
	}
}

// writePump 写入WebSocket消息
func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-c.sendChan:
			c.wsMu.RLock()
			conn := c.wsConn
			c.wsMu.RUnlock()

			if !ok || conn == nil {
				// sendChan 已关闭或连接断开
				if msg != nil {
					protocol.ReleaseMessage(msg)
				}
				return
			}

			data, err := msg.Serialize()
			if err != nil {
				c.logger.Errorf("Failed to serialize message: %v", err)
				protocol.ReleaseMessage(msg)
				continue
			}

			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				c.logger.Errorf("Failed to write message: %v", err)
				protocol.ReleaseMessage(msg)
				return
			}
			protocol.ReleaseMessage(msg)

		case <-ticker.C:
			// 发送ping
			c.wsMu.RLock()
			conn := c.wsConn
			c.wsMu.RUnlock()

			if conn == nil {
				return
			}

			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.logger.Warnf("Failed to send ping: %v", err)
				return
			}

		case <-c.ctx.Done():
			return
		}
	}
}

// messageProcessor 消息处理器
func (c *Client) messageProcessor() {
	defer c.wg.Done()

	sem := make(chan struct{}, c.config.MaxConcurrentRequests)

	for {
		select {
		case msg := <-c.recvChan:
			if msg == nil {
				return
			}

			// 使用信号量限制并发
			select {
			case sem <- struct{}{}:
				c.wg.Add(1)
				go func(m *protocol.WebSocketMessage) {
					defer c.wg.Done()
					defer func() { <-sem }()
					c.handleMessage(m)
				}(msg)
			case <-c.ctx.Done():
				protocol.ReleaseMessage(msg)
				return
			}

		case <-c.ctx.Done():
			return
		}
	}
}

// sendResponse 发送响应到服务端
func (c *Client) sendResponse(requestID string, msgType protocol.MessageType, data []byte, stream bool) {
	// 加密响应
	encrypted, err := c.crypto.EncryptAndCompress(data)
	if err != nil {
		c.logger.Errorf("Failed to encrypt response: %v", err)
		return
	}

	// 记录流量
	c.bytesOut.Add(int64(len(data)))

	// 创建消息
	msg := protocol.NewMessage(requestID, msgType, encrypted, stream)

	select {
	case c.sendChan <- msg:
	case <-time.After(5 * time.Second):
		c.logger.Warnf("Failed to send response %s: timeout", requestID)
		protocol.ReleaseMessage(msg)
	}
}

// sendError 发送错误响应
func (c *Client) sendError(requestID string, code string, message string) {
	errMsg := &protocol.ErrorMessage{
		Code:    code,
		Message: message,
	}
	data, _ := json.Marshal(errMsg)

	msg := protocol.NewMessage(requestID, protocol.MessageTypeError, data, false)

	select {
	case c.sendChan <- msg:
	case <-time.After(5 * time.Second):
		protocol.ReleaseMessage(msg)
	}
}

// GetStats 获取统计信息
func (c *Client) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"connected":          c.isConnected.Load(),
		"reconnect_count":    c.reconnectCount.Load(),
		"bytes_in":           c.bytesIn.Load(),
		"bytes_out":          c.bytesOut.Load(),
		"compression_level":  c.crypto.GetCompressionLevel(),
	}
}
