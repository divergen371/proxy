package main

import (
	"bufio"
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
	tunnelDirections = 2
)

// BlockList はブロックリストの構造を定義
type BlockList struct {
	BlockedIPs     []string `yaml:"blocked_ips"`
	BlockedDomains []string `yaml:"blocked_domains"`
	UpdateInterval string   `yaml:"update_interval,omitempty"`
}

// LogEntry はアクセスログの1エントリを表す構造体
type LogEntry struct {
	Time     time.Time
	ClientIP string
	Method   string
	Host     string
	URI      string
	Status   int
	BytesIn  int64
	BytesOut int64
	Duration float64
	Blocked  bool
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

	fmt.Printf("Loaded %d blocked IPs and %d blocked domains\n",
		len(blockList.BlockedIPs), len(blockList.BlockedDomains))
	return nil
}

// watchConfig は設定ファイルの変更を監視し、自動的に再読み込みを行う
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

// NewLogger は新しいLoggerインスタンスを作成する
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

// Log はアクセスログを記録する
func (l *Logger) Log(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	message := fmt.Sprintf(
		"%s %s %s %s %d %d bytes in %d bytes out %.3f seconds blocked=%v",
		entry.ClientIP,
		entry.Method,
		entry.Host,
		entry.URI,
		entry.Status,
		entry.BytesIn,
		entry.BytesOut,
		entry.Duration,
		entry.Blocked,
	)

	l.logger.Println(message)
}

// Close はロガーのリソースを解放する
func (l *Logger) Close() error {
	return l.file.Close()
}

// Proxy はプロキシサーバーの構造体
type Proxy struct {
	logger *Logger
	ac     *AccessControl
}

// NewProxy は新しいProxyインスタンスを作成する
func NewProxy(logger *Logger, ac *AccessControl) *Proxy {
	return &Proxy{
		logger: logger,
		ac:     ac,
	}
}

// Start はプロキシサーバーを起動する
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

// handleConnection はクライアントとの接続を処理する
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
		logEntry.Status = http.StatusMethodNotAllowed
		p.logger.Log(logEntry)
		fmt.Printf("Method not supported: %s\n", request.Method)
	}
}

// handleTunneling はHTTPSトンネリングを処理する
func (p *Proxy) handleTunneling(
	clientConn net.Conn, request *http.Request, logEntry *LogEntry,
) {
	startTime := time.Now()

	serverConn, err := net.Dial("tcp", request.Host)
	if err != nil {
		fmt.Printf("Error connecting to server: %v\n", err)
		logEntry.Status = http.StatusBadGateway
		p.logger.Log(*logEntry)
		return
	}
	defer serverConn.Close()

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
	logEntry.Duration = time.Since(startTime).Seconds()
	p.logger.Log(*logEntry)
}

func main() {
	// 作業ディレクトリの取得
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Printf("Failed to get working directory: %v\n", err)
		return
	}

	// 設定ファイルのパスを設定
	logPath := filepath.Join(workDir, LogFilePath)
	blockListPath := filepath.Join(workDir, BlockListPath)

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

	// プロキシサーバーの作成と起動
	proxy := NewProxy(logger, ac)
	if err := proxy.Start(); err != nil {
		fmt.Printf("Failed to start proxy: %v\n", err)
		return
	}
}
