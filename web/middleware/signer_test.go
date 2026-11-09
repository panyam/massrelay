package middleware_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/panyam/massrelay/web/middleware"
)

func newSigner(t *testing.T, cfg middleware.RelaySignerConfig) *middleware.RelaySigner {
	t.Helper()
	s, err := middleware.NewRelaySigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSignerTokenValidatesWithItsAuthenticator(t *testing.T) {
	s := newSigner(t, middleware.RelaySignerConfig{ClientID: "app", Issuer: "app-issuer", TTL: 10 * time.Minute})
	before := time.Now().Truncate(time.Second)
	tok, exp, err := s.Mint("user-1", []string{"room-a", "team-*"})
	if err != nil {
		t.Fatal(err)
	}
	if want := before.Add(10 * time.Minute); exp.Before(want) || exp.After(want.Add(2*time.Second)) {
		t.Fatalf("Mint expiry = %v, want about %v", exp, want)
	}

	claims, err := s.Authenticator(middleware.RelayAuthConfig{Required: true}).ValidateToken(context.Background(), tok)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.Subject != "user-1" || claims.ClientID != "app" {
		t.Fatalf("claims sub=%q client_id=%q, want user-1 and app", claims.Subject, claims.ClientID)
	}
	if !reflect.DeepEqual(claims.Rooms, []string{"room-a", "team-*"}) {
		t.Fatalf("claims.Rooms = %v", claims.Rooms)
	}
	if !claims.ExpiresAt.Equal(exp) {
		t.Fatalf("claims.ExpiresAt = %v, want Mint's %v", claims.ExpiresAt, exp)
	}
	if claims.IssuedAt.Before(before) || claims.IssuedAt.After(exp) {
		t.Fatalf("claims.IssuedAt = %v, want between %v and %v", claims.IssuedAt, before, exp)
	}
}

func TestSignerKeyStoreWorksForHandBuiltAuthenticator(t *testing.T) {
	s := newSigner(t, middleware.RelaySignerConfig{ClientID: "app"})
	tok, _, err := s.Mint("user-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	auth := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{KeyStore: s.KeyStore()})
	if _, err := auth.ValidateToken(context.Background(), tok); err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
}

func TestSignersWithGeneratedKeysRefuseEachOther(t *testing.T) {
	a := newSigner(t, middleware.RelaySignerConfig{ClientID: "app"})
	b := newSigner(t, middleware.RelaySignerConfig{ClientID: "app"})
	tok, _, err := a.Mint("user-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Authenticator(middleware.RelayAuthConfig{}).ValidateToken(context.Background(), tok); err == nil {
		t.Fatal("signer b's authenticator accepted signer a's token")
	}
}

func TestSignerSharedKeyAcrossInstances(t *testing.T) {
	key := []byte("shared-relay-secret")
	a := newSigner(t, middleware.RelaySignerConfig{ClientID: "app", Key: key})
	b := newSigner(t, middleware.RelaySignerConfig{ClientID: "app", Key: key})
	tok, _, err := a.Mint("user-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Authenticator(middleware.RelayAuthConfig{}).ValidateToken(context.Background(), tok); err != nil {
		t.Fatalf("same key, different signer: %v", err)
	}
}

func TestSignerIssuerMismatchRefused(t *testing.T) {
	s := newSigner(t, middleware.RelaySignerConfig{ClientID: "app", Issuer: "one", Key: []byte("k")})
	tok, _, err := s.Mint("user-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	other := middleware.NewRelayAuthenticator(middleware.RelayAuthConfig{KeyStore: s.KeyStore(), Issuer: "two"})
	if _, err := other.ValidateToken(context.Background(), tok); err == nil {
		t.Fatal("authenticator expecting issuer two accepted a token from issuer one")
	}
}

func TestSignerDefaultTTL(t *testing.T) {
	s := newSigner(t, middleware.RelaySignerConfig{ClientID: "app"})
	_, exp, err := s.Mint("user-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(exp); d < middleware.DefaultRelayTokenTTL-2*time.Second || d > middleware.DefaultRelayTokenTTL {
		t.Fatalf("default token lasts %v, want about %v", d, middleware.DefaultRelayTokenTTL)
	}
}

func TestSignerRequiresClientID(t *testing.T) {
	if _, err := middleware.NewRelaySigner(middleware.RelaySignerConfig{}); !errors.Is(err, middleware.ErrSignerNoClientID) {
		t.Fatalf("NewRelaySigner without client id = %v, want ErrSignerNoClientID", err)
	}
}

func TestSignerClaimShape(t *testing.T) {
	s := newSigner(t, middleware.RelaySignerConfig{ClientID: "app", Key: []byte("k")})
	tok, _, err := s.Mint("user-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(tok, claims); err != nil {
		t.Fatal(err)
	}
	if _, ok := claims["rooms"]; ok {
		t.Fatalf("Mint with no rooms wrote a rooms claim: %v", claims["rooms"])
	}
	if _, ok := claims["iss"]; ok {
		t.Fatalf("Mint with no issuer wrote an iss claim: %v", claims["iss"])
	}
	if claims["type"] != "access" {
		t.Fatalf("type claim = %v, want access", claims["type"])
	}
}
