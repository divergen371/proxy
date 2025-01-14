package logger

import (
	"encoding/json"
	"fmt"
	"time"
)

// LogLevel はログレベルを表す.
type LogLevel string

const (
	DEBUG LogLevel = "DEBUG"
	INFO  LogLevel = "INFO"
	WARN  LogLevel = "WARN"
	ERROR LogLevel = "ERROR"
)

// LogEntry はログエントリを表す.
type LogEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Level     LogLevel               `json:"level"`
	Message   string                 `json:"message"`
	Error     string                 `json:"error,omitempty"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

// Format はログエントリを文字列に変換.
func (e *LogEntry) Format() string {
	timestamp := e.Timestamp.Format("2006/01/02 15:04:05.000")

	// 基本的なログフォーマット
	logMsg := fmt.Sprintf("[%s] %s %s", timestamp, e.Level, e.Message)

	// フィールドの追加（存在する場合）
	if len(e.Fields) > 0 {
		if fields, err := json.Marshal(e.Fields); err == nil {
			logMsg += fmt.Sprintf(" fields=%s", string(fields))
		}
	}

	// エラーの追加（存在する場合）
	if e.Error != "" {
		logMsg += fmt.Sprintf(" error=%s", e.Error)
	}

	return logMsg + "\n"
}

// NewLogEntry は新しいLogEntryインスタンスを作成.
func NewLogEntry(
	level LogLevel, msg string, err error, fields map[string]interface{},
) *LogEntry {
	entry := &LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
		Fields:    fields,
	}

	if err != nil {
		entry.Error = err.Error()
	}

	return entry
}
