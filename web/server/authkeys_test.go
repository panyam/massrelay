package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/panyam/oneauth/keys"
	"github.com/panyam/oneauth/utils"
)

func writeKeysFile(t *testing.T, entries any) string {
	t.Helper()
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func accessClaims(clientID string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"sub": "user-1", "client_id": clientID, "type": "access",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
}

// dialStatus upgrades to the relay's WebSocket endpoint with token and
// returns the HTTP status: 101 on a successful upgrade.
func dialStatus(t *testing.T, app *RelayApp, token string) int {
	t.Helper()
	srv := httptest.NewServer(app)
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/v1/s/sync"
	if token != "" {
		url += "?token=" + token
	}
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		conn.Close()
	}
	if resp == nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	return resp.StatusCode
}

func TestInitFailsWhenAuthRequiredWithoutKeys(t *testing.T) {
	t.Setenv("RELAY_AUTH_REQUIRED", "true")
	err := NewRelayApp().Init()
	if !errors.Is(err, ErrAuthRequiredNoKeys) {
		t.Fatalf("Init() = %v, want ErrAuthRequiredNoKeys", err)
	}
}

func TestInitFailsWithBothKeySources(t *testing.T) {
	t.Setenv("RELAY_AUTH_KEYS_FILE", writeKeysFile(t, []AuthKey{{ClientID: "app", Algorithm: "HS256", Key: "s"}}))
	t.Setenv("RELAY_AUTH_JWKS_URL", "http://127.0.0.1:1/jwks.json")
	if err := NewRelayApp().Init(); err == nil {
		t.Fatal("Init() succeeded with both RELAY_AUTH_KEYS_FILE and RELAY_AUTH_JWKS_URL set")
	}
}

func TestInitFailsWhenJWKSUnreachable(t *testing.T) {
	t.Setenv("RELAY_AUTH_JWKS_URL", "http://127.0.0.1:1/jwks.json")
	if err := NewRelayApp().Init(); err == nil {
		t.Fatal("Init() succeeded with an unreachable JWKS URL")
	}
}

func TestKeysFileEnablesRequiredAuth(t *testing.T) {
	const secret = "test-secret-at-least-32-bytes-long!!"
	t.Setenv("RELAY_AUTH_REQUIRED", "true")
	t.Setenv("RELAY_AUTH_KEYS_FILE", writeKeysFile(t, []AuthKey{{ClientID: "app", Algorithm: "HS256", Key: secret}}))
	app := NewRelayApp()
	if err := app.Init(); err != nil {
		t.Fatal(err)
	}

	if got := dialStatus(t, app, ""); got != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", got)
	}
	bad, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims("app")).SignedString([]byte("some-other-secret-of-32-bytes-long!"))
	if got := dialStatus(t, app, bad); got != http.StatusUnauthorized {
		t.Errorf("wrong key: status %d, want 401", got)
	}
	good, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims("app")).SignedString([]byte(secret))
	if got := dialStatus(t, app, good); got != http.StatusSwitchingProtocols {
		t.Errorf("valid token: status %d, want 101", got)
	}
}

func TestJWKSURLEnablesRequiredAuth(t *testing.T) {
	privPEM, pubPEM, err := utils.GenerateRSAKeyPair(2048)
	if err != nil {
		t.Fatal(err)
	}
	issuerKeys := keys.NewInMemoryKeyStore()
	if _, err := issuerKeys.PutKey(context.Background(), &keys.PutKeyRequest{Record: &keys.KeyRecord{
		ClientID: "app", Key: pubPEM, Algorithm: "RS256",
	}}); err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(&keys.JWKSHandler{KeyStore: issuerKeys})
	defer jwks.Close()

	t.Setenv("RELAY_AUTH_REQUIRED", "true")
	t.Setenv("RELAY_AUTH_JWKS_URL", jwks.URL+"/.well-known/jwks.json")
	app := NewRelayApp()
	if err := app.Init(); err != nil {
		t.Fatal(err)
	}

	priv, err := utils.ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatal(err)
	}
	kid, err := utils.ComputeKid(pubPEM, "RS256")
	if err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, accessClaims("app"))
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}

	if got := dialStatus(t, app, ""); got != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", got)
	}
	if got := dialStatus(t, app, signed); got != http.StatusSwitchingProtocols {
		t.Errorf("valid RS256 token: status %d, want 101", got)
	}
}

func TestLoadAuthKeysFileRejectsBadEntries(t *testing.T) {
	cases := map[string]any{
		"empty array":      []AuthKey{},
		"missing id":       []AuthKey{{Algorithm: "HS256", Key: "s"}},
		"duplicate id":     []AuthKey{{ClientID: "a", Algorithm: "HS256", Key: "s"}, {ClientID: "a", Algorithm: "HS256", Key: "t"}},
		"unknown alg":      []AuthKey{{ClientID: "a", Algorithm: "none", Key: "s"}},
		"missing key":      []AuthKey{{ClientID: "a", Algorithm: "HS256"}},
		"RS256 not PEM":    []AuthKey{{ClientID: "a", Algorithm: "RS256", Key: "not a pem"}},
		"not a JSON array": map[string]string{"client_id": "a"},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadAuthKeysFile(writeKeysFile(t, entries)); err == nil {
				t.Errorf("LoadAuthKeysFile accepted %s", name)
			}
		})
	}
}
