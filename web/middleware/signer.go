package middleware

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/panyam/oneauth/keys"
)

// DefaultRelayTokenTTL is how long a minted token lasts when
// RelaySignerConfig.TTL is zero.
const DefaultRelayTokenTTL = time.Hour

// ErrSignerNoClientID is NewRelaySigner's error for a config without a
// ClientID. The relay looks up the signing key by the token's client_id, so a
// token without one can never validate.
var ErrSignerNoClientID = errors.New("relay signer needs a client id")

// RelaySignerConfig configures a RelaySigner.
type RelaySignerConfig struct {
	// ClientID goes into each token's client_id claim. The relay finds the
	// signing key by it. Required.
	ClientID string

	// Issuer goes into each token's iss claim. Authenticator checks it, so
	// leaving it empty means neither side sets or checks an issuer.
	Issuer string

	// Key is the HS256 secret. Nil makes NewRelaySigner generate 32 random
	// bytes, which only this process knows. Pass a key when a relay in
	// another process has to accept the tokens.
	Key []byte

	// TTL is how long each minted token lasts. Zero means
	// DefaultRelayTokenTTL.
	TTL time.Duration
}

// RelaySigner mints relay tokens with the claims RelayAuthenticator reads:
// sub, client_id, iss, rooms, iat, exp, and type "access", which oneauth
// requires. It signs with HS256. KeyStore and Authenticator give a relay
// that accepts exactly these tokens. A RelaySigner is safe for concurrent
// use.
type RelaySigner struct {
	clientID string
	issuer   string
	key      []byte
	ttl      time.Duration
	store    *keys.InMemoryKeyStore
}

// NewRelaySigner returns a signer for cfg. It fails with ErrSignerNoClientID
// when cfg.ClientID is empty, or when it can't generate a key.
func NewRelaySigner(cfg RelaySignerConfig) (*RelaySigner, error) {
	if cfg.ClientID == "" {
		return nil, ErrSignerNoClientID
	}
	key := cfg.Key
	if key == nil {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate relay signing key: %w", err)
		}
	}
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = DefaultRelayTokenTTL
	}
	store := keys.NewInMemoryKeyStore()
	if _, err := store.PutKey(context.Background(), &keys.PutKeyRequest{Record: &keys.KeyRecord{
		ClientID: cfg.ClientID, Key: key, Algorithm: "HS256",
	}}); err != nil {
		return nil, fmt.Errorf("store relay signing key: %w", err)
	}
	return &RelaySigner{clientID: cfg.ClientID, issuer: cfg.Issuer, key: key, ttl: ttl, store: store}, nil
}

// Mint signs a token for subject that may join rooms, and returns it with
// its expiry. Each entry in rooms is a session id, or a prefix ending in "*".
// An empty rooms leaves the claim out, and the relay then lets the token join
// any room, so pass the rooms explicitly unless that is what you want.
func (s *RelaySigner) Mint(subject string, rooms []string) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(s.ttl)
	claims := jwt.MapClaims{
		"sub":       subject,
		"client_id": s.clientID,
		"type":      "access",
		"iat":       now.Unix(),
		"exp":       exp.Unix(),
	}
	if s.issuer != "" {
		claims["iss"] = s.issuer
	}
	if len(rooms) > 0 {
		claims["rooms"] = rooms
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.key)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, time.Unix(exp.Unix(), 0), nil
}

// KeyStore returns a key store holding only this signer's key, under its
// client id. Use it for RelayAuthConfig.KeyStore when building the
// authenticator by hand.
func (s *RelaySigner) KeyStore() keys.KeyLookup {
	return s.store
}

// Authenticator returns a RelayAuthenticator that accepts this signer's
// tokens. It takes Required, TokenQueryParam and the callbacks from cfg, and
// replaces cfg's KeyStore and Issuer with the signer's.
func (s *RelaySigner) Authenticator(cfg RelayAuthConfig) *RelayAuthenticator {
	cfg.KeyStore = s.store
	cfg.Issuer = s.issuer
	return NewRelayAuthenticator(cfg)
}
