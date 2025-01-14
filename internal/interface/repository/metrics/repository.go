package metrics

import (
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"proxy/internal/domain"
)

// Repository はメトリクスのリポジトリ実装
type Repository struct {
	mu          sync.RWMutex
	metricsFile string
	startTime   time.Time
	connections int64
	requests    int64
	bytes       int64
	cacheHits   int64
	cacheMisses int64
	blocked     int64
	errors      int64
}

// インターフェースの実装を検証
var _ domain.MetricsCollector = (*Repository)(nil)

// New は新しいRepositoryインスタンスを作成
func New(metricsFile string) domain.MetricsCollector {
	return &Repository{
		metricsFile: metricsFile,
		startTime:   time.Now(),
	}
}

// SaveMetrics はメトリクスをファイルに保存
func (r *Repository) SaveMetrics(snapshot *domain.MetricsSnapshot) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	tempFile := r.metricsFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return err
	}

	return os.Rename(tempFile, r.metricsFile)
}

// 以下、MetricsCollector インターフェースの実装
func (r *Repository) IncrementConnections() {
	atomic.AddInt64(&r.connections, 1)
}

func (r *Repository) DecrementConnections() {
	atomic.AddInt64(&r.connections, -1)
}

func (r *Repository) AddBytesTransferred(bytes int64) {
	atomic.AddInt64(&r.bytes, bytes)
}

func (r *Repository) RecordRequest() {
	atomic.AddInt64(&r.requests, 1)
}

func (r *Repository) RecordCacheHit() {
	atomic.AddInt64(&r.cacheHits, 1)
}

func (r *Repository) RecordCacheMiss() {
	atomic.AddInt64(&r.cacheMisses, 1)
}

func (r *Repository) RecordBlockedRequest() {
	atomic.AddInt64(&r.blocked, 1)
}

func (r *Repository) RecordError() {
	atomic.AddInt64(&r.errors, 1)
}

func (r *Repository) GetSnapshot() map[string]interface{} {
	return map[string]interface{}{
		"timestamp":           time.Now(),
		"start_time":          r.startTime,
		"current_connections": atomic.LoadInt64(&r.connections),
		"total_requests":      atomic.LoadInt64(&r.requests),
		"bytes_transferred":   atomic.LoadInt64(&r.bytes),
		"cache_hits":          atomic.LoadInt64(&r.cacheHits),
		"cache_misses":        atomic.LoadInt64(&r.cacheMisses),
		"blocked_requests":    atomic.LoadInt64(&r.blocked),
		"errors":              atomic.LoadInt64(&r.errors),
		"uptime":              time.Since(r.startTime).String(),
	}
}
