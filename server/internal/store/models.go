package store

// Admin is a management console account.
type Admin struct {
	ID                int64  `json:"id"`
	Username          string `json:"username"`
	PasswordHash      string `json:"-"`
	PasswordChangedAt int64  `json:"-"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

// Rule maps a target hostname to a CDN upstream.
type Rule struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	TargetScheme   string `json:"target_scheme"`
	MatchHost      string `json:"match_host"`
	MatchPort      int    `json:"match_port"`
	UpstreamHost   string `json:"upstream_host"`
	UpstreamPort   int    `json:"upstream_port"`
	UpstreamScheme string `json:"upstream_scheme"`
	Enabled        bool   `json:"enabled"`
	Remark         string `json:"remark"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// ProxyUser is a proxy access account.
type ProxyUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Enabled      bool   `json:"enabled"`
	QuotaBytes   int64  `json:"quota_bytes"`
	TotalBytes   int64  `json:"total_bytes"`
	ExpiresAt    *int64 `json:"expires_at"`
	Note         string `json:"note"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}
