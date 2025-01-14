package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RotationConfig はログローテーションの設定を表す.
type RotationConfig struct {
	MaxSize    int64         // バイト単位の最大サイズ
	MaxAge     time.Duration // ログファイルの最大保持期間
	MaxBackups int           // 保持する古いログファイルの最大数
}

// DefaultRotationConfig はデフォルトのログローテーション設定を返す.
func DefaultRotationConfig() *RotationConfig {
	return &RotationConfig{
		MaxSize:    100 * 1024 * 1024,  // 100MB
		MaxAge:     7 * 24 * time.Hour, // 7日
		MaxBackups: 5,
	}
}

// needsRotation はログローテーションが必要かどうかを判断.
func needsRotation(filePath string, maxSize int64) (bool, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return info.Size() >= maxSize, nil
}

// rotateFile はログファイルをローテーション.
func rotateFile(basePath string) error {
	timestamp := time.Now().Format("20060102150405")
	rotatedPath := fmt.Sprintf("%s.%s", basePath, timestamp)

	if err := os.Rename(basePath, rotatedPath); err != nil {
		return err
	}

	return nil
}

// cleanOldLogs は古いログファイルを削除.
func cleanOldLogs(directory string, config *RotationConfig) error {
	files, err := filepath.Glob(filepath.Join(directory, "*.log.*"))
	if err != nil {
		return err
	}

	type fileInfo struct {
		path    string
		modTime time.Time
	}

	var logFiles []fileInfo
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		logFiles = append(logFiles, fileInfo{f, info.ModTime()})
	}

	// 古いファイルの削除
	now := time.Now()
	for _, f := range logFiles {
		if now.Sub(f.modTime) > config.MaxAge {
			os.Remove(f.path)
		}
	}

	return nil
}
