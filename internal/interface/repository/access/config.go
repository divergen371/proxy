package access

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type accessConfig struct {
	BlockedIPs     []string `yaml:"blocked_ips"`
	BlockedDomains []string `yaml:"blocked_domains"`
}

func loadConfigFile(path string) (*accessConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return createDefaultConfig(path)
		}
		return nil, err
	}

	var config accessConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func createDefaultConfig(path string) (*accessConfig, error) {
	config := &accessConfig{
		BlockedIPs:     []string{},
		BlockedDomains: []string{},
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return nil, err
	}

	return config, nil
}

// prepare は設定データを正規化する
func (c *accessConfig) prepare() (map[string]bool, map[string]bool) {
	ips := make(map[string]bool)
	domains := make(map[string]bool)

	for _, ip := range c.BlockedIPs {
		ips[strings.TrimSpace(ip)] = true
	}

	for _, domain := range c.BlockedDomains {
		domains[strings.ToLower(strings.TrimSpace(domain))] = true
	}

	return ips, domains
}
