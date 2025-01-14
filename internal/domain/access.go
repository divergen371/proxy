package domain

// AccessRule はアクセス制御ルールを表す.
type AccessRule struct {
	ID          string   `json:"id"`
	DomainMatch string   `json:"domain_match"`
	AllowedIPs  []string `json:"allowed_ips"`
	BlockedIPs  []string `json:"blocked_ips"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// AccessRuleRepository はアクセスルールの永続化を担当.
type AccessRuleRepository interface {
	Save(*AccessRule) error
	Delete(id string) error
	FindAll() ([]*AccessRule, error)
}

// AccessController はアクセス制御のインターフェース.
type AccessController interface {
	IsAllowed(clientIP, host string) (bool, error)
	Reload() error
}
