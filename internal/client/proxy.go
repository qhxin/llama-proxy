package client

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/user/llama-proxy/internal/protocol"
)

// handleMessage 处理从服务端接收到的消息
func (c *Client) handleMessage(msg *protocol.WebSocketMessage) {
	defer protocol.ReleaseMessage(msg)

	// 验证消息类型
	if msg.Type != protocol.MessageTypeRequest {
		c.logger.Warnf("Unexpected message type: %s", msg.Type)
		return
	}

	// 解密请求
	decrypted, err := c.crypto.DecryptAndDecompress(msg.Payload)
	if err != nil {
		c.logger.Errorf("Failed to decrypt request: %v", err)
		c.sendError(msg.RequestID, "decryption_failed", "Failed to decrypt request")
		return
	}

	// 记录流量
	c.bytesIn.Add(int64(len(decrypted)))

	// 解析HTTP请求
	httpReq, err := protocol.DeserializeRequest(decrypted)
	if err != nil {
		c.logger.Errorf("Failed to deserialize request: %v", err)
		c.sendError(msg.RequestID, "invalid_request", "Failed to parse request")
		return
	}
	defer protocol.ReleaseRequest(httpReq)

	// 转发到本地llama-server
	if msg.Stream {
		c.handleStreamRequest(msg.RequestID, httpReq)
	} else {
		c.handleNonStreamRequest(msg.RequestID, httpReq)
	}
}

// handleNonStreamRequest 处理非流式请求
func (c *Client) handleNonStreamRequest(requestID string, httpReq *protocol.HTTPRequest) {
	// 构建目标URL
	targetURL := c.config.LlamaURL + httpReq.URL

	// 创建HTTP请求
	req, err := http.NewRequest(httpReq.Method, targetURL, bytes.NewReader(httpReq.Body))
	if err != nil {
		c.logger.Errorf("Failed to create request: %v", err)
		c.sendError(requestID, "internal_error", "Failed to create request")
		return
	}

	// 复制请求头
	for k, v := range httpReq.Headers {
		req.Header.Set(k, v)
	}

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Errorf("Failed to send request to llama-server: %v", err)
		c.sendError(requestID, "llama_error", fmt.Sprintf("Failed to connect to llama-server: %v", err))
		return
	}
	defer resp.Body.Close()

	// 读取响应体
	body, err := io.ReadAll(io.LimitReader(resp.Body, protocol.MaxPayloadSize))
	if err != nil {
		c.logger.Errorf("Failed to read response body: %v", err)
		c.sendError(requestID, "read_error", "Failed to read response")
		return
	}

	// 构建HTTP响应
	httpResp := protocol.AcquireResponse()
	httpResp.StatusCode = resp.StatusCode
	// 复制响应头
	for k, v := range resp.Header {
		if len(v) > 0 {
			httpResp.Headers[k] = v[0]
		}
	}
	httpResp.Body = body

	// 序列化响应
	respData, err := httpResp.Serialize()
	protocol.ReleaseResponse(httpResp)
	if err != nil {
		c.logger.Errorf("Failed to serialize response: %v", err)
		c.sendError(requestID, "serialize_error", "Failed to serialize response")
		return
	}

	// 发送响应
	c.sendResponse(requestID, protocol.MessageTypeResponse, respData, false)
}

