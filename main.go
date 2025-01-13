package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// 定数定義
const (
	// プロキシサーバーのポート番号
	ProxyPort = ":10080"
	// ログファイルのパス
	LogFilePath = "proxy.log"
	// チャネルバッファサイズ
	tunnelDirections = 2
)

// LogEntry はアクセスログの1エントリを表す構造体
type LogEntry struct {
	Time     time.Time // アクセス時刻
	ClientIP string    // クライアントのIPアドレス
	Method   string    // HTTPメソッド
	Host     string    // 接続先ホスト
	URI      string    // リクエストURI
	Status   int       // ステータスコード
	BytesIn  int64     // 受信バイト数
	BytesOut int64     // 送信バイト数
	Duration float64   // 処理時間（秒）
}

// Logger はロギング機能を提供する構造体
type Logger struct {
	mu     sync.Mutex
	file   *os.File
	logger *log.Logger
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

	// ログメッセージのフォーマット
	message := fmt.Sprintf(
		"%s %s %s %s %d %d bytes in %d bytes out %.3f seconds",
		entry.ClientIP,
		entry.Method,
		entry.Host,
		entry.URI,
		entry.Status,
		entry.BytesIn,
		entry.BytesOut,
		entry.Duration,
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
}

// NewProxy は新しいProxyインスタンスを作成する
func NewProxy(logger *Logger) *Proxy {
	return &Proxy{
		logger: logger,
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

	// リクエストの読み取り
	reader := bufio.NewReader(clientConn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		fmt.Printf("Error reading request: %v\n", err)
		return
	}

	// ログエントリの初期化
	logEntry := LogEntry{
		Time:     startTime,
		ClientIP: clientIP,
		Method:   request.Method,
		Host:     request.Host,
		URI:      request.RequestURI,
	}

	// CONNECTメソッドの処理
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
	// サーバーへの接続
	serverConn, err := net.Dial("tcp", request.Host)
	if err != nil {
		fmt.Printf("Error connecting to server: %v\n", err)
		logEntry.Status = http.StatusBadGateway
		p.logger.Log(*logEntry)
		return
	}
	defer serverConn.Close()

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

	// 双方向トンネルの作成
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

func main() {
	// ロガーの初期化
	logger, err := NewLogger(LogFilePath)
	if err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		return
	}
	defer logger.Close()

	// プロキシサーバーの作成と起動
	proxy := NewProxy(logger)
	if err := proxy.Start(); err != nil {
		fmt.Printf("Failed to start proxy: %v\n", err)
		return
	}
}
