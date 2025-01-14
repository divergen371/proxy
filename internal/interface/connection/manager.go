package connection

import (
	"net"
	"sync"
	"time"
)

// Manager はコネクションプールを管理する
type Manager struct {
	mu          sync.RWMutex
	connections map[string][]*pooledConn
	maxIdle     int
	idleTimeout time.Duration
	maxLifetime time.Duration
}

type pooledConn struct {
	conn      net.Conn
	createdAt time.Time
	lastUsed  time.Time
}

// NewManager は新しいManagerインスタンスを作成
func NewManager(maxIdle int, idleTimeout, maxLifetime time.Duration) *Manager {
	m := &Manager{
		connections: make(map[string][]*pooledConn),
		maxIdle:     maxIdle,
		idleTimeout: idleTimeout,
		maxLifetime: maxLifetime,
	}

	// 定期的なクリーンアップを開始
	go m.periodicCleanup()

	return m
}

// GetConnection は接続を取得または新規作成
func (m *Manager) GetConnection(host string) (net.Conn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// プールから有効な接続を探す
	if conns := m.connections[host]; len(conns) > 0 {
		for i := len(conns) - 1; i >= 0; i-- {
			pc := conns[i]
			if time.Since(pc.lastUsed) > m.idleTimeout || time.Since(pc.createdAt) > m.maxLifetime {
				// 期限切れの接続は閉じる
				pc.conn.Close()
				continue
			}
			// 有効な接続を見つけた
			m.connections[host] = conns[:i]
			return pc.conn, nil
		}
	}

	// 新しい接続を作成
	conn, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// ReleaseConnection は使用済みの接続をプールに返却
func (m *Manager) ReleaseConnection(host string, conn net.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()

	conns := m.connections[host]
	if len(conns) >= m.maxIdle {
		// プールが一杯なら接続を閉じる
		conn.Close()
		return
	}

	// 接続をプールに追加
	pc := &pooledConn{
		conn:      conn,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}
	m.connections[host] = append(conns, pc)
}

// CloseAll は全ての接続を閉じる
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, conns := range m.connections {
		for _, pc := range conns {
			pc.conn.Close()
		}
	}

	m.connections = make(map[string][]*pooledConn)
	return nil
}

// periodicCleanup は定期的に古い接続を削除
func (m *Manager) periodicCleanup() {
	ticker := time.NewTicker(m.idleTimeout / 2)
	defer ticker.Stop()

	for range ticker.C {
		m.cleanup()
	}
}

// cleanup は期限切れの接続を削除
func (m *Manager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for host, conns := range m.connections {
		var active []*pooledConn
		for _, pc := range conns {
			if now.Sub(pc.lastUsed) > m.idleTimeout || now.Sub(pc.createdAt) > m.maxLifetime {
				pc.conn.Close()
				continue
			}
			active = append(active, pc)
		}
		if len(active) == 0 {
			delete(m.connections, host)
		} else {
			m.connections[host] = active
		}
	}
}
