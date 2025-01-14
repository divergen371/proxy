package domain

import (
	"fmt"
	"strings"
	"time"
)

// MetricsCollector はメトリクス収集のインターフェース
type MetricsCollector interface {
	IncrementConnections()
	DecrementConnections()
	AddBytesTransferred(bytes int64)
	RecordRequest()
	RecordCacheHit()
	RecordCacheMiss()
	RecordBlockedRequest()
	RecordError()
	GetSnapshot() map[string]interface{}
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

// MetricsFormatter はメトリクスのフォーマット機能を提供するインターフェース
type MetricsFormatter interface {
	ToPrometheusFormat() string
	ToJSON() ([]byte, error)
}

// メトリクスのフォーマット用メソッド
func (ms *MetricsSnapshot) ToPrometheusFormat() string {
	return formatMetricsToPrometheus(ms)
}

// formatMetricsToPrometheus はメトリクスをPrometheus形式にフォーマット
func formatMetricsToPrometheus(ms *MetricsSnapshot) string {
	var metrics []string

	metrics = append(metrics,
		fmt.Sprintf("# HELP proxy_current_connections Current number of active connections\n"+
			"# TYPE proxy_current_connections gauge\n"+
			"proxy_current_connections %d", ms.CurrentConnections),

		fmt.Sprintf("# HELP proxy_total_requests Total number of processed requests\n"+
			"# TYPE proxy_total_requests counter\n"+
			"proxy_total_requests %d", ms.TotalRequests),

		fmt.Sprintf("# HELP proxy_bytes_transferred Total number of bytes transferred\n"+
			"# TYPE proxy_bytes_transferred counter\n"+
			"proxy_bytes_transferred %d", ms.BytesTransferred),

		fmt.Sprintf("# HELP proxy_cache_hits Total number of cache hits\n"+
			"# TYPE proxy_cache_hits counter\n"+
			"proxy_cache_hits %d", ms.CacheHits),

		fmt.Sprintf("# HELP proxy_cache_misses Total number of cache misses\n"+
			"# TYPE proxy_cache_misses counter\n"+
			"proxy_cache_misses %d", ms.CacheMisses),

		fmt.Sprintf("# HELP proxy_blocked_requests Total number of blocked requests\n"+
			"# TYPE proxy_blocked_requests counter\n"+
			"proxy_blocked_requests %d", ms.BlockedRequests),

		fmt.Sprintf("# HELP proxy_errors Total number of errors\n"+
			"# TYPE proxy_errors counter\n"+
			"proxy_errors %d", ms.Errors),
	)

	return strings.Join(metrics, "\n\n") + "\n"
}
