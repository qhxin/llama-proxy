package protocol

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// MessageType 定义消息类型
type MessageType string

const (
	// MessageTypeRequest HTTP请求
	MessageTypeRequest MessageType = "request"
	// MessageTypeResponse HTTP响应
	MessageTypeResponse MessageType = "response"
	// MessageTypeStreamChunk 流式响应块
	MessageTypeStreamChunk MessageType = "stream_chunk"
	// MessageTypeStreamEnd 流式响应结束
	MessageTypeStreamEnd MessageType = "stream_end"
	// MessageTypeError 错误消息
	MessageTypeError MessageType = "error"
	// MessageTypePing 心跳请求
	MessageTypePing MessageType = "ping"
	// MessageTypePong 心跳响应
	MessageTypePong MessageType = "pong"
)

// WebSocketMessage WebSocket消息结构
type WebSocketMessage struct {
	RequestID string      `json:"request_id"` // 请求唯一标识(UUID)
	Type      MessageType `json:"type"`       // 消息类型
	Payload   []byte      `json:"payload"`    // 加密+压缩后的HTTP数据
	Timestamp int64       `json:"timestamp"`  // 时间戳(毫秒，防重放)
	Stream    bool        `json:"stream"`     // 是否流式请求/响应
}

// HTTPRequest 包装HTTP请求
type HTTPRequest struct {
	Method  string            `json:"method"`  // HTTP方法
	URL     string            `json:"url"`     // 请求URL
	Headers map[string]string `json:"headers"` // 请求头(简化)
	Body    []byte            `json:"body"`    // 请求体
}

// HTTPResponse 包装HTTP响应
type HTTPResponse struct {
	StatusCode int               `json:"status_code"` // 状态码
	Headers    map[string]string `json:"headers"`     // 响应头
	Body       []byte            `json:"body"`        // 响应体
}

// ErrorMessage 错误消息
type ErrorMessage struct {
	Code    string `json:"code"`    // 错误码
	Message string `json:"message"` // 错误信息
}

// 对象池用于重用消息对象，减少GC压力
var (
	messagePool = sync.Pool{
		New: func() interface{} {
			return &WebSocketMessage{}
		},
	}

	requestPool = sync.Pool{
		New: func() interface{} {
			return &HTTPRequest{
				Headers: make(map[string]string),
			}
		},
	}

	responsePool = sync.Pool{
		New: func() interface{} {
			return &HTTPResponse{
				Headers: make(map[string]string),
			}
		},
	}

	bufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 4096) // 默认4KB buffer
		},
	}
)

// AcquireMessage 从池中获取消息对象
func AcquireMessage() *WebSocketMessage {
	msg := messagePool.Get().(*WebSocketMessage)
	msg.RequestID = ""
	msg.Type = ""
	msg.Payload = nil
	msg.Timestamp = 0
	msg.Stream = false
	return msg
}

// ReleaseMessage 将消息对象放回池中
func ReleaseMessage(msg *WebSocketMessage) {
	if msg == nil {
		return
	}
	// 清空Payload以允许GC回收
	msg.Payload = nil
	messagePool.Put(msg)
}

// AcquireRequest 从池中获取请求对象
func AcquireRequest() *HTTPRequest {
	req := requestPool.Get().(*HTTPRequest)
	req.Method = ""
	req.URL = ""
	// 清空headers map
	for k := range req.Headers {
		delete(req.Headers, k)
	}
	req.Body = nil
	return req
}

// ReleaseRequest 将请求对象放回池中
func ReleaseRequest(req *HTTPRequest) {
	if req == nil {
		return
	}
	req.Body = nil
	requestPool.Put(req)
}

// AcquireResponse 从池中获取响应对象
func AcquireResponse() *HTTPResponse {
	resp := responsePool.Get().(*HTTPResponse)
	resp.StatusCode = 0
	// 清空headers map
	for k := range resp.Headers {
		delete(resp.Headers, k)
	}
	resp.Body = nil
	return resp
}

// ReleaseResponse 将响应对象放回池中
func ReleaseResponse(resp *HTTPResponse) {
	if resp == nil {
		return
	}
	resp.Body = nil
	responsePool.Put(resp)
}

