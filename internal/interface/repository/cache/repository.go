package cache

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"proxy/internal/domain"
)

// Repository はキャッシュのリポジトリ実装
type Repository struct {
	mu       sync.RWMutex
	baseDir  string
	maxSize  int64
	currSize int64
	entries  map[string]*Entry
}

// Verify interface implementation
var _ domain.CacheManager = (*Repository)(nil)

// New は新しいRepositoryインスタンスを作成
func New(baseDir string, maxSize int64) (*Repository, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, err
	}

	return &Repository{
		baseDir: baseDir,
		maxSize: maxSize,
		entries: make(map[string]*Entry),
	}, nil
}

// Get はキャッシュからデータを取得
func (r *Repository) Get(key string) (*domain.CacheEntry, bool) {
	r.mu.RLock()
	entry, exists := r.entries[key]
	r.mu.RUnlock()

	if !exists {
		return nil, false
	}

	if entry.IsExpired() {
		go r.Delete(key) // 非同期でクリーンアップ
		return nil, false
	}

	data, err := r.readFile(entry.Key)
	if err != nil {
		go r.Delete(key) // ファイルが読めない場合は削除
		return nil, false
	}

	if entry.Compressed {
		data, err = decompress(data)
		if err != nil {
			go r.Delete(key)
			return nil, false
		}
	}

	return &domain.CacheEntry{
		Data:       data,
		CreatedAt:  entry.CreatedAt,
		ExpiresAt:  entry.ExpiresAt,
		Compressed: entry.Compressed,
	}, true
}

// Set はキャッシュにデータを保存
func (r *Repository) Set(key string, entry *domain.CacheEntry) error {
	data := entry.Data
	compressed := entry.Compressed

	// 大きなデータの場合は圧縮を試みる
	if !compressed && len(data) > 1024 {
		if compData, err := compress(data); err == nil && len(compData) < len(data) {
			data = compData
			compressed = true
		}
	}

	// キャッシュサイズのチェックと調整
	r.mu.Lock()
	defer r.mu.Unlock()

	newSize := r.currSize + int64(len(data))
	for newSize > r.maxSize && len(r.entries) > 0 {
		r.evictOldest()
		newSize = r.currSize + int64(len(data))
	}

	// ファイルへの保存
	if err := r.writeFile(key, data); err != nil {
		return err
	}

	// エントリの登録
	r.entries[key] = NewEntry(key, int64(len(data)), time.Until(entry.ExpiresAt), compressed)
	r.currSize += int64(len(data))

	return nil
}

// Delete はキャッシュからエントリを削除
func (r *Repository) Delete(key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, exists := r.entries[key]; exists {
		r.currSize -= entry.Size
		delete(r.entries, key)
		return os.Remove(r.getFilePath(key))
	}
	return nil
}

func (r *Repository) evictOldest() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range r.entries {
		if oldestKey == "" || entry.CreatedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.CreatedAt
		}
	}

	if oldestKey != "" {
		r.Delete(oldestKey)
	}
}

func (r *Repository) getFilePath(key string) string {
	return filepath.Join(r.baseDir, key)
}

func (r *Repository) readFile(key string) ([]byte, error) {
	return os.ReadFile(r.getFilePath(key))
}

func (r *Repository) writeFile(key string, data []byte) error {
	return os.WriteFile(r.getFilePath(key), data, 0644)
}

// compress はデータをgzip圧縮する
func compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)

	if _, err := gz.Write(data); err != nil {
		return nil, err
	}

	if err := gz.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// decompress はgzip圧縮されたデータを展開する
func decompress(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	return io.ReadAll(gz)
}
