package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/user/llama-proxy/internal/config"
	"github.com/user/llama-proxy/internal/crypto"
	"github.com/user/llama-proxy/internal/protocol"
)

// Server 服务端结构
type Server struct {
	config     *config.ServerConfig
	crypto     *crypto.Crypto
	logger     *logrus.Logger

	// HTTP服务器
	httpServer  *http.Server
	wsUpgrader  websocket.Upgrader

	// WebSocket连接管理
	clients     map[string]*ClientConn
	clientsMu   sync.RWMutex

	// 请求管理
	requests    map[string]*PendingRequest
	requestsMu  sync.RWMutex

	// 流量统计
	bytesIn     atomic.Int64
	bytesOut    atomic.Int64

	// 并发控制
	sem         chan struct{} // 用于限制并发请求数

	// 生命周期
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// ClientConn WebSocket客户端连接
type ClientConn struct {
	ID       string
	Conn     *websocket.Conn
	Server   *Server
	SendChan chan *protocol.WebSocketMessage

	// 连接状态
	lastPing  time.Time
	isAlive   atomic.Bool

	// 清理函数
	closeOnce sync.Once
	onClose   func()
}

// PendingRequest 等待响应的请求
type PendingRequest struct {
	RequestID   string
	Stream      bool
	ResponseChan chan *protocol.WebSocketMessage
	DoneChan    chan struct{}
	Writer      http.ResponseWriter
	StartTime   time.Time
}

// New 创建新服务端
func New(cfg *config.ServerConfig, logger *logrus.Logger) (*Server, error) {
	// 创建加密器
	crypt, err := crypto.New(cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to create crypto: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &Server{
		config:   cfg,
		crypto:   crypt,
		logger:   logger,
		clients:  make(map[string]*ClientConn),
		requests: make(map[string]*PendingRequest),
		sem:      make(chan struct{}, cfg.MaxConcurrentRequests),
		ctx:      ctx,
		cancel:   cancel,
	}

	// 配置WebSocket upgrader
	s.wsUpgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			// 在生产环境中应该限制允许的origin
			return true
		},
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		EnableCompression: false, // 我们自己处理压缩
	}

	// 创建HTTP路由器
	router := mux.NewRouter()
	s.setupRoutes(router)

	s.httpServer = &http.Server{
		Addr:         cfg.Listen,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s, nil
}

// setupRoutes 设置HTTP路由
func (s *Server) setupRoutes(router *mux.Router) {
	// OpenAI API端点
	router.HandleFunc("/v1/chat/completions", s.handleChatCompletions).Methods("POST")
	router.HandleFunc("/v1/completions", s.handleCompletions).Methods("POST")
	router.HandleFunc("/v1/models", s.handleModels).Methods("GET")

	// WebSocket端点
	router.HandleFunc("/ws", s.handleWebSocket)

	// 健康检查
	router.HandleFunc("/health", s.handleHealth).Methods("GET")

	// 统计信息（可选）
	router.HandleFunc("/stats", s.handleStats).Methods("GET")
}

// Start 启动服务端
func (s *Server) Start() error {
	s.logger.Infof("Starting server on %s", s.config.Listen)
	s.logger.Infof("WebSocket endpoint: ws://%s/ws", s.config.Listen)

	// 启动清理协程
	s.wg.Add(1)
	go s.cleanupRoutine()

	// 启动HTTP服务器
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server error: %w", err)
	}

	return nil
}

// Stop 停止服务端
func (s *Server) Stop() error {
	s.logger.Info("Stopping server...")

	// 取消上下文
	s.cancel()

	// 关闭所有WebSocket连接
	s.clientsMu.Lock()
	for _, client := range s.clients {
		client.Close()
	}
	s.clients = make(map[string]*ClientConn)
	s.clientsMu.Unlock()

	// 关闭所有待处理请求
	s.requestsMu.Lock()
	for _, req := range s.requests {
		close(req.DoneChan)
	}
	s.requests = make(map[string]*PendingRequest)
	s.requestsMu.Unlock()

	// 关闭HTTP服务器
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		s.logger.Errorf("HTTP server shutdown error: %v", err)
	}

	// 等待所有协程结束
	s.wg.Wait()

	// 关闭加密器
	if err := s.crypto.Close(); err != nil {
		s.logger.Errorf("Crypto close error: %v", err)
	}

	s.logger.Info("Server stopped")
	return nil
}

// GetActiveClient 获取一个活动的客户端连接
func (s *Server) GetActiveClient() *ClientConn {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	// 简单策略：返回第一个活跃的客户端
	for _, client := range s.clients {
		if client.isAlive.Load() {
			return client
		}
	}
	return nil
}

// RegisterRequest 注册一个等待响应的请求
func (s *Server) RegisterRequest(reqID string, stream bool, w http.ResponseWriter) *PendingRequest {
	s.requestsMu.Lock()
	defer s.requestsMu.Unlock()

	req := &PendingRequest{
		RequestID:    reqID,
		Stream:       stream,
		ResponseChan: make(chan *protocol.WebSocketMessage, 10),
		DoneChan:     make(chan struct{}),
		Writer:       w,
		StartTime:    time.Now(),
	}

	s.requests[reqID] = req
	return req
}

