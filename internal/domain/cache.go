package domain

import "time"

// CacheManager はキャッシュ管理のインターフェース.
type CacheManager interface {
	Get(key string) (*CacheEntry, bool)
	Set(key string, entry *CacheEntry) error
	Delete(key string) error
}

// CacheEntry はキャッシュのエントリを表す.
type CacheEntry struct {
	Data       []byte
	Headers    map[string][]string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Compressed bool
}
