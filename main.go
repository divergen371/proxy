package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// 定数定義
const (
	ProxyPort        = ":10080"
	LogFilePath      = "proxy.log"
	BlockListPath    = "blocked.yaml"
	CacheDirPath     = "cache"
	StatisticsPath   = "stats.json"
	MetricsPort      = ":10081"
	tunnelDirections = 2
	maxCacheSize     = 100 * 1024 * 1024 // 100MB
	maxCacheTime     = 24 * time.Hour
	maxIdleConns     = 100
	idleTimeout      = 90 * time.Second
	statsInterval    = 10 * time.Second
)

// BlockList はブロックリストの構造を定義
type BlockList struct {
	BlockedIPs     []string `yaml:"blocked_ips"`
	BlockedDomains []string `yaml:"blocked_domains"`
	UpdateInterval string   `yaml:"update_interval,omitempty"`
}

// Statistics はプロキシサーバーの統計情報を保持
type Statistics struct {
	mu                 sync.RWMutex
	StartTime          time.Time     `json:"start_time"`
	CurrentConnections int64         `json:"current_connections"`
	TotalConnections   int64         `json:"total_connections"`
	BytesIn            int64         `json:"bytes_in"`
	BytesOut           int64         `json:"bytes_out"`
	RequestCount       int64         `json:"request_count"`
	CacheHits          int64         `json:"cache_hits"`
	CacheMisses        int64         `json:"cache_misses"`
	AverageLatency     float64       `json:"average_latency"`
	BlockedRequests    int64         `json:"blocked_requests"`
	Errors             int64         `json:"errors"`
	LatencyBuckets     map[int]int64 `json:"latency_buckets"`
	StatusCodes        map[int]int64 `json:"status_codes"`
	MemoryStats        *MemoryStats  `json:"memory_stats"`
}

// MemoryStats はメモリ使用状況の統計を保持
type MemoryStats struct {
	Alloc       uint64 `json:"alloc"`
	TotalAlloc  uint64 `json:"total_alloc"`
	Sys         uint64 `json:"sys"`
	NumGC       uint32 `json:"num_gc"`
	HeapObjects uint64 `json:"heap_objects"`
	GCPauseNs   uint64 `json:"gc_pause_ns"`
}

// CacheEntry はキャッシュエントリの構造を定義
type CacheEntry struct {
	Data       []byte
	Timestamp  time.Time
	Etag       string
	Headers    http.Header
	Compressed bool
}

// LogEntry はアクセスログの1エントリを表す
type LogEntry struct {
	Time       time.Time
	ClientIP   string
	Method     string
	Host       string
	URI        string
	Status     int
	BytesIn    int64
	BytesOut   int64
	Duration   float64
	Blocked    bool
	Cached     bool
	Compressed bool
}

// Monitor はモニタリング機能を提供
type Monitor struct {
	stats *Statistics
	proxy *Proxy
	done  chan bool
}

// Cache はキャッシュ機能を提供
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry
	size    int64
	baseDir string
}

// Logger はロギング機能を提供
type Logger struct {
	mu     sync.Mutex
	file   *os.File
	logger *log.Logger
}

// AccessControl はアクセス制御機能を提供
type AccessControl struct {
	mu             sync.RWMutex
	blockedIPs     map[string]bool
	blockedDomains map[string]bool
	configPath     string
}

// ConnectionPool は接続プールを管理
type ConnectionPool struct {
	mu      sync.Mutex
	conns   map[string][]*pooledConn
	maxIdle int
	timeout time.Duration
}

// pooledConn はプール内の接続を表す
type pooledConn struct {
	conn     net.Conn
	lastUsed time.Time
}

// Proxy はプロキシサーバーの構造体
type Proxy struct {
	logger  *Logger
	ac      *AccessControl
	cache   *Cache
	pool    *ConnectionPool
	monitor *Monitor
}

// NewLogger は新しいLoggerインスタンスを作成
func NewLogger(filename string) (*Logger, error) {
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %v", err)
	}

	return &Logger{
		file:   file,
		logger: log.New(file, "", log.Ldate|log.Ltime),
	}, nil
}