// UnregisterRequest 注销请求
func (s *Server) UnregisterRequest(reqID string) {
	s.requestsMu.Lock()
	defer s.requestsMu.Unlock()

	if req, ok := s.requests[reqID]; ok {
		close(req.ResponseChan)
		delete(s.requests, reqID)
	}
}

// GetRequest 获取待处理请求
func (s *Server) GetRequest(reqID string) *PendingRequest {
	s.requestsMu.RLock()
	defer s.requestsMu.RUnlock()
	return s.requests[reqID]
}

// SendToClient 发送消息到客户端
func (s *Server) SendToClient(clientID string, msg *protocol.WebSocketMessage) error {
	s.clientsMu.RLock()
	client, ok := s.clients[clientID]
	s.clientsMu.RUnlock()

	if !ok {
		return fmt.Errorf("client not found: %s", clientID)
	}

	select {
	case client.SendChan <- msg:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("send timeout")
	}
}

// BroadcastToClients 广播消息到所有客户端
func (s *Server) BroadcastToClients(msg *protocol.WebSocketMessage) {
	s.clientsMu.RLock()
	clients := make([]*ClientConn, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.clientsMu.RUnlock()

	for _, client := range clients {
		select {
		case client.SendChan <- msg:
		default:
			s.logger.Warnf("Client %s send channel full", client.ID)
		}
	}
}

// RecordBytes 记录流量
func (s *Server) RecordBytes(in, out int64) {
	s.bytesIn.Add(in)
	s.bytesOut.Add(out)
}

// GetStats 获取统计信息
func (s *Server) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"active_clients":      len(s.clients),
		"pending_requests":    len(s.requests),
		"bytes_in":            s.bytesIn.Load(),
		"bytes_out":           s.bytesOut.Load(),
		"compression_level":   s.crypto.GetCompressionLevel(),
	}
}

// cleanupRoutine 定期清理资源
func (s *Server) cleanupRoutine() {
	defer s.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.cleanupExpiredRequests()
			s.cleanupDeadClients()
		}
	}
}

// cleanupExpiredRequests 清理过期请求
func (s *Server) cleanupExpiredRequests() {
	s.requestsMu.Lock()
	defer s.requestsMu.Unlock()

	timeout := 2 * time.Minute
	now := time.Now()

	for reqID, req := range s.requests {
		if now.Sub(req.StartTime) > timeout {
			s.logger.Warnf("Request %s timed out", reqID)
			close(req.DoneChan)
			delete(s.requests, reqID)
		}
	}
}

// cleanupDeadClients 清理死连接
func (s *Server) cleanupDeadClients() {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()

	for id, client := range s.clients {
		if !client.isAlive.Load() || time.Since(client.lastPing) > 2*time.Minute {
			s.logger.Infof("Cleaning up dead client: %s", id)
			client.Close()
			delete(s.clients, id)
		}
	}
}

// ClientConn 方法

// Run 启动客户端连接处理
func (c *ClientConn) Run() {
	c.isAlive.Store(true)
	c.lastPing = time.Now()

	// 启动读取协程
	go c.readPump()
	// 启动写入协程
	go c.writePump()
}

// readPump 读取WebSocket消息
func (c *ClientConn) readPump() {
	defer func() {
		c.Close()
	}()

	c.Conn.SetReadLimit(protocol.MaxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.lastPing = time.Now()
		return nil
	})

	for {
		_, data, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.Server.logger.Warnf("WebSocket error: %v", err)
			}
			return
		}

		// 处理消息
		if err := c.handleMessage(data); err != nil {
			c.Server.logger.Errorf("Failed to handle message: %v", err)
		}
	}
}

// writePump 写入WebSocket消息
func (c *ClientConn) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case msg, ok := <-c.SendChan:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			data, err := msg.Serialize()
			if err != nil {
				c.Server.logger.Errorf("Failed to serialize message: %v", err)
				protocol.ReleaseMessage(msg)
				continue
			}

			if err := c.Conn.WriteMessage(websocket.TextMessage, data); err != nil {
				c.Server.logger.Errorf("Failed to write message: %v", err)
				protocol.ReleaseMessage(msg)
				return
			}
			protocol.ReleaseMessage(msg)

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-c.Server.ctx.Done():
			return
		}
	}
}

// handleMessage 处理接收到的消息
func (c *ClientConn) handleMessage(data []byte) error {
	msg, err := protocol.Deserialize(data)
	if err != nil {
		return err
	}
	defer protocol.ReleaseMessage(msg)

	// 验证消息
	if !msg.IsValid() {
		return fmt.Errorf("invalid message")
	}

	// 处理心跳
	if msg.Type == protocol.MessageTypePong {
		c.lastPing = time.Now()
		return nil
	}

	// 查找对应的请求
	req := c.Server.GetRequest(msg.RequestID)
	if req == nil {
		return fmt.Errorf("request not found: %s", msg.RequestID)
	}

	// 转发到请求处理协程
	select {
	case req.ResponseChan <- msg.Clone():
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("response channel full")
	}
}

// Close 关闭连接
func (c *ClientConn) Close() {
	c.closeOnce.Do(func() {
		c.isAlive.Store(false)
		close(c.SendChan)
		c.Conn.Close()
		if c.onClose != nil {
			c.onClose()
		}
	})
}
