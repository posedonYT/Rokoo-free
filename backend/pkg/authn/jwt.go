package authn

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"rokko.dev/pkg/apperr"
)

type Identity struct {
	UserID   int64
	Username string
	Token    string
}

type ctxKey struct{}

func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}

type Claims struct {
	jwt.RegisteredClaims
	Username string `json:"username"`
}

type Manager struct {
	private   ed25519.PrivateKey
	public    ed25519.PublicKey
	accessTTL time.Duration
	issuer    string
}

func Load(privatePath, publicPath, issuer string, accessTTL time.Duration) (*Manager, error) {
	pubPEM, err := os.ReadFile(publicPath)
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}
	pubAny, err := jwt.ParseEdPublicKeyFromPEM(pubPEM)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	pub, ok := pubAny.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not ed25519")
	}
	m := &Manager{public: pub, accessTTL: accessTTL, issuer: issuer}
	if privatePath == "" {
		return m, nil
	}
	privPEM, err := os.ReadFile(privatePath)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	privAny, err := jwt.ParseEdPrivateKeyFromPEM(privPEM)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	priv, ok := privAny.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not ed25519")
	}
	m.private = priv
	return m, nil
}

func (m *Manager) Issue(userID int64, username string) (string, error) {
	if m.private == nil {
		return "", fmt.Errorf("private key is not configured")
	}
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10),
			Issuer:    m.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.accessTTL)),
		},
		Username: username,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return tok.SignedString(m.private)
}

func (m *Manager) Parse(token string) (Identity, error) {
	var claims Claims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodEdDSA {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return m.public, nil
	})
	if err != nil || !parsed.Valid {
		return Identity{}, apperr.Unauthorized("invalid token")
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return Identity{}, apperr.Unauthorized("invalid token subject")
	}
	return Identity{UserID: id, Username: claims.Username, Token: token}, nil
}
