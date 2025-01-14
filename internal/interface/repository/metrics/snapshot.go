package metrics

import (
	"encoding/json"
	"time"
)

// Snapshot はメトリクスのスナップショットを表す.
type Snapshot struct {
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

// ToJSON はスナップショットをJSON形式に変換.
func (s *Snapshot) ToJSON() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// ToPrometheus はスナップショットをPrometheus形式に変換.
func (s *Snapshot) ToPrometheus() string {
	return `# HELP proxy_current_connections Current number of active connections
# TYPE proxy_current_connections gauge
proxy_current_connections ` + formatInt64(s.CurrentConnections) + `
# HELP proxy_total_requests Total number of processed requests
# TYPE proxy_total_requests counter
proxy_total_requests ` + formatInt64(s.TotalRequests) + `
# HELP proxy_bytes_transferred Total number of bytes transferred
# TYPE proxy_bytes_transferred counter
proxy_bytes_transferred ` + formatInt64(s.BytesTransferred) + `
# HELP proxy_cache_hits Total number of cache hits
# TYPE proxy_cache_hits counter
proxy_cache_hits ` + formatInt64(s.CacheHits) + `
# HELP proxy_cache_misses Total number of cache misses
# TYPE proxy_cache_misses counter
proxy_cache_misses ` + formatInt64(s.CacheMisses) + `
# HELP proxy_blocked_requests Total number of blocked requests
# TYPE proxy_blocked_requests counter
proxy_blocked_requests ` + formatInt64(s.BlockedRequests) + `
# HELP proxy_errors Total number of errors
# TYPE proxy_errors counter
proxy_errors ` + formatInt64(s.Errors)
}

// formatInt64 は数値を文字列に変換.
func formatInt64(n int64) string {
	return string(n)
}
