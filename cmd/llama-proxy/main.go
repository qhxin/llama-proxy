package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/user/llama-proxy/internal/client"
	"github.com/user/llama-proxy/internal/config"
	"github.com/user/llama-proxy/internal/logger"
	"github.com/user/llama-proxy/internal/server"
)

var (
	// 版本信息
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"

	// 全局配置
	configPath string
	rootCmd    *cobra.Command
)

func main() {
	if err := execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func execute() error {
	rootCmd = &cobra.Command{
		Use:   "llama-proxy",
		Short: "WebSocket代理程序，用于云端访问本地llama-server",
		Long: `llama-proxy 是一个WebSocket代理程序，用于实现云端AI应用
通过加密压缩通道访问本地llama-server大模型。

支持服务端和客户端两种模式，支持流式和非流式请求。`,
		Version: fmt.Sprintf("%s (build: %s, commit: %s)", Version, BuildTime, GitCommit),
	}

	// 全局标志
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "配置文件路径")

	// 添加子命令
	rootCmd.AddCommand(serverCmd())
	rootCmd.AddCommand(clientCmd())
	rootCmd.AddCommand(configCmd())
	rootCmd.AddCommand(versionCmd())

	return rootCmd.Execute()
}

// serverCmd 服务端命令
func serverCmd() *cobra.Command {
	var (
		listen           string
		wsPort           string
		key              string
		maxConcurrent    int
		compressionLevel int
	)

	cmd := &cobra.Command{
		Use:   "server",
		Short: "以服务端模式运行",
		Long:  `启动服务端，监听HTTP API请求和WebSocket连接。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// 加载配置
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			// 命令行参数覆盖配置
			if cmd.Flags().Changed("listen") {
				cfg.Server.Listen = listen
			}
			if cmd.Flags().Changed("ws-port") {
				cfg.Server.WSPort = wsPort
			}
			if cmd.Flags().Changed("key") {
				cfg.Server.Key = key
			}
			if cmd.Flags().Changed("max-concurrent") {
				cfg.Server.MaxConcurrentRequests = maxConcurrent
			}
			if cmd.Flags().Changed("compression-level") {
				cfg.Server.CompressionLevel = compressionLevel
			}

			// 验证密钥
			if cfg.Server.Key == "" {
				return fmt.Errorf("请设置加密密钥：使用 --key 参数或配置文件")
			}

			// 创建日志
			log := logger.New(&cfg.Log)
			log.Infof("Starting llama-proxy server v%s", Version)
			log.Infof("Listening on %s", cfg.Server.Listen)

			// 创建服务端
			srv, err := server.New(&cfg.Server, log)
			if err != nil {
				return fmt.Errorf("failed to create server: %w", err)
			}

			// 设置信号处理
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

			// 启动服务端
			go func() {
				if err := srv.Start(); err != nil {
					log.Errorf("Server error: %v", err)
					sigChan <- syscall.SIGTERM
				}
			}()

			// 等待退出信号
			<-sigChan
			log.Info("Received shutdown signal")

			// 优雅关闭
			return srv.Stop()
		},
	}

	// 命令行参数
	cmd.Flags().StringVarP(&listen, "listen", "l", ":8080", "HTTP监听地址")
	cmd.Flags().StringVar(&wsPort, "ws-port", ":8081", "WebSocket监听地址")
	cmd.Flags().StringVarP(&key, "key", "k", "", "加密密钥（至少16字符）")
	cmd.Flags().IntVar(&maxConcurrent, "max-concurrent", 50, "最大并发请求数")
	cmd.Flags().IntVar(&compressionLevel, "compression-level", 15, "zstd压缩级别(1-22)")

	return cmd
}

// clientCmd 客户端命令
func clientCmd() *cobra.Command {
	var (
		serverWS         string
		llamaURL         string
		key              string
		maxConcurrent    int
		compressionLevel int
	)

	cmd := &cobra.Command{
		Use:   "client",
		Short: "以客户端模式运行",
		Long:  `启动客户端，连接到服务端并转发请求到本地llama-server。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// 加载配置
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			// 命令行参数覆盖配置
			if cmd.Flags().Changed("server") {
				cfg.Client.ServerWS = serverWS
			}
			if cmd.Flags().Changed("llama") {
				cfg.Client.LlamaURL = llamaURL
			}
			if cmd.Flags().Changed("key") {
				cfg.Client.Key = key
			}
			if cmd.Flags().Changed("max-concurrent") {
				cfg.Client.MaxConcurrentRequests = maxConcurrent
			}
			if cmd.Flags().Changed("compression-level") {
				cfg.Client.CompressionLevel = compressionLevel
			}

			// 验证配置
			if cfg.Client.Key == "" {
				return fmt.Errorf("请设置加密密钥：使用 --key 参数或配置文件")
			}
			if cfg.Client.ServerWS == "" {
				return fmt.Errorf("请设置服务端地址：使用 --server 参数或配置文件")
			}
			if cfg.Client.LlamaURL == "" {
				return fmt.Errorf("请设置llama-server地址：使用 --llama 参数或配置文件")
			}

			// 创建日志
			log := logger.New(&cfg.Log)
			log.Infof("Starting llama-proxy client v%s", Version)
			log.Infof("Server: %s, Llama: %s", cfg.Client.ServerWS, cfg.Client.LlamaURL)

			// 创建客户端
			cli, err := client.New(&cfg.Client, log)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// 设置信号处理
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

			// 启动客户端
			if err := cli.Start(); err != nil {
				return fmt.Errorf("failed to start client: %w", err)
			}

			log.Info("Client started successfully")

			// 等待退出信号
			<-sigChan
			log.Info("Received shutdown signal")

			// 优雅关闭
			return cli.Stop()
		},
	}

	// 命令行参数
	cmd.Flags().StringVarP(&serverWS, "server", "s", "ws://localhost:8081", "服务端WebSocket地址")
	cmd.Flags().StringVar(&llamaURL, "llama", "http://127.0.0.1:8080", "本地llama-server地址")
	cmd.Flags().StringVarP(&key, "key", "k", "", "加密密钥（至少16字符）")
	cmd.Flags().IntVar(&maxConcurrent, "max-concurrent", 10, "最大并发请求数")
	cmd.Flags().IntVar(&compressionLevel, "compression-level", 15, "zstd压缩级别(1-22)")

	return cmd
}

// configCmd 配置相关命令
func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "配置相关命令",
		Long:  `生成配置文件示例或验证现有配置。`,
	}

	// 生成配置示例
	generateCmd := &cobra.Command{
		Use:   "example",
		Short: "生成配置文件示例",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(config.ExampleConfig())
		},
	}

	// 验证配置
	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "验证配置文件",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			fmt.Println("Configuration is valid")
			return nil
		},
	}

	cmd.AddCommand(generateCmd)
	cmd.AddCommand(validateCmd)

	return cmd
}

// versionCmd 版本命令
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "显示版本信息",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("llama-proxy version %s\n", Version)
			fmt.Printf("  Build time: %s\n", BuildTime)
			fmt.Printf("  Git commit: %s\n", GitCommit)
		},
	}
}

// loadConfig 加载配置
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		// 如果配置文件不存在，使用默认配置
		if os.IsNotExist(err) || configPath == "" {
			return config.DefaultConfig(), nil
		}
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return cfg, nil
}
