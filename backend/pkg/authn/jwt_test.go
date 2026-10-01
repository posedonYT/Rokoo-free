package authn

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testManager(t *testing.T, issuer string) *Manager {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Manager{private: priv, public: pub, accessTTL: time.Minute, issuer: issuer}
}

func TestParseValidToken(t *testing.T) {
	m := testManager(t, "rokko")
	tok, err := m.Issue(42, "bob")
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if id.UserID != 42 || id.Username != "bob" || id.ExpiresAt.IsZero() {
		t.Fatalf("unexpected identity %+v", id)
	}
}

func TestParseRejectsWrongIssuer(t *testing.T) {
	m := testManager(t, "rokko")
	tok, _ := m.Issue(1, "a")
	other := &Manager{public: m.public, issuer: "evil"}
	if _, err := other.Parse(tok); err == nil {
		t.Fatal("expected issuer mismatch error")
	}
}

func TestParseRejectsMissingExpiry(t *testing.T) {
	m := testManager(t, "rokko")
	claims := Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "1", Issuer: "rokko"}}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(m.private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Parse(tok); err == nil {
		t.Fatal("expected error for token without exp")
	}
}

func TestParseRejectsExpired(t *testing.T) {
	m := testManager(t, "rokko")
	m.accessTTL = -time.Minute
	tok, _ := m.Issue(1, "a")
	if _, err := m.Parse(tok); err == nil {
		t.Fatal("expected expired error")
	}
}