// Log はアクセスログを記録
func (l *Logger) Log(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	message := fmt.Sprintf(
		"%s %s %s %s %d %d bytes in %d bytes out %.3f seconds blocked=%v cached=%v compressed=%v",
		entry.ClientIP,
		entry.Method,
		entry.Host,
		entry.URI,
		entry.Status,
		entry.BytesIn,
		entry.BytesOut,
		entry.Duration,
		entry.Blocked,
		entry.Cached,
		entry.Compressed,
	)

	l.logger.Println(message)
}

// Close はロガーのリソースを解放
func (l *Logger) Close() error {
	return l.file.Close()
}

// NewAccessControl は新しいAccessControlインスタンスを作成
func NewAccessControl(configPath string) (*AccessControl, error) {
	ac := &AccessControl{
		blockedIPs:     make(map[string]bool),
		blockedDomains: make(map[string]bool),
		configPath:     configPath,
	}

	if err := ac.loadBlockList(); err != nil {
		return nil, err
	}

	go ac.watchConfig()

	return ac, nil
}

// loadBlockList はブロックリストをYAMLファイルから読み込む
func (ac *AccessControl) loadBlockList() error {
	data, err := os.ReadFile(ac.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			defaultConfig := BlockList{
				BlockedIPs:     []string{},
				BlockedDomains: []string{},
				UpdateInterval: "1m",
			}
			data, err = yaml.Marshal(defaultConfig)
			if err != nil {
				return fmt.Errorf("failed to create default config: %v", err)
			}
			if err := os.WriteFile(ac.configPath, data, 0644); err != nil {
				return fmt.Errorf("failed to write default config: %v", err)
			}
		} else {
			return fmt.Errorf("failed to read config: %v", err)
		}
	}

	var blockList BlockList
	if err := yaml.Unmarshal(data, &blockList); err != nil {
		return fmt.Errorf("failed to parse config: %v", err)
	}

	newBlockedIPs := make(map[string]bool)
	newBlockedDomains := make(map[string]bool)

	for _, ip := range blockList.BlockedIPs {
		newBlockedIPs[strings.TrimSpace(ip)] = true
	}

	for _, domain := range blockList.BlockedDomains {
		newBlockedDomains[strings.ToLower(strings.TrimSpace(domain))] = true
	}

	ac.mu.Lock()
	ac.blockedIPs = newBlockedIPs
	ac.blockedDomains = newBlockedDomains
	ac.mu.Unlock()

	return nil
}

// watchConfig は設定ファイルの変更を監視
func (ac *AccessControl) watchConfig() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	var lastModTime time.Time
	for range ticker.C {
		stat, err := os.Stat(ac.configPath)
		if err != nil {
			fmt.Printf("Error checking config file: %v\n", err)
			continue
		}

		if stat.ModTime().After(lastModTime) {
			if err := ac.loadBlockList(); err != nil {
				fmt.Printf("Error reloading config: %v\n", err)
				continue
			}
			lastModTime = stat.ModTime()
			fmt.Println("Config reloaded successfully")
		}
	}
}

// isBlocked は指定されたIPアドレスまたはドメインがブロックされているか確認
func (ac *AccessControl) isBlocked(ip, domain string) bool {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	if ac.blockedIPs[ip] {
		return true
	}

	domain = strings.ToLower(domain)
	if ac.blockedDomains[domain] {
		return true
	}

	parts := strings.Split(domain, ".")
	for i := 0; i < len(parts)-1; i++ {
		wildcard := "*." + strings.Join(parts[i+1:], ".")
		if ac.blockedDomains[wildcard] {
			return true
		}
	}

	return false
}

// NewCache は新しいCacheインスタンスを作成
func NewCache(baseDir string) (*Cache, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, err
	}

	return &Cache{
		entries: make(map[string]*CacheEntry),
		baseDir: baseDir,
	}, nil
}

// getFilePath はキャッシュファイルのパスを生成
func (c *Cache) getFilePath(key string) string {
	hash := sha256.Sum256([]byte(key))
	return filepath.Join(c.baseDir, fmt.Sprintf("%x", hash))
}

// Set はキャッシュにデータを保存
func (c *Cache) Set(key string, data []byte, headers http.Header) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var compressed []byte
	var isCompressed bool
	if len(data) > 1024 {
		buf := &bytes.Buffer{}
		gz := gzip.NewWriter(buf)
		if _, err := gz.Write(data); err != nil {
			return err
		}
		if err := gz.Close(); err != nil {
			return err
		}
		compressed = buf.Bytes()
		if len(compressed) < len(data) {
			data = compressed
			isCompressed = true
		}
	}

	if c.size+int64(len(data)) > maxCacheSize {
		c.cleanup()
	}

	entry := &CacheEntry{
		Data:       data,
		Timestamp:  time.Now(),
		Etag:       fmt.Sprintf("\"%x\"", sha256.Sum256(data)),
		Headers:    headers.Clone(),
		Compressed: isCompressed,
	}

	if err := os.WriteFile(c.getFilePath(key), data, 0644); err != nil {
		return err
	}

	c.entries[key] = entry
	c.size += int64(len(data))
	return nil
}

