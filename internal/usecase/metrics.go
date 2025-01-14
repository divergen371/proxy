package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"proxy/internal/domain"
)

// MetricsUseCase はメトリクス関連のユースケースを実装
type MetricsUseCase struct {
	metrics      domain.MetricsCollector
	logger       domain.Logger
	saveInterval time.Duration
	done         chan struct{}
}

// MetricsConfig はメトリクスの設定を表す
type MetricsConfig struct {
	SaveInterval time.Duration
	MetricsFile  string
}

// MetricsSnapshot はメトリクスのスナップショットを表す
type MetricsSnapshot struct {
	Timestamp          time.Time `json:"timestamp"`
	StartTime          time.Time `json:"start_time"`
	CurrentConnections int64     `json:"current_connections"`
	TotalRequests      int64     `json:"total_requests"`
	BytesTransferred   int64     `json:"bytes_transferred"`
	CacheHits          int64     `json:"cache_hits"`
	CacheMisses        int64     `json:"cache_misses"`
	BlockedRequests    int64     `json:"blocked_requests"`
	Errors             int64     `json:"errors"`
	Uptime             string    `json:"uptime"`
}

// NewMetricsUseCase は新しいMetricsUseCaseインスタンスを作成
func NewMetricsUseCase(
	metrics domain.MetricsCollector, logger domain.Logger, config MetricsConfig,
) *MetricsUseCase {
	if config.SaveInterval == 0 {
		config.SaveInterval = 1 * time.Minute
	}

	uc := &MetricsUseCase{
		metrics:      metrics,
		logger:       logger,
		saveInterval: config.SaveInterval,
		done:         make(chan struct{}),
	}

	go uc.startPeriodicSave()
	return uc
}

// Start はメトリクス収集を開始
func (uc *MetricsUseCase) Start() error {
	uc.logger.Info("Starting metrics collection", map[string]interface{}{
		"save_interval": uc.saveInterval.String(),
	})
	return nil
}

// Stop はメトリクス収集を停止
func (uc *MetricsUseCase) Stop() error {
	uc.logger.Info("Stopping metrics collection", nil)
	close(uc.done)
	return nil
}

// startPeriodicSave は定期的なメトリクス保存を開始
func (uc *MetricsUseCase) startPeriodicSave() {
	ticker := time.NewTicker(uc.saveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := uc.saveMetrics(); err != nil {
				uc.logger.Error("Failed to save metrics", err, nil)
			}
		case <-uc.done:
			uc.logger.Info("Stopping periodic metrics save", nil)
			return
		}
	}
}

// saveMetrics は現在のメトリクスを保存
func (uc *MetricsUseCase) saveMetrics() error {
	snapshot, err := uc.GetMetricsSnapshot()
	if err != nil {
		return fmt.Errorf("failed to get metrics snapshot: %v", err)
	}

	// メトリクスの保存処理をリポジトリに委譲
	if saver, ok := uc.metrics.(interface {
		SaveMetrics(*domain.MetricsSnapshot) error
	}); ok {
		return saver.SaveMetrics(snapshot)
	}

	return nil
}

// GetMetricsSnapshot は現在のメトリクスのスナップショットを取得
func (uc *MetricsUseCase) GetMetricsSnapshot() (
	*domain.MetricsSnapshot, error,
) {
	// メトリクスコレクターからデータを取得
	data := uc.metrics.GetSnapshot()

	// データをMetricsSnapshotに変換
	snapshot := &domain.MetricsSnapshot{
		Timestamp:          time.Now(),
		StartTime:          data["start_time"].(time.Time),
		CurrentConnections: data["current_connections"].(int64),
		TotalRequests:      data["total_requests"].(int64),
		BytesTransferred:   data["bytes_transferred"].(int64),
		CacheHits:          data["cache_hits"].(int64),
		CacheMisses:        data["cache_misses"].(int64),
		BlockedRequests:    data["blocked_requests"].(int64),
		Errors:             data["errors"].(int64),
		Uptime:             data["uptime"].(string),
	}

	return snapshot, nil
}

// GetPrometheusMetrics はPrometheus形式のメトリクスを取得
func (uc *MetricsUseCase) GetPrometheusMetrics(ctx context.Context) (
	string, error,
) {
	snapshot, err := uc.GetMetricsSnapshot()
	if err != nil {
		return "", err
	}

	return snapshot.ToPrometheusFormat(), nil
}

// formatPrometheusMetrics はメトリクスをPrometheus形式にフォーマット
func formatPrometheusMetrics(snapshot *MetricsSnapshot) string {
	var metrics []string

	// 各メトリクスの追加
	metrics = append(metrics,
		fmt.Sprintf("# HELP proxy_current_connections Current number of active connections\n"+
			"# TYPE proxy_current_connections gauge\n"+
			"proxy_current_connections %d", snapshot.CurrentConnections),

		fmt.Sprintf("# HELP proxy_total_requests Total number of processed requests\n"+
			"# TYPE proxy_total_requests counter\n"+
			"proxy_total_requests %d", snapshot.TotalRequests),

		fmt.Sprintf("# HELP proxy_bytes_transferred Total number of bytes transferred\n"+
			"# TYPE proxy_bytes_transferred counter\n"+
			"proxy_bytes_transferred %d", snapshot.BytesTransferred),

		fmt.Sprintf("# HELP proxy_cache_hits Total number of cache hits\n"+
			"# TYPE proxy_cache_hits counter\n"+
			"proxy_cache_hits %d", snapshot.CacheHits),

		fmt.Sprintf("# HELP proxy_cache_misses Total number of cache misses\n"+
			"# TYPE proxy_cache_misses counter\n"+
			"proxy_cache_misses %d", snapshot.CacheMisses),

		fmt.Sprintf("# HELP proxy_blocked_requests Total number of blocked requests\n"+
			"# TYPE proxy_blocked_requests counter\n"+
			"proxy_blocked_requests %d", snapshot.BlockedRequests),

		fmt.Sprintf("# HELP proxy_errors Total number of errors\n"+
			"# TYPE proxy_errors counter\n"+
			"proxy_errors %d", snapshot.Errors),
	)

	return strings.Join(metrics, "\n\n") + "\n"
}
