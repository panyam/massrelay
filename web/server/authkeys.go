package server

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/panyam/oneauth/keys"
)

// ErrAuthRequiredNoKeys is returned by Init when RELAY_AUTH_REQUIRED=true but
// neither RELAY_AUTH_KEYS_FILE nor RELAY_AUTH_JWKS_URL is set. Without keys
// the auth middleware would be off, and the relay would accept every
// connection while its config says auth is required.
var ErrAuthRequiredNoKeys = errors.New("RELAY_AUTH_REQUIRED=true but no signing keys are configured (set RELAY_AUTH_KEYS_FILE or RELAY_AUTH_JWKS_URL)")

// AuthKey is one entry in the RELAY_AUTH_KEYS_FILE JSON array. Tokens are
// matched to it by their client_id claim. Key is the shared secret for the
// HS* algorithms and a PEM public key for RS*, PS* and ES*.
type AuthKey struct {
	ClientID  string `json:"client_id"`
	Algorithm string `json:"alg"`
	Key       string `json:"key"`
}

var authKeyAlgs = map[string]bool{
	"HS256": true, "HS384": true, "HS512": true,
	"RS256": true, "RS384": true, "RS512": true,
	"PS256": true, "PS384": true, "PS512": true,
	"ES256": true, "ES384": true, "ES512": true,
}

// LoadAuthKeysFile reads a RELAY_AUTH_KEYS_FILE into an InMemoryKeyStore.
// The file is a JSON array of AuthKey. It fails on an empty array, a
// repeated client_id, an unknown algorithm, or a key that isn't PEM for an
// asymmetric algorithm, so a typo stops the relay at startup instead of
// rejecting every token later.
func LoadAuthKeysFile(path string) (*keys.InMemoryKeyStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth keys file: %w", err)
	}
	var entries []AuthKey
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("auth keys file %s: %w", path, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("auth keys file %s: no keys", path)
	}
	ks := keys.NewInMemoryKeyStore()
	seen := map[string]bool{}
	for i, e := range entries {
		switch {
		case e.ClientID == "":
			return nil, fmt.Errorf("auth keys file %s: entry %d: client_id is required", path, i)
		case seen[e.ClientID]:
			return nil, fmt.Errorf("auth keys file %s: client_id %q appears twice", path, e.ClientID)
		case !authKeyAlgs[e.Algorithm]:
			return nil, fmt.Errorf("auth keys file %s: client_id %q: unsupported alg %q", path, e.ClientID, e.Algorithm)
		case e.Key == "":
			return nil, fmt.Errorf("auth keys file %s: client_id %q: key is required", path, e.ClientID)
		case !strings.HasPrefix(e.Algorithm, "HS"):
			if block, _ := pem.Decode([]byte(e.Key)); block == nil {
				return nil, fmt.Errorf("auth keys file %s: client_id %q: %s key is not PEM", path, e.ClientID, e.Algorithm)
			}
		}
		seen[e.ClientID] = true
		if _, err := ks.PutKey(context.Background(), &keys.PutKeyRequest{Record: &keys.KeyRecord{
			ClientID: e.ClientID, Key: []byte(e.Key), Algorithm: e.Algorithm,
		}}); err != nil {
			return nil, fmt.Errorf("auth keys file %s: client_id %q: %w", path, e.ClientID, err)
		}
	}
	return ks, nil
}

// authKeyStoreFromEnv builds the key store from RELAY_AUTH_KEYS_FILE or
// RELAY_AUTH_JWKS_URL (setting both is an error). It returns a nil store
// when neither is set. A JWKS store does its first fetch here, so a relay
// that can't reach its JWKS endpoint fails to start.
func authKeyStoreFromEnv() (keys.KeyLookup, string, error) {
	file := os.Getenv("RELAY_AUTH_KEYS_FILE")
	jwksURL := os.Getenv("RELAY_AUTH_JWKS_URL")
	switch {
	case file != "" && jwksURL != "":
		return nil, "", errors.New("set one of RELAY_AUTH_KEYS_FILE and RELAY_AUTH_JWKS_URL, not both")
	case file != "":
		ks, err := LoadAuthKeysFile(file)
		if err != nil {
			return nil, "", err
		}
		return ks, "file", nil
	case jwksURL != "":
		ks := keys.NewJWKSKeyStore(jwksURL)
		if err := ks.Start(); err != nil {
			return nil, "", err
		}
		return ks, "jwks", nil
	}
	return nil, "", nil
}