// Get はキャッシュからデータを取得
func (c *Cache) Get(key string) (*CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}

	if time.Since(entry.Timestamp) > maxCacheTime {
		go c.Remove(key)
		return nil, false
	}

	return entry, true
}

// Remove はキャッシュからエントリを削除
func (c *Cache) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, exists := c.entries[key]; exists {
		c.size -= int64(len(entry.Data))
		delete(c.entries, key)
		os.Remove(c.getFilePath(key))
	}
}

// cleanup は古いキャッシュエントリを削除
func (c *Cache) cleanup() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range c.entries {
		if oldestKey == "" || entry.Timestamp.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.Timestamp
		}
	}

	if oldestKey != "" {
		c.Remove(oldestKey)
	}
}

// NewConnectionPool は新しい接続プールを作成
func NewConnectionPool(maxIdle int, timeout time.Duration) *ConnectionPool {
	pool := &ConnectionPool{
		conns:   make(map[string][]*pooledConn),
		maxIdle: maxIdle,
		timeout: timeout,
	}

	go func() {
		for {
			time.Sleep(timeout / 2)
			pool.cleanup()
		}
	}()

	return pool
}

// Get は接続プールから接続を取得または新規作成
func (p *ConnectionPool) Get(addr string) (net.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	conns := p.conns[addr]
	for i := len(conns) - 1; i >= 0; i-- {
		pc := conns[i]
		if time.Since(pc.lastUsed) > p.timeout {
			pc.conn.Close()
			conns = append(conns[:i], conns[i+1:]...)
			continue
		}
		p.conns[addr] = conns[:i]
		return pc.conn, nil
	}

	return net.Dial("tcp", addr)
}

// Put は使用済みの接続をプールに返却
func (p *ConnectionPool) Put(addr string, conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	conns := p.conns[addr]
	if len(conns) >= p.maxIdle {
		conn.Close()
		return
	}

	p.conns[addr] = append(conns, &pooledConn{
		conn:     conn,
		lastUsed: time.Now(),
	})
}

// cleanup は古い接続を削除
func (p *ConnectionPool) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for addr, conns := range p.conns {
		var active []*pooledConn
		for _, pc := range conns {
			if time.Since(pc.lastUsed) > p.timeout {
				pc.conn.Close()
			} else {
				active = append(active, pc)
			}
		}
		if len(active) == 0 {
			delete(p.conns, addr)
		} else {
			p.conns[addr] = active
		}
	}
}

// NewMonitor は新しいMonitorインスタンスを作成
func NewMonitor(proxy *Proxy) *Monitor {
	return &Monitor{
		stats: &Statistics{
			StartTime:      time.Now(),
			LatencyBuckets: make(map[int]int64),
			StatusCodes:    make(map[int]int64),
			MemoryStats:    &MemoryStats{},
		},
		proxy: proxy,
		done:  make(chan bool),
	}
}

// Start はモニタリングを開始
func (m *Monitor) Start() {
	go m.startMetricsServer()
	go m.periodicStatsUpdate()
}

// Stop はモニタリングを停止
func (m *Monitor) Stop() {
	m.done <- true
}

// recordLatency はレイテンシーを記録
func (m *Monitor) recordLatency(duration time.Duration) {
	ms := int(duration.Milliseconds())
	bucket := ms - (ms % 100) // 100msごとのバケット

	m.stats.mu.Lock()
	defer m.stats.mu.Unlock()

	m.stats.LatencyBuckets[bucket]++

	// 平均レイテンシーの更新
	count := int64(0)
	sum := int64(0)
	for bucket, c := range m.stats.LatencyBuckets {
		sum += int64(bucket) * c
		count += c
	}
	if count > 0 {
		m.stats.AverageLatency = float64(sum) / float64(count)
	}
}

