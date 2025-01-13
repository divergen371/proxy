package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// 定数定義
const (
	ProxyPort        = ":10080"
	LogFilePath      = "proxy.log"
	BlockListPath    = "blocked.yaml"
	CacheDirPath     = "cache"
	tunnelDirections = 2
	maxCacheSize     = 100 * 1024 * 1024 // 100MB
	maxCacheTime     = 24 * time.Hour
	maxIdleConns     = 100
	idleTimeout      = 90 * time.Second
)

// BlockList はブロックリストの構造を定義
type BlockList struct {
	BlockedIPs     []string `yaml:"blocked_ips"`
	BlockedDomains []string `yaml:"blocked_domains"`
	UpdateInterval string   `yaml:"update_interval,omitempty"`
}

// CacheEntry はキャッシュエントリの構造を定義
type CacheEntry struct {
	Data       []byte
	Timestamp  time.Time
	Etag       string
	Headers    http.Header
	Compressed bool
}

// Cache はキャッシュ機能を提供する構造体
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry
	size    int64
	baseDir string
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

	// 圧縮の実行
	var compressed []byte
	var isCompressed bool
	if len(data) > 1024 { // 1KB以上のデータのみ圧縮
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

	// キャッシュサイズの確認と古いエントリの削除
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

	// ファイルへの保存
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

	// キャッシュの有効期限チェック
	if time.Since(entry.Timestamp) > maxCacheTime {
		go c.Remove(key) // 非同期で削除
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

	// 最も古いエントリを探す
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

// ConnectionPool は接続プールを管理する構造体
type ConnectionPool struct {
	mu      sync.Mutex
	conns   map[string][]*pooledConn
	maxIdle int
	timeout time.Duration
}

// pooledConn はプール内の接続を表す構造体
type pooledConn struct {
	conn     net.Conn
	lastUsed time.Time
}

// NewConnectionPool は新しい接続プールを作成
func NewConnectionPool(maxIdle int, timeout time.Duration) *ConnectionPool {
	pool := &ConnectionPool{
		conns:   make(map[string][]*pooledConn),
		maxIdle: maxIdle,
		timeout: timeout,
	}

	// 古い接続を定期的にクリーンアップ
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

// LogEntry はアクセスログの1エントリを表す構造体
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

// Logger はロギング機能を提供する構造体
type Logger struct {
	mu     sync.Mutex
	file   *os.File
	logger *log.Logger
}

// AccessControl はアクセス制御機能を提供する構造体
type AccessControl struct {
	mu             sync.RWMutex
	blockedIPs     map[string]bool
	blockedDomains map[string]bool
	configPath     string
}

// NewAccessControl は新しいAccessControlインスタンスを作成する
func NewAccessControl(configPath string) (*AccessControl, error) {
	ac := &AccessControl{
		blockedIPs:     make(map[string]bool),
		blockedDomains: make(map[string]bool),
		configPath:     configPath,
	}

	if err := ac.loadBlockList(); err != nil {
		return nil, err
	}

	// 設定の自動リロードを開始
	go ac.watchConfig()

	return ac, nil
}

// loadBlockList はブロックリストをYAMLファイルから読み込む
func (ac *AccessControl) loadBlockList() error {
	data, err := os.ReadFile(ac.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// 設定ファイルが存在しない場合は、デフォルト設定を作成
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

	// 新しいマップを作成
	newBlockedIPs := make(map[string]bool)
	newBlockedDomains := make(map[string]bool)

	// IPアドレスの登録
	for _, ip := range blockList.BlockedIPs {
		newBlockedIPs[strings.TrimSpace(ip)] = true
	}

	// ドメインの登録
	for _, domain := range blockList.BlockedDomains {
		newBlockedDomains[strings.ToLower(strings.TrimSpace(domain))] = true
	}

	// 既存の設定を更新
	ac.mu.Lock()
	ac.blockedIPs = newBlockedIPs
	ac.blockedDomains = newBlockedDomains
	ac.mu.Unlock()

	return nil
}

// watchConfig は設定ファイルの変更を監視する
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

// isBlocked は指定されたIPアドレスまたはドメインがブロックされているか確認する
func (ac *AccessControl) isBlocked(ip, domain string) bool {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	// IPアドレスのチェック
	if ac.blockedIPs[ip] {
		return true
	}

	// ドメインのチェック
	domain = strings.ToLower(domain)
	if ac.blockedDomains[domain] {
		return true
	}

	// ワイルドカードドメインのチェック
	parts := strings.Split(domain, ".")
	for i := 0; i < len(parts)-1; i++ {
		wildcard := "*." + strings.Join(parts[i+1:], ".")
		if ac.blockedDomains[wildcard] {
			return true
		}
	}

	return false
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

// Proxy はプロキシサーバーの構造体
type Proxy struct {
	logger *Logger
	ac     *AccessControl
	cache  *Cache
	pool   *ConnectionPool
}

// NewProxy は新しいProxyインスタンスを作成
func NewProxy(
	logger *Logger, ac *AccessControl, cache *Cache, pool *ConnectionPool,
) *Proxy {
	return &Proxy{
		logger: logger,
		ac:     ac,
		cache:  cache,
		pool:   pool,
	}
}

// handleConnection はクライアントとの接続を処理
func (p *Proxy) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	startTime := time.Now()
	clientIP := clientConn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(clientIP); err == nil {
		clientIP = host
	}

	reader := bufio.NewReader(clientConn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		fmt.Printf("Error reading request: %v\n", err)
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
	// キャッシュキーの生成
	cacheKey := request.Method + " " + request.URL.String()

	// キャッシュの確認
	if entry, exists := p.cache.Get(cacheKey); exists && request.Method == "GET" {
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
		}
		p.logger.Log(*logEntry)
		return
	}

	// サーバーへの接続
	serverConn, err := p.pool.Get(request.Host)
	if err != nil {
		fmt.Printf("Error connecting to server: %v\n", err)
		logEntry.Status = http.StatusBadGateway
		p.logger.Log(*logEntry)
		return
	}
	defer p.pool.Put(request.Host, serverConn)

	// リクエストの転送
	if err := request.Write(serverConn); err != nil {
		fmt.Printf("Error writing request to server: %v\n", err)
		logEntry.Status = http.StatusInternalServerError
		p.logger.Log(*logEntry)
		return
	}

	// レスポンスの読み取り
	response, err := http.ReadResponse(bufio.NewReader(serverConn), request)
	if err != nil {
		fmt.Printf("Error reading response from server: %v\n", err)
		logEntry.Status = http.StatusBadGateway
		p.logger.Log(*logEntry)
		return
	}
	defer response.Body.Close()

	// レスポンスボディの読み取り
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		fmt.Printf("Error reading response body: %v\n", err)
		logEntry.Status = http.StatusBadGateway
		p.logger.Log(*logEntry)
		return
	}

	// GETリクエストの場合はキャッシュに保存
	if request.Method == "GET" && response.StatusCode == http.StatusOK {
		if err := p.cache.Set(cacheKey, responseBody, response.Header); err != nil {
			fmt.Printf("Error caching response: %v\n", err)
		}
	}

	// レスポンスの転送
	if err := response.Write(clientConn); err != nil {
		fmt.Printf("Error writing response to client: %v\n", err)
		logEntry.Status = http.StatusInternalServerError
		p.logger.Log(*logEntry)
		return
	}

	logEntry.Status = response.StatusCode
	logEntry.BytesIn = request.ContentLength
	logEntry.BytesOut = int64(len(responseBody))
	logEntry.Duration = time.Since(logEntry.Time).Seconds()
	p.logger.Log(*logEntry)
}

// handleTunneling はHTTPSトンネリングを処理
func (p *Proxy) handleTunneling(
	clientConn net.Conn, request *http.Request, logEntry *LogEntry,
) {
	startTime := time.Now()

	// サーバーへの接続
	serverConn, err := p.pool.Get(request.Host)
	if err != nil {
		fmt.Printf("Error connecting to server: %v\n", err)
		logEntry.Status = http.StatusBadGateway
		p.logger.Log(*logEntry)
		return
	}
	defer p.pool.Put(request.Host, serverConn)

	// クライアントへ200 OKを送信
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
		p.logger.Log(*logEntry)
		return
	}

	// データ転送用のチャネル
	bytesIn := make(chan int64, 1)
	bytesOut := make(chan int64, 1)
	done := make(chan bool, tunnelDirections)

	// クライアント → サーバー
	go func() {
		n, err := io.Copy(serverConn, clientConn)
		if err != nil {
			fmt.Printf("Error copying client to server: %v\n", err)
		}
		bytesIn <- n
		done <- true
	}()

	// サーバー → クライアント
	go func() {
		n, err := io.Copy(clientConn, serverConn)
		if err != nil {
			fmt.Printf("Error copying server to client: %v\n", err)
		}
		bytesOut <- n
		done <- true
	}()

	// 転送完了待ち
	<-done
	logEntry.Status = http.StatusOK
	logEntry.BytesIn = <-bytesIn
	logEntry.BytesOut = <-bytesOut
	logEntry.Duration = time.Since(startTime).Seconds()
	p.logger.Log(*logEntry)
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
	if err := proxy.Start(); err != nil {
		fmt.Printf("Failed to start proxy: %v\n", err)
		return
	}
}
