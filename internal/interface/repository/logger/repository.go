package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"proxy/internal/domain"
)

// Repository はロガーのリポジトリ実装.
type Repository struct {
	mu       sync.Mutex
	file     *os.File
	config   *RotationConfig
	dir      string
	filename string
}

// Verify interface implementation.
var _ domain.Logger = (*Repository)(nil)

// New は新しいRepositoryインスタンスを作成.
func New(directory, filename string, config *RotationConfig) (
	*Repository, error,
) {
	if err := os.MkdirAll(directory, 0755); err != nil {
		return nil, err
	}

	if config == nil {
		config = DefaultRotationConfig()
	}

	filepath := filepath.Join(directory, filename)
	file, err := os.OpenFile(filepath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	logger := &Repository{
		file:     file,
		config:   config,
		dir:      directory,
		filename: filename,
	}

	// ログクリーンアップを定期的に実行
	go logger.periodicCleanup()

	return logger, nil
}

// Info はINFOレベルのログを記録.
func (r *Repository) Info(msg string, fields map[string]interface{}) {
	r.log(NewLogEntry(INFO, msg, nil, fields))
}

// Error はERRORレベルのログを記録.
func (r *Repository) Error(
	msg string, err error, fields map[string]interface{},
) {
	r.log(NewLogEntry(ERROR, msg, err, fields))
}

// Debug はDEBUGレベルのログを記録.
func (r *Repository) Debug(msg string, fields map[string]interface{}) {
	r.log(NewLogEntry(DEBUG, msg, nil, fields))
}

// log はログエントリを書き込み.
func (r *Repository) log(entry *LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// ローテーションのチェック.
	if needs, err := needsRotation(r.file.Name(), r.config.MaxSize); err == nil && needs {
		r.rotate()
	}

	// ログの書き込み.
	formatted := entry.Format()
	if _, err := r.file.WriteString(formatted); err != nil {
		// エラーが発生した場合は標準エラー出力に書き込み.
		os.Stderr.WriteString(fmt.Sprintf("Failed to write log: %v\n", err))
	}
}

// rotate はログファイルをローテーション.
func (r *Repository) rotate() error {
	if err := r.file.Close(); err != nil {
		return err
	}

	if err := rotateFile(r.file.Name()); err != nil {
		return err
	}

	file, err := os.OpenFile(filepath.Join(r.dir, r.filename), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	r.file = file
	return nil
}

// periodicCleanup は定期的に古いログファイルを削除.
func (r *Repository) periodicCleanup() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		cleanOldLogs(r.dir, r.config)
	}
}

// Close はロガーのリソースを解放.
func (r *Repository) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.Close()
}
