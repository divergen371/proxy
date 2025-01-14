// internal/interface/repository/access/repository.go
package access

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"proxy/internal/domain"

	"gopkg.in/yaml.v3"
)

type BlockList struct {
	BlockedIPs     []string `yaml:"blocked_ips"`
	BlockedDomains []string `yaml:"blocked_domains"`
}

// Repository はアクセス制御のリポジトリ実装
type Repository struct {
	mu             sync.RWMutex
	configFile     string
	blockedIPs     map[string]bool
	blockedDomains map[string]bool
}

var _ domain.AccessController = (*Repository)(nil)

// New は新しいRepositoryインスタンスを作成
func New(configFile string) domain.AccessController {
	r := &Repository{
		configFile:     configFile,
		blockedIPs:     make(map[string]bool),
		blockedDomains: make(map[string]bool),
	}

	// 初期ロード
	if err := r.loadConfig(); err != nil {
		fmt.Printf("Warning: Failed to load initial config: %v\n", err)
	}

	// 設定の自動リロードを開始
	go r.watchConfig()

	return r
}

// IsAllowed は指定されたIPアドレスとホストがアクセスを許可されているか確認
func (r *Repository) IsAllowed(clientIP, host string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// IPアドレスのチェック
	if r.blockedIPs[clientIP] {
		fmt.Printf("Blocked IP access attempt: %s\n", clientIP)
		return false, nil
	}

	// ドメインのチェック
	host = strings.ToLower(host)
	if r.blockedDomains[host] {
		fmt.Printf("Blocked domain access attempt: %s\n", host)
		return false, nil
	}

	// ワイルドカードドメインのチェック
	parts := strings.Split(host, ".")
	for i := 0; i < len(parts)-1; i++ {
		wildcard := "*." + strings.Join(parts[i+1:], ".")
		if r.blockedDomains[wildcard] {
			fmt.Printf("Blocked wildcard domain access attempt: %s matches %s\n", host, wildcard)
			return false, nil
		}
	}

	return true, nil
}

// Reload は設定を再読み込み
func (r *Repository) Reload() error {
	return r.loadConfig()
}

// loadConfig は設定ファイルから設定を読み込む
func (r *Repository) loadConfig() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.configFile)
	if err != nil {
		if os.IsNotExist(err) {
			// デフォルト設定の作成
			defaultConfig := BlockList{
				BlockedIPs:     []string{},
				BlockedDomains: []string{},
			}
			data, err = yaml.Marshal(defaultConfig)
			if err != nil {
				return fmt.Errorf("failed to create default config: %v", err)
			}
			if err := os.WriteFile(r.configFile, data, 0644); err != nil {
				return fmt.Errorf("failed to write default config: %v", err)
			}
		} else {
			return fmt.Errorf("failed to read config: %v", err)
		}
	}

	var blockList BlockList
	if err := yaml.Unmarshal(data, &blockList); err != nil {
		return fmt.Errorf("failed to parse config: %v", err)
	}

	// 新しいマップの作成
	newBlockedIPs := make(map[string]bool)
	newBlockedDomains := make(map[string]bool)

	// IPアドレスの登録
	for _, ip := range blockList.BlockedIPs {
		ip = strings.TrimSpace(ip)
		newBlockedIPs[ip] = true
		fmt.Printf("Loaded blocked IP: %s\n", ip)
	}

	// ドメインの登録
	for _, domain := range blockList.BlockedDomains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		newBlockedDomains[domain] = true
		fmt.Printf("Loaded blocked domain: %s\n", domain)
	}

	r.blockedIPs = newBlockedIPs
	r.blockedDomains = newBlockedDomains

	fmt.Printf("Loaded %d blocked IPs and %d blocked domains\n",
		len(blockList.BlockedIPs), len(blockList.BlockedDomains))
	return nil
}

// watchConfig は設定ファイルの変更を監視
func (r *Repository) watchConfig() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	var lastModTime time.Time
	for range ticker.C {
		stat, err := os.Stat(r.configFile)
		if err != nil {
			fmt.Printf("Error checking config file: %v\n", err)
			continue
		}

		if stat.ModTime().After(lastModTime) {
			if err := r.loadConfig(); err != nil {
				fmt.Printf("Error reloading config: %v\n", err)
				continue
			}
			lastModTime = stat.ModTime()
		}
	}
}
