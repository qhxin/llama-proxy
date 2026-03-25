package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/user/llama-proxy/internal/protocol"
)

// handleWebSocket 处理WebSocket升级请求
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// 验证密钥（通过header或query参数）
	clientKey := r.Header.Get("X-Auth-Key")
	if clientKey == "" {
		clientKey = r.URL.Query().Get("key")
	}

	// 简单的密钥验证（应该与服务端密钥匹配）
	if clientKey != s.config.Key {
		s.logger.Warnf("WebSocket connection rejected: invalid key from %s", r.RemoteAddr)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 升级连接
	conn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Errorf("Failed to upgrade WebSocket: %v", err)
		return
	}

	// 创建客户端连接
	clientID := uuid.New().String()
	client := &ClientConn{
		ID:       clientID,
		Conn:     conn,
		Server:   s,
		SendChan: make(chan *protocol.WebSocketMessage, 100),
		lastPing: time.Now(),
	}

	// 注册客户端
	s.clientsMu.Lock()
	s.clients[clientID] = client
	s.clientsMu.Unlock()

	s.logger.Infof("Client %s connected from %s", clientID, r.RemoteAddr)

	// 设置关闭回调
	client.onClose = func() {
		s.clientsMu.Lock()
		delete(s.clients, clientID)
		s.clientsMu.Unlock()
		s.logger.Infof("Client %s disconnected", clientID)
	}

	// 启动客户端处理
	client.Run()
}

// IsClientConnected 检查是否有客户端连接
func (s *Server) IsClientConnected() bool {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for _, client := range s.clients {
		if client.isAlive.Load() {
			return true
		}
	}
	return false
}

// GetClientCount 获取客户端数量
func (s *Server) GetClientCount() int {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}

// CloseClient 关闭指定客户端
func (s *Server) CloseClient(clientID string) error {
	s.clientsMu.Lock()
	client, ok := s.clients[clientID]
	s.clientsMu.Unlock()

	if !ok {
		return fmt.Errorf("client not found: %s", clientID)
	}

	client.Close()
	return nil
}

// SendPingToAll 向所有客户端发送ping
func (s *Server) SendPingToAll() {
	msg := protocol.NewPingMessage()
	s.BroadcastToClients(msg)
}