// handleStreamRequest 处理流式请求（SSE）
func (c *Client) handleStreamRequest(requestID string, httpReq *protocol.HTTPRequest) {
	// 构建目标URL
	targetURL := c.config.LlamaURL + httpReq.URL

	// 创建HTTP请求
	req, err := http.NewRequest(httpReq.Method, targetURL, bytes.NewReader(httpReq.Body))
	if err != nil {
		c.logger.Errorf("Failed to create stream request: %v", err)
		c.sendError(requestID, "internal_error", "Failed to create request")
		return
	}

	// 复制请求头
	for k, v := range httpReq.Headers {
		req.Header.Set(k, v)
	}

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Errorf("Failed to send stream request to llama-server: %v", err)
		c.sendError(requestID, "llama_error", fmt.Sprintf("Failed to connect to llama-server: %v", err))
		return
	}
	defer resp.Body.Close()

	// 检查是否为SSE响应
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/event-stream") {
		// 非流式响应，按普通响应处理
		body, err := io.ReadAll(io.LimitReader(resp.Body, protocol.MaxPayloadSize))
		if err != nil {
			c.logger.Errorf("Failed to read response body: %v", err)
			c.sendError(requestID, "read_error", "Failed to read response")
			return
		}

		httpResp := protocol.AcquireResponse()
		httpResp.StatusCode = resp.StatusCode
		for k, v := range resp.Header {
			if len(v) > 0 {
				httpResp.Headers[k] = v[0]
			}
		}
		httpResp.Body = body

		respData, err := httpResp.Serialize()
		protocol.ReleaseResponse(httpResp)
		if err != nil {
			c.sendError(requestID, "serialize_error", "Failed to serialize response")
			return
		}

		c.sendResponse(requestID, protocol.MessageTypeResponse, respData, false)
		return
	}

	// 处理SSE流 - 为这个流式请求借用专用编码器
	streamEncoder := c.crypto.BorrowEncoder()
	defer c.crypto.ReturnEncoder(streamEncoder)

	reader := &sseReader{reader: resp.Body}

	for {
		// 读取一个SSE事件
		event, err := reader.readEvent()
		if err != nil {
			if err == io.EOF {
				// 流结束
				c.sendResponse(requestID, protocol.MessageTypeStreamEnd, nil, true)
				return
			}
			c.logger.Errorf("Failed to read SSE event: %v", err)
			c.sendError(requestID, "stream_error", "Failed to read stream")
			return
		}

		if len(event) == 0 {
			continue
		}

		// 发送流块（复用同一个编码器）
		c.sendResponseWithEncoder(requestID, protocol.MessageTypeStreamChunk, event, true, streamEncoder)

		// 检查是否收到取消信号
		select {
		case <-c.ctx.Done():
			return
		default:
		}
	}
}

// sseReader SSE读取器
type sseReader struct {
	reader io.Reader
	buffer []byte
}

// readEvent 读取一个SSE事件
func (r *sseReader) readEvent() ([]byte, error) {
	var event bytes.Buffer

	// 复用缓冲区，避免每次分配
	if r.buffer == nil {
		r.buffer = make([]byte, 4096) // 4KB 缓冲区
	}

	for {
		n, err := r.reader.Read(r.buffer)
		if err != nil {
			if err == io.EOF && event.Len() > 0 {
				return event.Bytes(), nil
			}
			return nil, err
		}
		if n == 0 {
			continue
		}

		// 添加读取的数据
		event.Write(r.buffer[:n])

		// 检查事件结束（双换行）
		data := event.Bytes()
		if len(data) >= 4 {
			// 检查 "\n\n" 或 "\r\n\r\n"
			if bytes.HasSuffix(data, []byte("\n\n")) || bytes.HasSuffix(data, []byte("\r\n\r\n")) {
				return event.Bytes(), nil
			}
		}

		// 防止缓冲区过大
		if event.Len() > protocol.MaxPayloadSize {
			return event.Bytes(), nil
		}
	}
}

// GetMaxRequestBodySize 返回最大请求体大小
func GetMaxRequestBodySize() int64 {
	return protocol.MaxPayloadSize
}

// GetMaxConcurrentRequests 返回最大并发请求数
func (c *Client) GetMaxConcurrentRequests() int {
	return c.config.MaxConcurrentRequests
}

// SetCompressionLevel 设置压缩级别
func (c *Client) SetCompressionLevel(level int) {
	c.crypto.SetCompressionLevel(level)
}

// AdjustCompressionLevel 根据负载调整压缩级别
func (c *Client) AdjustCompressionLevel(cpuUsage float64) {
	c.crypto.AdjustCompressionLevel(cpuUsage)
}