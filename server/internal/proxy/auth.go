package proxy

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"cdnproxy/internal/store"
)

const authCacheTTL = 10 * time.Minute

type authResult struct {
	User     *store.ProxyUser
	Username string
	Status   int
	Message  string
}

// authenticate validates proxy credentials and quota state.
func (s *Server) authenticate(r *http.Request) authResult {
	username, password, hasCredentials := proxyCredentials(r)
	if !hasCredentials {
		return authResult{
			Username: "-",
			Status:   http.StatusProxyAuthRequired,
			Message:  "proxy authentication required",
		}
	}
	if username == "" {
		return authResult{
			Username: "-",
			Status:   http.StatusProxyAuthRequired,
			Message:  "proxy authentication required",
		}
	}

	user, err := s.store.GetUserByUsername(username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return authResult{
				Username: username,
				Status:   http.StatusProxyAuthRequired,
				Message:  "invalid proxy credentials",
			}
		}
		s.logger.Printf("[proxy] 查询代理用户失败: %v", err)
		return authResult{Username: username, Status: http.StatusInternalServerError, Message: "internal error"}
	}

	// bcrypt is intentionally slow, so a successful credential pair is cached
	// briefly. Cache entries are cleared whenever an account is modified.
	if !s.auth.Valid(username, password) {
		if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
			return authResult{
				Username: username,
				Status:   http.StatusProxyAuthRequired,
				Message:  "invalid proxy credentials",
			}
		}
		s.auth.Put(username, password)
	}

	if !user.Enabled {
		return authResult{Username: user.Username, Status: http.StatusForbidden, Message: "proxy account disabled"}
	}
	if user.ExpiresAt != nil && time.Now().Unix() > *user.ExpiresAt {
		return authResult{Username: user.Username, Status: http.StatusForbidden, Message: "proxy account expired"}
	}
	used := user.TotalBytes
	if s.stats != nil {
		used += s.stats.PendingUserBytes(user.ID)
	}
	if user.QuotaBytes > 0 && used >= user.QuotaBytes {
		return authResult{Username: user.Username, Status: http.StatusForbidden, Message: "traffic quota exceeded"}
	}
	return authResult{User: user, Username: user.Username}
}

func proxyCredentials(r *http.Request) (username, password string, ok bool) {
	if r.URL != nil && r.URL.User != nil {
		u := r.URL.User.Username()
		p, _ := r.URL.User.Password()
		if u != "" {
			return u, p, true
		}
	}
	header := r.Header.Get("Proxy-Authorization")
	if header == "" {
		return "", "", false
	}
	const prefix = "Basic "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[len(prefix):]))
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

type credentialCache struct {
	mu      sync.Mutex
	entries map[[32]byte]time.Time
}

func (c *credentialCache) key(username, password string) [32]byte {
	return sha256.Sum256([]byte(username + "\x00" + password))
}

func (c *credentialCache) Valid(username, password string) bool {
	key := c.key(username, password)
	c.mu.Lock()
	defer c.mu.Unlock()
	expires, ok := c.entries[key]
	if !ok {
		return false
	}
	if time.Now().After(expires) {
		delete(c.entries, key)
		return false
	}
	return true
}

func (c *credentialCache) Put(username, password string) {
	key := c.key(username, password)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[[32]byte]time.Time)
	}
	now := time.Now()
	for k, expires := range c.entries {
		if now.After(expires) {
			delete(c.entries, k)
		}
	}
	c.entries[key] = now.Add(authCacheTTL)
}

func (c *credentialCache) Clear() {
	c.mu.Lock()
	c.entries = nil
	c.mu.Unlock()
}