// AcquireBuffer 从池中获取buffer
func AcquireBuffer() []byte {
	return bufferPool.Get().([]byte)
}

// ReleaseBuffer 将buffer放回池中
func ReleaseBuffer(buf []byte) {
	if cap(buf) <= 65536 { // 只回收小于64KB的buffer
		bufferPool.Put(buf[:4096]) // 重置长度
	}
}

// NewMessage 创建新消息（带当前时间戳）
func NewMessage(requestID string, msgType MessageType, payload []byte, stream bool) *WebSocketMessage {
	msg := AcquireMessage()
	msg.RequestID = requestID
	msg.Type = msgType
	msg.Payload = payload
	msg.Timestamp = time.Now().UnixMilli()
	msg.Stream = stream
	return msg
}

// NewPingMessage 创建ping消息
func NewPingMessage() *WebSocketMessage {
	return NewMessage("", MessageTypePing, nil, false)
}

// NewPongMessage 创建pong消息
func NewPongMessage() *WebSocketMessage {
	return NewMessage("", MessageTypePong, nil, false)
}

// Serialize 序列化消息为JSON
func (m *WebSocketMessage) Serialize() ([]byte, error) {
	return json.Marshal(m)
}

// Deserialize 从JSON反序列化消息
func Deserialize(data []byte) (*WebSocketMessage, error) {
	msg := AcquireMessage()
	if err := json.Unmarshal(data, msg); err != nil {
		ReleaseMessage(msg)
		return nil, err
	}
	return msg, nil
}

// SerializeRequest 序列化HTTP请求
func (r *HTTPRequest) Serialize() ([]byte, error) {
	return json.Marshal(r)
}

// DeserializeRequest 反序列化HTTP请求
func DeserializeRequest(data []byte) (*HTTPRequest, error) {
	req := AcquireRequest()
	if err := json.Unmarshal(data, req); err != nil {
		ReleaseRequest(req)
		return nil, err
	}
	return req, nil
}

// SerializeResponse 序列化HTTP响应
func (r *HTTPResponse) Serialize() ([]byte, error) {
	return json.Marshal(r)
}

// DeserializeResponse 反序列化HTTP响应
func DeserializeResponse(data []byte) (*HTTPResponse, error) {
	resp := AcquireResponse()
	if err := json.Unmarshal(data, resp); err != nil {
		ReleaseResponse(resp)
		return nil, err
	}
	return resp, nil
}

// IsValid 验证消息是否有效
func (m *WebSocketMessage) IsValid() bool {
	if m == nil {
		return false
	}
	if m.RequestID == "" && m.Type != MessageTypePing && m.Type != MessageTypePong {
		return false
	}
	if m.Type == "" {
		return false
	}
	// 检查时间戳（消息不应超过5分钟）
	msgTime := time.UnixMilli(m.Timestamp)
	if time.Since(msgTime) > 5*time.Minute {
		return false
	}
	return true
}

// Clone 创建消息的深拷贝
func (m *WebSocketMessage) Clone() *WebSocketMessage {
	clone := AcquireMessage()
	clone.RequestID = m.RequestID
	clone.Type = m.Type
	if len(m.Payload) > 0 {
		clone.Payload = make([]byte, len(m.Payload))
		copy(clone.Payload, m.Payload)
	}
	clone.Timestamp = m.Timestamp
	clone.Stream = m.Stream
	return clone
}

// WebSocketMessageType 将消息类型转换为WebSocket消息类型
func WebSocketMessageType(msgType MessageType) int {
	switch msgType {
	case MessageTypePing, MessageTypePong:
		return websocket.PingMessage
	default:
		return websocket.TextMessage
	}
}

// MessageTimeout 消息超时时间
const MessageTimeout = 30 * time.Second

// MaxMessageSize 最大消息大小（10MB，考虑到压缩后数据）
const MaxMessageSize = 10 * 1024 * 1024

// MaxPayloadSize 最大payload大小（加密前，5MB）
const MaxPayloadSize = 5 * 1024 * 1024
