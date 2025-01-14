package usecase

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"proxy/internal/domain"
)

// ProxyUseCase はプロキシの主要なユースケースを実装
type ProxyUseCase struct {
	accessControl domain.AccessController
	cache         domain.CacheManager
	metrics       domain.MetricsCollector
	logger        domain.Logger
}

// NewProxyUseCase は新しいProxyUseCaseインスタンスを作成
func NewProxyUseCase(
	accessControl domain.AccessController,
	cache domain.CacheManager,
	metrics domain.MetricsCollector,
	logger domain.Logger,
) *ProxyUseCase {
	return &ProxyUseCase{
		accessControl: accessControl,
		cache:         cache,
		metrics:       metrics,
		logger:        logger,
	}
}

// CheckAccess はアクセス制御チェックを行う
func (uc *ProxyUseCase) CheckAccess(
	ctx context.Context, clientIP, host string,
) (bool, error) {
	allowed, err := uc.accessControl.IsAllowed(clientIP, host)
	if err != nil {
		return false, fmt.Errorf("access control check failed: %v", err)
	}

	if !allowed {
		uc.metrics.RecordBlockedRequest()
	}

	return allowed, nil
}

// HandleTunnel はHTTPSトンネリングを処理
func (uc *ProxyUseCase) HandleTunnel(
	ctx context.Context, clientConn net.Conn, host string, clientIP string,
) error {
	// タイムアウトの設定
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	// サーバーへの接続
	serverConn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer serverConn.Close()

	// 接続のタイムアウト設定
	if tc, ok := serverConn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// エラーチャネル
	errc := make(chan error, 2)

	// クライアント → サーバー
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024) // 32KB buffer
		_, err := io.CopyBuffer(serverConn, clientConn, buf)
		if err != nil && !isConnectionClosed(err) {
			uc.logger.Error("クライアント→サーバー転送失敗", err, nil)
			errc <- err
		}
		// 送信側のコネクションをシャットダウン
		if tc, ok := serverConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	// サーバー → クライアント
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024) // 32KB buffer
		_, err := io.CopyBuffer(clientConn, serverConn, buf)
		if err != nil && !isConnectionClosed(err) {
			uc.logger.Error("サーバー→クライアント転送失敗", err, nil)
			errc <- err
		}
		// 送信側のコネクションをシャットダウン
		if tc, ok := clientConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	// ゴルーチンの完了を待つ
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// エラーまたは完了を待つ
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errc:
		return err
	case <-done:
		return nil
	}
}

// isConnectionClosed は接続が正常に閉じられたかを判断
func isConnectionClosed(err error) bool {
	if err == io.EOF {
		return true
	}
	if operr, ok := err.(*net.OpError); ok {
		if operr.Err.Error() == "use of closed network connection" {
			return true
		}
	}
	return false
}
