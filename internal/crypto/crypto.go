package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	// KeySize AES-256 key size
	KeySize = 32
	// NonceSize GCM nonce size
	NonceSize = 12
	// DefaultCompressionLevel 默认压缩级别
	DefaultCompressionLevel = 15
	// LowLoadCompressionLevel 低负载时压缩级别（更高压缩率）
	LowLoadCompressionLevel = 19
	// HighLoadCompressionLevel 高负载时压缩级别（更快压缩）
	HighLoadCompressionLevel = 3
	// LoadThresholdCPU 负载阈值，用于动态调整压缩级别
	LoadThresholdCPU = 0.7
	// TimestampWindow 时间戳窗口（5分钟防重放）
	TimestampWindow = 5 * time.Minute
)

var (
	// ErrInvalidKey 密钥长度无效
	ErrInvalidKey = errors.New("invalid key size, must be 32 bytes")
	// ErrInvalidCiphertext 密文格式无效
	ErrInvalidCiphertext = errors.New("invalid ciphertext format")
	// ErrInvalidTimestamp 时间戳无效（可能是重放攻击）
	ErrInvalidTimestamp = errors.New("invalid timestamp")
	// ErrMessageTooOld 消息过期
	ErrMessageTooOld = errors.New("message too old")
)

// Crypto 加密器
type Crypto struct {
	key               []byte
	block             cipher.Block
	gcm               cipher.AEAD
	encoderPool       sync.Pool
	decoderPool       sync.Pool
	compressionLevel  atomic.Int32
}

// New 创建新的加密器
func New(key string) (*Crypto, error) {
	// 从字符串派生32字节密钥
	hash := sha256.Sum256([]byte(key))
	return NewWithKey(hash[:])
}

// NewWithKey 使用指定密钥创建加密器
func NewWithKey(key []byte) (*Crypto, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	c := &Crypto{
		key:   key,
		block: block,
		gcm:   gcm,
	}

	c.compressionLevel.Store(DefaultCompressionLevel)

	// 初始化编码器池
	c.encoderPool = sync.Pool{
		New: func() interface{} {
			enc, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
			return enc
		},
	}

	// 初始化解码器池
	c.decoderPool = sync.Pool{
		New: func() interface{} {
			dec, _ := zstd.NewReader(nil)
			return dec
		},
	}

	return c, nil
}

// SetCompressionLevel 设置压缩级别（线程安全）
func (c *Crypto) SetCompressionLevel(level int) {
	if level < 1 || level > 22 {
		level = DefaultCompressionLevel
	}
	c.compressionLevel.Store(int32(level))
}

// GetCompressionLevel 获取当前压缩级别
func (c *Crypto) GetCompressionLevel() int {
	return int(c.compressionLevel.Load())
}

// AdjustCompressionLevel 根据负载动态调整压缩级别
func (c *Crypto) AdjustCompressionLevel(cpuUsage float64) {
	if cpuUsage < LoadThresholdCPU {
		// 低负载，提高压缩级别节省带宽
		c.SetCompressionLevel(LowLoadCompressionLevel)
	} else {
		// 高负载，降低压缩级别节省CPU
		c.SetCompressionLevel(HighLoadCompressionLevel)
	}
}

// EncryptAndCompress 加密并压缩数据
// 格式: [4字节时间戳][12字节nonce][密文+tag]
func (c *Crypto) EncryptAndCompress(data []byte) ([]byte, error) {
	// 1. 压缩
	compressed, err := c.compress(data)
	if err != nil {
		return nil, fmt.Errorf("compression failed: %w", err)
	}

	// 2. 添加时间戳防重放
	timestamp := make([]byte, 8)
	binary.BigEndian.PutUint64(timestamp, uint64(time.Now().Unix()))
	
	// 3. 加密（压缩数据 + 时间戳）
	plaintext := make([]byte, len(timestamp)+len(compressed))
	copy(plaintext, timestamp)
	copy(plaintext[len(timestamp):], compressed)

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := c.gcm.Seal(nonce, nonce, plaintext, nil)

	// 4. 组装最终输出: [timestamp][nonce+ciphertext]
	result := make([]byte, 8+len(ciphertext))
	copy(result, timestamp)
	copy(result[8:], ciphertext)

	return result, nil
}

// DecryptAndDecompress 解密并解压数据
func (c *Crypto) DecryptAndDecompress(data []byte) ([]byte, error) {
	if len(data) < 8+NonceSize+c.gcm.Overhead() {
		return nil, ErrInvalidCiphertext
	}

	// 1. 提取时间戳并验证
	timestamp := binary.BigEndian.Uint64(data[:8])
	msgTime := time.Unix(int64(timestamp), 0)
	if time.Since(msgTime) > TimestampWindow {
		return nil, ErrMessageTooOld
	}

	// 2. 提取nonce和密文
	ciphertext := data[8:]
	if len(ciphertext) < NonceSize+c.gcm.Overhead() {
		return nil, ErrInvalidCiphertext
	}

	nonce := ciphertext[:NonceSize]
	ciphertext = ciphertext[NonceSize:]

	// 3. 解密
	plaintext, err := c.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	// 4. 验证内部时间戳（双重验证）
	innerTimestamp := binary.BigEndian.Uint64(plaintext[:8])
	if innerTimestamp != timestamp {
		return nil, ErrInvalidTimestamp
	}

	// 5. 解压
	compressed := plaintext[8:]
	return c.decompress(compressed)
}

// compress 压缩数据（使用对象池）
func (c *Crypto) compress(data []byte) ([]byte, error) {
	level := zstd.EncoderLevelFromZstd(int(c.compressionLevel.Load()))
	
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(level))
	if err != nil {
		return nil, err
	}
	defer enc.Close()

	return enc.EncodeAll(data, nil), nil
}

// decompress 解压数据（使用对象池）
func (c *Crypto) decompress(data []byte) ([]byte, error) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()

	return dec.DecodeAll(data, nil)
}

// GetOverhead 计算加密开销（用于预估）
func (c *Crypto) GetOverhead() int {
	return 8 + NonceSize + c.gcm.Overhead() // 时间戳 + nonce + tag
}

// Close 清理资源
func (c *Crypto) Close() error {
	return nil
}