// startMetricsServer はメトリクス用のHTTPサーバーを起動
func (m *Monitor) startMetricsServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", m.handleMetrics)
	mux.HandleFunc("/stats", m.handleStats)

	server := &http.Server{
		Addr:    MetricsPort,
		Handler: mux,
	}

	fmt.Printf("Metrics server listening on port %s\n", MetricsPort)
	if err := server.ListenAndServe(); err != nil {
		fmt.Printf("Metrics server error: %v\n", err)
	}
}

// handleMetrics はPrometheus形式のメトリクスを提供
func (m *Monitor) handleMetrics(w http.ResponseWriter, r *http.Request) {
	m.stats.mu.RLock()
	defer m.stats.mu.RUnlock()

	fmt.Fprintf(w, "# TYPE proxy_current_connections gauge\n")
	fmt.Fprintf(w, "proxy_current_connections %d\n", m.stats.CurrentConnections)
	fmt.Fprintf(w, "# TYPE proxy_total_connections counter\n")
	fmt.Fprintf(w, "proxy_total_connections %d\n", m.stats.TotalConnections)
	fmt.Fprintf(w, "# TYPE proxy_bytes_transferred counter\n")
	fmt.Fprintf(w, "proxy_bytes_in %d\n", m.stats.BytesIn)
	fmt.Fprintf(w, "proxy_bytes_out %d\n", m.stats.BytesOut)
	fmt.Fprintf(w, "# TYPE proxy_requests counter\n")
	fmt.Fprintf(w, "proxy_requests_total %d\n", m.stats.RequestCount)
	fmt.Fprintf(w, "# TYPE proxy_cache_operations counter\n")
	fmt.Fprintf(w, "proxy_cache_hits %d\n", m.stats.CacheHits)
	fmt.Fprintf(w, "proxy_cache_misses %d\n", m.stats.CacheMisses)
}

// handleStats はJSON形式の詳細な統計情報を提供
func (m *Monitor) handleStats(w http.ResponseWriter, r *http.Request) {
	m.stats.mu.RLock()
	defer m.stats.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m.stats)
}

// periodicStatsUpdate は定期的に統計情報を更新
func (m *Monitor) periodicStatsUpdate() {
	ticker := time.NewTicker(statsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.updateMemoryStats()
			m.saveStats()
		case <-m.done:
			return
		}
	}
}

// updateMemoryStats はメモリ使用状況の統計を更新
func (m *Monitor) updateMemoryStats() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	m.stats.mu.Lock()
	defer m.stats.mu.Unlock()

	m.stats.MemoryStats.Alloc = memStats.Alloc
	m.stats.MemoryStats.TotalAlloc = memStats.TotalAlloc
	m.stats.MemoryStats.Sys = memStats.Sys
	m.stats.MemoryStats.NumGC = memStats.NumGC
	m.stats.MemoryStats.HeapObjects = memStats.HeapObjects
	m.stats.MemoryStats.GCPauseNs = memStats.PauseNs[(memStats.NumGC+255)%256]
}

// saveStats は統計情報をファイルに保存
func (m *Monitor) saveStats() {
	m.stats.mu.RLock()
	data, err := json.MarshalIndent(m.stats, "", "  ")
	m.stats.mu.RUnlock()

	if err != nil {
		fmt.Printf("Error marshaling stats: %v\n", err)
		return
	}

	if err := os.WriteFile(StatisticsPath, data, 0644); err != nil {
		fmt.Printf("Error writing stats file: %v\n", err)
	}
}

// NewProxy は新しいProxyインスタンスを作成
func NewProxy(
	logger *Logger, ac *AccessControl, cache *Cache, pool *ConnectionPool,
) *Proxy {
	p := &Proxy{
		logger: logger,
		ac:     ac,
		cache:  cache,
		pool:   pool,
	}
	p.monitor = NewMonitor(p)
	return p
}

// Start はプロキシサーバーを起動
func (p *Proxy) Start() error {
	listener, err := net.Listen("tcp", ProxyPort)
	if err != nil {
		return fmt.Errorf("failed to start server: %v", err)
	}
	defer listener.Close()

	fmt.Printf("Proxy server listening on port %s\n", ProxyPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("Error accepting connection: %v\n", err)
			continue
		}
		go p.handleConnection(conn)
	}
}

