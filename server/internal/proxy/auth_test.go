package proxy

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func authRequest(username, password string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "http://target.test/", nil)
	credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	request.Header.Set("Proxy-Authorization", "Basic "+credentials)
	return request
}

func TestUserSnapshotCacheAndInvalidation(t *testing.T) {
	env := newTestEnvWithOptions(t, func(options *Options) {
		options.StatsFlushInterval = time.Hour
	})
	env.addUser(t, "cached-user", "password123", 0)

	first := env.server.authenticate(authRequest("cached-user", "password123"))
	if first.Status != 0 || first.User == nil {
		t.Fatalf("first auth = %+v", first)
	}

	// Deleting the row directly bypasses the API's invalidation on purpose: the
	// snapshot cache must keep the hot path off SQLite for its short TTL.
	if err := env.store.DeleteUser(first.User.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	second := env.server.authenticate(authRequest("cached-user", "password123"))
	if second.Status != 0 || second.User == nil {
		t.Fatalf("cached auth = %+v", second)
	}

	env.server.InvalidateAuthCache()
	third := env.server.authenticate(authRequest("cached-user", "password123"))
	if third.Status != http.StatusProxyAuthRequired {
		t.Fatalf("auth after invalidation = %+v", third)
	}
}
