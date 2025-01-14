package domain

import "fmt"

// ErrNotAllowed はアクセス拒否エラー.
type ErrNotAllowed struct {
	ClientIP string
	Host     string
}

func (e *ErrNotAllowed) Error() string {
	return fmt.Sprintf("access not allowed for client %s to host %s", e.ClientIP, e.Host)
}

// ErrConnectionFailed は接続失敗エラー.
type ErrConnectionFailed struct {
	Host string
	Err  error
}

func (e *ErrConnectionFailed) Error() string {
	return fmt.Sprintf("failed to connect to host %s: %v", e.Host, e.Err)
}