// handleConnection はクライアントとの接続を処理
func (p *Proxy) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	atomic.AddInt64(&p.monitor.stats.CurrentConnections, 1)
	atomic.AddInt64(&p.monitor.stats.TotalConnections, 1)
	defer atomic.AddInt64(&p.monitor.stats.CurrentConnections, -1)

	startTime := time.Now()
	clientIP := clientConn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(clientIP); err == nil {
		clientIP = host
	}

	reader := bufio.NewReader(clientConn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		fmt.Printf("Error reading request: %v\n", err)
		atomic.AddInt64(&p.monitor.stats.Errors, 1)
		return
	}

	logEntry := LogEntry{
		Time:     startTime,
		ClientIP: clientIP,
		Method:   request.Method,
		Host:     request.Host,
		URI:      request.RequestURI,
	}

	// アクセス制御チェック
	host := request.Host
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	if p.ac.isBlocked(clientIP, host) {
		logEntry.Status = http.StatusForbidden
		logEntry.Blocked = true
		atomic.AddInt64(&p.monitor.stats.BlockedRequests, 1)
		p.logger.Log(logEntry)

		response := &http.Response{
			Status:     "403 Forbidden",
			StatusCode: http.StatusForbidden,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Body:       io.NopCloser(strings.NewReader("Access Denied")),
		}
		response.Write(clientConn)
		return
	}

	if request.Method == http.MethodConnect {
		p.handleTunneling(clientConn, request, &logEntry)
	} else {
		p.handleHTTP(clientConn, request, &logEntry)
	}
}

// handleHTTP はHTTPリクエストを処理
func (p *Proxy) handleHTTP(
	clientConn net.Conn, request *http.Request, logEntry *LogEntry,
) {
	atomic.AddInt64(&p.monitor.stats.RequestCount, 1)
	startTime := time.Now()

	// キャッシュキーの生成と確認
	cacheKey := request.Method + " " + request.URL.String()
	if entry, exists := p.cache.Get(cacheKey); exists && request.Method == "GET" {
		atomic.AddInt64(&p.monitor.stats.CacheHits, 1)
		logEntry.Cached = true
		logEntry.Compressed = entry.Compressed
		logEntry.Status = http.StatusOK
		logEntry.BytesOut = int64(len(entry.Data))

		response := &http.Response{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Proto:      request.Proto,
			ProtoMajor: request.ProtoMajor,
			ProtoMinor: request.ProtoMinor,
			Header:     entry.Headers,
			Body:       io.NopCloser(bytes.NewReader(entry.Data)),
		}

		if entry.Compressed {
			response.Header.Set("Content-Encoding", "gzip")
		}
		response.Header.Set("X-Cache", "HIT")
		response.Header.Set("ETag", entry.Etag)

		if err := response.Write(clientConn); err != nil {
			fmt.Printf("Error writing cached response: %v\n", err)
			atomic.AddInt64(&p.monitor.stats.Errors, 1)
		}

		duration := time.Since(startTime)
		logEntry.Duration = duration.Seconds()
		p.monitor.recordLatency(duration)
		p.logger.Log(*logEntry)
		return
	}

	atomic.AddInt64(&p.monitor.stats.CacheMisses, 1)

	// サーバーへの接続
	serverConn, err := p.pool.Get(request.Host)
	if err != nil {
		fmt.Printf("Error connecting to server: %v\n", err)
		logEntry.Status = http.StatusBadGateway
		atomic.AddInt64(&p.monitor.stats.Errors, 1)
		p.logger.Log(*logEntry)
		return
	}
	defer p.pool.Put(request.Host, serverConn)

	if err := request.Write(serverConn); err != nil {
		fmt.Printf("Error writing request to server: %v\n", err)
		logEntry.Status = http.StatusInternalServerError
		atomic.AddInt64(&p.monitor.stats.Errors, 1)
		p.logger.Log(*logEntry)
		return
	}

	response, err := http.ReadResponse(bufio.NewReader(serverConn), request)
	if err != nil {
		fmt.Printf("Error reading response from server: %v\n", err)
		logEntry.Status = http.StatusBadGateway
		atomic.AddInt64(&p.monitor.stats.Errors, 1)
		p.logger.Log(*logEntry)
		return
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		fmt.Printf("Error reading response body: %v\n", err)
		logEntry.Status = http.StatusBadGateway
		atomic.AddInt64(&p.monitor.stats.Errors, 1)
		p.logger.Log(*logEntry)
		return
	}

	if request.Method == "GET" && response.StatusCode == http.StatusOK {
		if err := p.cache.Set(cacheKey, responseBody, response.Header); err != nil {
			fmt.Printf("Error caching response: %v\n", err)
		}
	}

	logEntry.BytesIn = request.ContentLength
	logEntry.BytesOut = int64(len(responseBody))
	atomic.AddInt64(&p.monitor.stats.BytesIn, logEntry.BytesIn)
	atomic.AddInt64(&p.monitor.stats.BytesOut, logEntry.BytesOut)

	response.Body = io.NopCloser(bytes.NewReader(responseBody))
	if err := response.Write(clientConn); err != nil {
		fmt.Printf("Error writing response to client: %v\n", err)
		logEntry.Status = http.StatusInternalServerError
		atomic.AddInt64(&p.monitor.stats.Errors, 1)
		p.logger.Log(*logEntry)
		return
	}

	duration := time.Since(startTime)
	logEntry.Duration = duration.Seconds()
	logEntry.Status = response.StatusCode
	p.monitor.recordLatency(duration)
	p.logger.Log(*logEntry)
}

