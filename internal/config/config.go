package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// ServerConfig 服务端配置
type ServerConfig struct {
	Listen                string        `mapstructure:"listen"`
	WSPort                string        `mapstructure:"ws_port"`
	Key                   string        `mapstructure:"key"`
	MaxConcurrentRequests int           `mapstructure:"max_concurrent_requests"`
	CompressionLevel      int           `mapstructure:"compression_level"`
	EnableMetrics         bool          `mapstructure:"enable_metrics"`
	MetricsPort           string        `mapstructure:"metrics_port"`
}

// ClientConfig 客户端配置
type ClientConfig struct {
	ServerWS              string        `mapstructure:"server_ws"`
	LlamaURL              string        `mapstructure:"llama_url"`
	Key                   string        `mapstructure:"key"`
	ReconnectInterval     time.Duration `mapstructure:"reconnect_interval"`
	MaxConcurrentRequests int           `mapstructure:"max_concurrent_requests"`
	CompressionLevel      int           `mapstructure:"compression_level"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

// Config 总配置
type Config struct {
	Server ServerConfig `mapstructure:"server"`
	Client ClientConfig `mapstructure:"client"`
	Log    LogConfig    `mapstructure:"log"`
}

// DefaultServerConfig 返回默认服务端配置
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Listen:                ":8080",
		WSPort:                ":8081",
		Key:                   "",
		MaxConcurrentRequests: 50,
		CompressionLevel:      15,
		EnableMetrics:         false,
		MetricsPort:           ":9090",
	}
}

// DefaultClientConfig 返回默认客户端配置
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		ServerWS:              "ws://localhost:8081",
		LlamaURL:              "http://127.0.0.1:8080",
		Key:                   "",
		ReconnectInterval:     5 * time.Second,
		MaxConcurrentRequests: 10,
		CompressionLevel:      15,
	}
}

// DefaultLogConfig 返回默认日志配置
func DefaultLogConfig() LogConfig {
	return LogConfig{
		Level:  "info",
		Format: "text",
		Output: "stdout",
	}
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Server: DefaultServerConfig(),
		Client: DefaultClientConfig(),
		Log:    DefaultLogConfig(),
	}
}

// Load 从文件加载配置（不自动验证，由调用者根据模式验证）
func Load(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	if configPath == "" {
		// 尝试从环境变量获取配置路径
		configPath = os.Getenv("LLAMA_PROXY_CONFIG")
	}

	if configPath != "" {
		viper.SetConfigFile(configPath)
		if err := viper.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}

		if err := viper.Unmarshal(cfg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config: %w", err)
		}
	}

	// 从环境变量覆盖配置
	loadFromEnv(cfg)

	return cfg, nil
}

// loadFromEnv 从环境变量加载配置
func loadFromEnv(cfg *Config) {
	// 服务端配置
	if v := os.Getenv("LLAMA_PROXY_SERVER_LISTEN"); v != "" {
		cfg.Server.Listen = v
	}
	if v := os.Getenv("LLAMA_PROXY_SERVER_WS_PORT"); v != "" {
		cfg.Server.WSPort = v
	}
	if v := os.Getenv("LLAMA_PROXY_SERVER_KEY"); v != "" {
		cfg.Server.Key = v
	}
	if v := os.Getenv("LLAMA_PROXY_SERVER_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Server.MaxConcurrentRequests = n
		}
	}
	if v := os.Getenv("LLAMA_PROXY_SERVER_COMPRESSION"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Server.CompressionLevel = n
		}
	}

	// 客户端配置
	if v := os.Getenv("LLAMA_PROXY_CLIENT_SERVER_WS"); v != "" {
		cfg.Client.ServerWS = v
	}
	if v := os.Getenv("LLAMA_PROXY_CLIENT_LLAMA_URL"); v != "" {
		cfg.Client.LlamaURL = v
	}
	if v := os.Getenv("LLAMA_PROXY_CLIENT_KEY"); v != "" {
		cfg.Client.Key = v
	}
	if v := os.Getenv("LLAMA_PROXY_CLIENT_RECONNECT_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Client.ReconnectInterval = d
		}
	}

	// 日志配置
	if v := os.Getenv("LLAMA_PROXY_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("LLAMA_PROXY_LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
}

// Validate 验证完整配置（服务端+客户端）
func (c *Config) Validate() error {
	if err := c.ValidateServer(); err != nil {
		return err
	}
	if err := c.ValidateClient(); err != nil {
		return err
	}
	return nil
}

// ValidateServer 仅验证服务端配置
func (c *Config) ValidateServer() error {
	if c.Server.Key == "" {
		return fmt.Errorf("server key is required")
	}
	if len(c.Server.Key) < 16 {
		return fmt.Errorf("server key must be at least 16 characters")
	}
	if c.Server.CompressionLevel < 1 || c.Server.CompressionLevel > 22 {
		return fmt.Errorf("server compression level must be between 1 and 22")
	}

	// 日志配置验证
	validLevels := []string{"debug", "info", "warn", "error", "fatal"}
	if !contains(validLevels, strings.ToLower(c.Log.Level)) {
		return fmt.Errorf("invalid log level: %s", c.Log.Level)
	}

	return nil
}

// ValidateClient 仅验证客户端配置
func (c *Config) ValidateClient() error {
	if c.Client.Key == "" {
		return fmt.Errorf("client key is required")
	}
	if len(c.Client.Key) < 16 {
		return fmt.Errorf("client key must be at least 16 characters")
	}
	if c.Client.ServerWS == "" {
		return fmt.Errorf("client server_ws is required")
	}
	if c.Client.LlamaURL == "" {
		return fmt.Errorf("client llama_url is required")
	}
	if c.Client.CompressionLevel < 1 || c.Client.CompressionLevel > 22 {
		return fmt.Errorf("client compression level must be between 1 and 22")
	}

	// 日志配置验证
	validLevels := []string{"debug", "info", "warn", "error", "fatal"}
	if !contains(validLevels, strings.ToLower(c.Log.Level)) {
		return fmt.Errorf("invalid log level: %s", c.Log.Level)
	}

	return nil
}

// GetServerConfig 获取服务端配置
func (c *Config) GetServerConfig() *ServerConfig {
	return &c.Server
}

// GetClientConfig 获取客户端配置
func (c *Config) GetClientConfig() *ClientConfig {
	return &c.Client
}

// contains 检查字符串是否在切片中
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ExampleConfig 返回配置示例
func ExampleConfig() string {
	return `# llama-proxy 配置示例

# 服务端配置
server:
  listen: ":8080"              # HTTP API监听地址
  ws_port: ":8081"              # WebSocket监听地址（可选，不设置则使用listen）
  key: "your-secret-key-here"  # 加密密钥（至少16字符）
  max_concurrent_requests: 50  # 最大并发请求数
  compression_level: 15        # zstd压缩级别（1-22，越大压缩率越高但越慢）
  enable_metrics: false        # 是否启用监控端点
  metrics_port: ":9090"        # 监控端点端口

# 客户端配置
client:
  server_ws: "ws://your-vps-ip:8081"  # 服务端WebSocket地址
  llama_url: "http://127.0.0.1:8080"  # 本地llama-server地址
  key: "your-secret-key-here"         # 加密密钥（必须与服务器相同）
  reconnect_interval: 5s              # 重连间隔
  max_concurrent_requests: 10         # 最大并发请求数
  compression_level: 15              # zstd压缩级别

# 日志配置
log:
  level: "info"     # 日志级别: debug, info, warn, error, fatal
  format: "text"    # 日志格式: text, json
  output: "stdout"  # 日志输出: stdout, stderr, 或文件路径
`
}
