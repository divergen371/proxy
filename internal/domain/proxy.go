package domain

import (
	"net"
	"time"
)

// Request はプロキシリクエストを表す.
type Request struct {
	ID          string
	ClientIP    string
	Method      string
	Host        string
	Path        string
	Headers     map[string][]string
	Body        []byte
	IsEncrypted bool
	CreatedAt   time.Time
}

// Response はプロキシレスポンスを表す.
type Response struct {
	RequestID    string
	StatusCode   int
	Headers      map[string][]string
	Body         []byte
	IsFromCache  bool
	IsCompressed bool
	CreatedAt    time.Time
}

// ConnectionManager は接続管理のインターフェース.
type ConnectionManager interface {
	GetConnection(host string) (net.Conn, error)
	ReleaseConnection(host string, conn net.Conn)
	CloseAll() error
}