// handleTunneling はHTTPSトンネリングを処理
func (p *Proxy) handleTunneling(
	clientConn net.Conn, request *http.Request, logEntry *LogEntry,
) {
	atomic.AddInt64(&p.monitor.stats.RequestCount, 1)
	startTime := time.Now()

	serverConn, err := p.pool.Get(request.Host)
	if err != nil {
		fmt.Printf("Error connecting to server: %v\n", err)
		logEntry.Status = http.StatusBadGateway
		atomic.AddInt64(&p.monitor.stats.Errors, 1)
		p.logger.Log(*logEntry)
		return
	}
	defer p.pool.Put(request.Host, serverConn)

	response := &http.Response{
		Status:     "200 Connection established",
		StatusCode: http.StatusOK,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}

	if err := response.Write(clientConn); err != nil {
		fmt.Printf("Error writing response: %v\n", err)
		logEntry.Status = http.StatusInternalServerError
		atomic.AddInt64(&p.monitor.stats.Errors, 1)
		p.logger.Log(*logEntry)
		return
	}

	bytesIn := make(chan int64, 1)
	bytesOut := make(chan int64, 1)
	done := make(chan bool, tunnelDirections)

	go func() {
		n, err := io.Copy(serverConn, clientConn)
		if err != nil {
			fmt.Printf("Error copying client to server: %v\n", err)
		}
		bytesIn <- n
		done <- true
	}()

	go func() {
		n, err := io.Copy(clientConn, serverConn)
		if err != nil {
			fmt.Printf("Error copying server to client: %v\n", err)
		}
		bytesOut <- n
		done <- true
	}()

	<-done
	logEntry.Status = http.StatusOK
	logEntry.BytesIn = <-bytesIn
	logEntry.BytesOut = <-bytesOut
	atomic.AddInt64(&p.monitor.stats.BytesIn, logEntry.BytesIn)
	atomic.AddInt64(&p.monitor.stats.BytesOut, logEntry.BytesOut)

	duration := time.Since(startTime)
	logEntry.Duration = duration.Seconds()
	p.monitor.recordLatency(duration)
	p.logger.Log(*logEntry)
}

func main() {
	// 作業ディレクトリの取得
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Printf("Failed to get working directory: %v\n", err)
		return
	}

	// 各種パスの設定
	logPath := filepath.Join(workDir, LogFilePath)
	blockListPath := filepath.Join(workDir, BlockListPath)
	cachePath := filepath.Join(workDir, CacheDirPath)

	// ロガーの初期化
	logger, err := NewLogger(logPath)
	if err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		return
	}
	defer logger.Close()

	// アクセス制御の初期化
	ac, err := NewAccessControl(blockListPath)
	if err != nil {
		fmt.Printf("Failed to initialize access control: %v\n", err)
		return
	}

	// キャッシュの初期化
	cache, err := NewCache(cachePath)
	if err != nil {
		fmt.Printf("Failed to initialize cache: %v\n", err)
		return
	}

	// 接続プールの初期化
	pool := NewConnectionPool(maxIdleConns, idleTimeout)

	// プロキシサーバーの作成と起動
	proxy := NewProxy(logger, ac, cache, pool)
	proxy.monitor.Start()

	if err := proxy.Start(); err != nil {
		fmt.Printf("Failed to start proxy: %v\n", err)
		return
	}
}
