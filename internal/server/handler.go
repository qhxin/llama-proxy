package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/user/llama-proxy/internal/protocol"
)

// handleChatCompletions 处理聊天补全请求
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.handleProxyRequest(w, r, "/v1/chat/completions")
}

// handleCompletions 处理补全请求
func (s *Server) handleCompletions(w http.ResponseWriter, r *http.Request) {
	s.handleProxyRequest(w, r, "/v1/completions")
}

// handleModels 处理模型列表请求
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	// 直接返回支持的模型列表
	models := map[string]interface{}{
		"object": "list",
		"data": []map[string]interface{}{
			{
				"id":       "llama-proxy",
				"object":   "model",
				"created":  time.Now().Unix(),
				"owned_by": "llama-proxy",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models)
}

// handleProxyRequest 处理代理请求的核心逻辑
func (s *Server) handleProxyRequest(w http.ResponseWriter, r *http.Request, endpoint string) {
	// 并发控制
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-s.ctx.Done():
		http.Error(w, "Server shutting down", http.StatusServiceUnavailable)
		return
	default:
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}

	// 检查是否有可用的WebSocket客户端
	client := s.GetActiveClient()
	if client == nil {
		http.Error(w, "No client connected", http.StatusServiceUnavailable)
		return
	}

	// 读取请求体
	body, err := io.ReadAll(io.LimitReader(r.Body, protocol.MaxPayloadSize))
	if err != nil {
		s.logger.Errorf("Failed to read request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// 解析请求以检查是否为流式请求
	var reqBody map[string]interface{}
	isStream := false
	if err := json.Unmarshal(body, &reqBody); err == nil {
		if stream, ok := reqBody["stream"].(bool); ok && stream {
			isStream = true
		}
	}

	// 创建HTTPRequest对象
	httpReq := protocol.AcquireRequest()
	httpReq.Method = r.Method
	httpReq.URL = endpoint
	// 复制请求头
	for k, v := range r.Header {
		if len(v) > 0 {
			httpReq.Headers[k] = v[0]
		}
	}
	httpReq.Body = body

	// 序列化请求
	reqData, err := httpReq.Serialize()
	protocol.ReleaseRequest(httpReq)
	if err != nil {
		s.logger.Errorf("Failed to serialize request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// 加密并压缩
	encryptedReq, err := s.crypto.EncryptAndCompress(reqData)
	if err != nil {
		s.logger.Errorf("Failed to encrypt request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// 记录入站流量
	s.RecordBytes(int64(len(body)), 0)

	// 生成请求ID
	requestID := uuid.New().String()

	// 创建WebSocket消息
	msg := protocol.NewMessage(requestID, protocol.MessageTypeRequest, encryptedReq, isStream)

	// 注册请求等待响应
	pendingReq := s.RegisterRequest(requestID, isStream, w)
	defer s.UnregisterRequest(requestID)

	// 发送到客户端
	select {
	case client.SendChan <- msg:
		s.logger.Debugf("Request %s sent to client %s", requestID, client.ID)
	case <-time.After(5 * time.Second):
		s.logger.Errorf("Failed to send request %s: timeout", requestID)
		protocol.ReleaseMessage(msg) // 释放消息避免内存泄漏
		http.Error(w, "Client busy", http.StatusServiceUnavailable)
		return
	}

	// 根据是否为流式请求选择处理方式
	if isStream {
		s.handleStreamResponse(pendingReq, w)
	} else {
		s.handleNonStreamResponse(pendingReq, w)
	}
}

// handleNonStreamResponse 处理非流式响应
func (s *Server) handleNonStreamResponse(req *PendingRequest, w http.ResponseWriter) {
	timeout := time.NewTimer(10 * time.Minute)
	defer timeout.Stop()

	for {
		select {
		case msg := <-req.ResponseChan:
			if msg == nil {
				http.Error(w, "Connection closed", http.StatusServiceUnavailable)
				return
			}

			// 处理错误消息
			if msg.Type == protocol.MessageTypeError {
				s.handleErrorResponse(w, msg)
				protocol.ReleaseMessage(msg)
				return
			}

			// 解密响应
			decrypted, err := s.crypto.DecryptAndDecompress(msg.Payload)
			if err != nil {
				s.logger.Errorf("Failed to decrypt response: %v", err)
				http.Error(w, "Decryption failed", http.StatusInternalServerError)
				protocol.ReleaseMessage(msg)
				return
			}

			// 解析HTTP响应
			httpResp, err := protocol.DeserializeResponse(decrypted)
			if err != nil {
				s.logger.Errorf("Failed to deserialize response: %v", err)
				http.Error(w, "Invalid response", http.StatusInternalServerError)
				protocol.ReleaseMessage(msg)
				return
			}

			// 设置响应头
			w.Header().Set("Content-Type", "application/json")
			for k, v := range httpResp.Headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(httpResp.StatusCode)

			// 写入响应体
			if len(httpResp.Body) > 0 {
				w.Write(httpResp.Body)
				s.RecordBytes(0, int64(len(httpResp.Body)))
			}

			protocol.ReleaseResponse(httpResp)
			protocol.ReleaseMessage(msg)
			return

		case <-req.DoneChan:
			http.Error(w, "Request cancelled", http.StatusRequestTimeout)
			return

		case <-timeout.C:
			http.Error(w, "Request timeout", http.StatusRequestTimeout)
			return

		case <-s.ctx.Done():
			http.Error(w, "Server shutting down", http.StatusServiceUnavailable)
			return
		}
	}
}

// handleStreamResponse 处理流式响应（SSE）
func (s *Server) handleStreamResponse(req *PendingRequest, w http.ResponseWriter) {
	// 设置SSE头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 禁用Nginx缓冲

	// 发送HTTP头
	w.WriteHeader(http.StatusOK)
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.logger.Error("Streaming not supported")
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}
	flusher.Flush()

	timeout := time.NewTimer(15 * time.Minute)
	defer timeout.Stop()

	for {
		select {
		case msg := <-req.ResponseChan:
			if msg == nil {
				// 连接关闭
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}

			// 处理错误
			if msg.Type == protocol.MessageTypeError {
				s.handleStreamError(w, msg, flusher)
				protocol.ReleaseMessage(msg)
				return
			}

			// 处理流结束
			if msg.Type == protocol.MessageTypeStreamEnd {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				protocol.ReleaseMessage(msg)
				return
			}

			// 处理流块
			if msg.Type == protocol.MessageTypeStreamChunk {
				// 解密
				decrypted, err := s.crypto.DecryptAndDecompress(msg.Payload)
				if err != nil {
					s.logger.Errorf("Failed to decrypt stream chunk: %v", err)
					protocol.ReleaseMessage(msg)
					continue
				}

				// 流块已经是SSE格式，直接转发
				w.Write(decrypted)
				s.RecordBytes(0, int64(len(decrypted)))
				flusher.Flush()
			}

			protocol.ReleaseMessage(msg)

		case <-req.DoneChan:
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return

		case <-timeout.C:
			s.logger.Warnf("Stream %s timed out", req.RequestID)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return

		case <-s.ctx.Done():
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
	}
}

// handleErrorResponse 处理错误响应
func (s *Server) handleErrorResponse(w http.ResponseWriter, msg *protocol.WebSocketMessage) {
	var errMsg protocol.ErrorMessage
	if err := json.Unmarshal(msg.Payload, &errMsg); err != nil {
		http.Error(w, "Unknown error", http.StatusInternalServerError)
		return
	}

	statusCode := http.StatusInternalServerError
	switch errMsg.Code {
	case "timeout":
		statusCode = http.StatusRequestTimeout
	case "not_found":
		statusCode = http.StatusNotFound
	case "bad_request":
		statusCode = http.StatusBadRequest
	}

	http.Error(w, errMsg.Message, statusCode)
}

// handleStreamError 处理流式错误
func (s *Server) handleStreamError(w http.ResponseWriter, msg *protocol.WebSocketMessage, flusher http.Flusher) {
	var errMsg protocol.ErrorMessage
	if err := json.Unmarshal(msg.Payload, &errMsg); err != nil {
		fmt.Fprintf(w, "data: {\"error\": \"Unknown error\"}\n\n")
		flusher.Flush()
		return
	}

	// 发送错误作为SSE事件
	errorJSON, _ := json.Marshal(map[string]string{
		"error": errMsg.Message,
	})
	fmt.Fprintf(w, "data: %s\n\n", errorJSON)
	flusher.Flush()

	// 结束流
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// handleHealth 健康检查
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "ok",
		"clients":   len(s.clients),
		"requests":  len(s.requests),
		"timestamp": time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// handleStats 统计信息
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := s.GetStats()
	stats["uptime"] = time.Since(time.Now()).String()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
