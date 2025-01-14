package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"proxy/internal/interface/handler"
	"proxy/internal/interface/repository/access"
	"proxy/internal/interface/repository/cache"
	"proxy/internal/interface/repository/logger"
	"proxy/internal/interface/repository/metrics"
	"proxy/internal/usecase"
)

const (
	defaultPort        = 10080
	defaultMetricsPort = 10081
	defaultConfigDir   = "./configs"
	defaultLogDir      = "./logs"
	defaultCacheDir    = "./cache"
)

type config struct {
	port                int
	metricsPort         int
	configDir           string
	logDir              string
	cacheDir            string
	maxCacheSize        int64
	maxConnections      int
	metricsSaveInterval time.Duration
}

func main() {
	// コンフィグの解析
	cfg := parseConfig()

	// ディレクトリの準備
	if err := prepareDirectories(cfg); err != nil {
		fmt.Printf("Failed to prepare directories: %v\n", err)
		os.Exit(1)
	}

	// ロガーの初期化
	loggerRepo, err := logger.New(
		cfg.logDir,
		"proxy.log",
		logger.DefaultRotationConfig(),
	)
	if err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer loggerRepo.Close()

	// アクセス制御の初期化
	accessController := access.New(filepath.Join(cfg.configDir, "blocked.yaml"))

	// キャッシュの初期化
	cacheManager, err := cache.New(cfg.cacheDir, cfg.maxCacheSize)
	if err != nil {
		loggerRepo.Error("Failed to initialize cache", err, nil)
		os.Exit(1)
	}

	// メトリクスの初期化
	metricsCollector := metrics.New(filepath.Join(cfg.logDir, "metrics.json"))

	// プロキシのユースケース作成
	proxyUseCase := usecase.NewProxyUseCase(
		accessController, // domain.AccessController
		cacheManager,     // domain.CacheManager
		metricsCollector, // domain.MetricsCollector
		loggerRepo,       // domain.Logger
	)

	// メトリクスのユースケース作成
	metricsUseCase := usecase.NewMetricsUseCase(
		metricsCollector,
		loggerRepo,
		usecase.MetricsConfig{
			SaveInterval: cfg.metricsSaveInterval,
			MetricsFile:  filepath.Join(cfg.logDir, "metrics.json"),
		},
	)

	// ハンドラーの作成
	proxyHandler := handler.NewProxyHandler(proxyUseCase, loggerRepo)
	metricsHandler := handler.NewMetricsHandler(metricsUseCase, loggerRepo)

	// プロキシサーバーの設定
	proxyServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.port),
		Handler: proxyHandler,
	}

	// メトリクスサーバーの設定
	metricsServer := &http.Server{
		Addr: fmt.Sprintf(":%d", cfg.metricsPort),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/metrics":
				metricsHandler.HandleMetrics(w, r)
			case "/stats":
				metricsHandler.HandleStats(w, r)
			case "/health":
				metricsHandler.HandleHealth(w, r)
			default:
				http.NotFound(w, r)
			}
		}),
	}

	// シャットダウンハンドラの設定
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	// サーバーの起動
	go func() {
		loggerRepo.Info("Starting proxy server", map[string]interface{}{"port": cfg.port})
		if err := proxyServer.ListenAndServe(); err != http.ErrServerClosed {
			loggerRepo.Error("Proxy server error", err, nil)
			cancel()
		}
	}()

	go func() {
		loggerRepo.Info("Starting metrics server", map[string]interface{}{"port": cfg.metricsPort})
		if err := metricsServer.ListenAndServe(); err != http.ErrServerClosed {
			loggerRepo.Error("Metrics server error", err, nil)
			cancel()
		}
	}()

	// シグナル待機
	select {
	case <-signalChan:
		loggerRepo.Info("Shutdown signal received", nil)
	case <-ctx.Done():
		loggerRepo.Info("Shutdown initiated", nil)
	}

	// グレースフルシャットダウン
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// プロキシサーバーのシャットダウン
	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		loggerRepo.Error("Error shutting down proxy server", err, nil)
	}

	// メトリクスサーバーのシャットダウン
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		loggerRepo.Error("Error shutting down metrics server", err, nil)
	}

	loggerRepo.Info("Shutdown complete", nil)
}

func parseConfig() *config {
	cfg := &config{}

	flag.IntVar(&cfg.port, "port", defaultPort, "Proxy server port")
	flag.IntVar(&cfg.metricsPort, "metrics-port", defaultMetricsPort, "Metrics server port")
	flag.StringVar(&cfg.configDir, "config-dir", defaultConfigDir, "Configuration directory")
	flag.StringVar(&cfg.logDir, "log-dir", defaultLogDir, "Log directory")
	flag.StringVar(&cfg.cacheDir, "cache-dir", defaultCacheDir, "Cache directory")
	flag.Int64Var(&cfg.maxCacheSize, "max-cache-size", 100*1024*1024, "Maximum cache size in bytes")
	flag.IntVar(&cfg.maxConnections, "max-connections", 1000, "Maximum number of concurrent connections")
	flag.DurationVar(&cfg.metricsSaveInterval, "metrics-save-interval", time.Minute, "Metrics save interval")

	flag.Parse()

	return cfg
}

func prepareDirectories(cfg *config) error {
	dirs := []string{
		cfg.configDir,
		cfg.logDir,
		cfg.cacheDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %v", dir, err)
		}
	}

	return nil
}
