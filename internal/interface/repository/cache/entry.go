package cache

import (
	"time"
)

// Entry はキャッシュエントリのメタデータを表す
type Entry struct {
	Key        string
	Size       int64
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Compressed bool
}

// NewEntry は新しいEntryインスタンスを作成
func NewEntry(
	key string, size int64, ttl time.Duration, compressed bool,
) *Entry {
	now := time.Now()
	return &Entry{
		Key:        key,
		Size:       size,
		CreatedAt:  now,
		ExpiresAt:  now.Add(ttl),
		Compressed: compressed,
	}
}

// IsExpired はエントリが期限切れかどうかを確認
func (e *Entry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}
