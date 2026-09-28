package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/authn"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/id"
	"rokko.dev/pkg/validate"
	"rokko.dev/services/auth/internal/domain"
	"rokko.dev/services/auth/internal/repository"
)

const userEventsTopic = "user.events"

type RefreshStore interface {
	Save(ctx context.Context, hash string, userID int64, username string, ttl time.Duration) error
	Get(ctx context.Context, hash string) (int64, string, error)
	Delete(ctx context.Context, hash string) error
}

type RefreshRotator interface {
	SaveFamily(ctx context.Context, hash string, userID int64, username, family string, ttl time.Duration) error
	Rotate(ctx context.Context, oldHash, newHash string, ttl time.Duration) (int64, string, string, bool, error)
	RevokeUser(ctx context.Context, userID int64) error
}

type Limiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

type Profiles interface {
	Ensure(ctx context.Context, accessToken string, userID int64, username string) error
}

type RegisterInput struct {
	Email    string `validate:"required,email,max=254"`
	Username string `validate:"required,min=2,max=32"`
	Password string `validate:"required,min=8,max=72"`
}

type LoginInput struct {
	Email    string `validate:"required,email"`
	Password string `validate:"required"`
}

type Tokens struct {
	UserID       int64
	Username     string
	AccessToken  string
	RefreshToken string
}

type Service struct {
	store      repository.Store
	refresh    RefreshStore
	tokens     *authn.Manager
	ids        *id.Generator
	limiter    Limiter
	profiles   Profiles
	refreshTTL time.Duration
}

func New(store repository.Store, refresh RefreshStore, tokens *authn.Manager, ids *id.Generator, limiter Limiter, profiles Profiles, refreshTTL time.Duration) *Service {
	return &Service{
		store: store, refresh: refresh, tokens: tokens, ids: ids,
		limiter: limiter, profiles: profiles, refreshTTL: refreshTTL,
	}
}

func (s *Service) Register(ctx context.Context, in RegisterInput) (Tokens, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if err := validate.Struct(in); err != nil {
		return Tokens{}, err
	}
	if !validUsername(in.Username) {
		return Tokens{}, apperr.Invalid("username must be 2-32 chars of a-z, 0-9 or _")
	}
	hash, err := authn.HashPassword(in.Password)
	if err != nil {
		return Tokens{}, err
	}
	userID := s.ids.Next()
	eventID := s.ids.Next()
	env, err := eventx.New(id.Format(eventID), "user.registered", map[string]string{
		"user_id":  id.Format(userID),
		"email":    in.Email,
		"username": in.Username,
	})
	if err != nil {
		return Tokens{}, err
	}
	payload, err := env.Bytes()
	if err != nil {
		return Tokens{}, err
	}
	account := domain.Account{ID: userID, Email: in.Email, Username: in.Username, PasswordHash: hash}
	if err := s.store.CreateAccount(ctx, account, domain.Event{
		ID: eventID, Topic: userEventsTopic, Key: id.Format(userID), Payload: payload,
	}); err != nil {
		return Tokens{}, err
	}
	tokens, err := s.issue(ctx, userID, in.Username)
	if err != nil {
		return Tokens{}, err
	}
	if s.profiles != nil {
		_ = s.profiles.Ensure(ctx, tokens.AccessToken, userID, in.Username)
	}
	return tokens, nil
}

func (s *Service) Login(ctx context.Context, in LoginInput) (Tokens, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if err := validate.Struct(in); err != nil {
		return Tokens{}, err
	}
	if s.limiter != nil {
		ok, err := s.limiter.Allow(ctx, "login:"+in.Email)
		if err != nil {
			return Tokens{}, err
		}
		if !ok {
			return Tokens{}, apperr.RateLimited("too many login attempts")
		}
	}
	account, err := s.store.FindByEmail(ctx, in.Email)
	if err != nil {
		return Tokens{}, err
	}
	ok, err := authn.VerifyPassword(in.Password, account.PasswordHash)
	if err != nil {
		return Tokens{}, err
	}
	if !ok {
		return Tokens{}, apperr.Unauthorized("invalid email or password")
	}
	return s.issue(ctx, account.ID, account.Username)
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	if refreshToken == "" {
		return Tokens{}, apperr.Unauthorized("missing refresh token")
	}
	if rot, ok := s.refresh.(RefreshRotator); ok {
		raw, hash, err := newRefresh()
		if err != nil {
			return Tokens{}, err
		}
		userID, username, family, reused, err := rot.Rotate(ctx, hashToken(refreshToken), hash, s.refreshTTL)
		if err != nil {
			return Tokens{}, err
		}
		if reused {
			return Tokens{}, apperr.Unauthorized("refresh reused")
		}
		access, err := s.tokens.Issue(userID, username)
		if err != nil {
			return Tokens{}, err
		}
		_ = family
		return Tokens{UserID: userID, Username: username, AccessToken: access, RefreshToken: raw}, nil
	}
	userID, username, err := s.refresh.Get(ctx, hashToken(refreshToken))
	if err != nil {
		return Tokens{}, err
	}
	if err := s.refresh.Delete(ctx, hashToken(refreshToken)); err != nil {
		return Tokens{}, err
	}
	return s.issue(ctx, userID, username)
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return s.refresh.Delete(ctx, hashToken(refreshToken))
}

func (s *Service) LogoutAll(ctx context.Context, userID int64) error {
	if rot, ok := s.refresh.(RefreshRotator); ok {
		return rot.RevokeUser(ctx, userID)
	}
	return nil
}

func (s *Service) issue(ctx context.Context, userID int64, username string) (Tokens, error) {
	access, err := s.tokens.Issue(userID, username)
	if err != nil {
		return Tokens{}, err
	}
	raw, hash, err := newRefresh()
	if err != nil {
		return Tokens{}, err
	}
	if rot, ok := s.refresh.(RefreshRotator); ok {
		family := id.Format(s.ids.Next())
		if err := rot.SaveFamily(ctx, hash, userID, username, family, s.refreshTTL); err != nil {
			return Tokens{}, err
		}
	} else if err := s.refresh.Save(ctx, hash, userID, username, s.refreshTTL); err != nil {
		return Tokens{}, err
	}
	return Tokens{UserID: userID, Username: username, AccessToken: access, RefreshToken: raw}, nil
}

func validUsername(s string) bool {
	if len(s) < 2 || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func newRefresh() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashToken(raw), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
