package service

import (
	"context"
	"crypto/rand"
	"math/big"
	"strings"

	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/id"
	"rokko.dev/pkg/validate"
	"rokko.dev/services/user/internal/domain"
	"rokko.dev/services/user/internal/repository"
)

type UpdateInput struct {
	DisplayName string `validate:"max=64"`
	AvatarURL   string `validate:"max=512"`
	StatusText  string `validate:"max=128"`
}

type Service struct {
	store repository.Store
	ids   *id.Generator
}

func New(store repository.Store, ids *id.Generator) *Service {
	return &Service{store: store, ids: ids}
}

func (s *Service) EnsureProfile(ctx context.Context, userID int64, username string) (domain.Profile, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return domain.Profile{}, apperr.Invalid("username required")
	}
	if existing, err := s.store.GetProfile(ctx, userID); err == nil {
		return existing, nil
	}
	var last error
	for i := 0; i < 30; i++ {
		disc, err := discriminator()
		if err != nil {
			return domain.Profile{}, err
		}
		p := domain.Profile{ID: userID, Username: username, Discriminator: disc, DisplayName: username}
		if err := s.store.InsertProfile(ctx, p); err != nil {
			if app, ok := apperr.As(err); ok && app.Code == apperr.CodeConflict {
				last = err
				continue
			}
			if existing, getErr := s.store.GetProfile(ctx, userID); getErr == nil {
				return existing, nil
			}
			return domain.Profile{}, err
		}
		got, err := s.store.GetProfile(ctx, userID)
		if err != nil {
			return p, nil
		}
		return got, nil
	}
	if last != nil {
		return domain.Profile{}, last
	}
	return domain.Profile{}, apperr.Conflict("could not allocate discriminator")
}

func (s *Service) UpdateProfile(ctx context.Context, userID int64, in UpdateInput) (domain.Profile, error) {
	if err := validate.Struct(in); err != nil {
		return domain.Profile{}, err
	}
	return s.store.UpdateProfile(ctx, domain.Profile{
		ID: userID, DisplayName: strings.TrimSpace(in.DisplayName), AvatarURL: strings.TrimSpace(in.AvatarURL), StatusText: strings.TrimSpace(in.StatusText),
	})
}

func (s *Service) Get(ctx context.Context, userID int64) (domain.Profile, error) {
	return s.store.GetProfile(ctx, userID)
}

func (s *Service) Search(ctx context.Context, query string, limit int) ([]domain.Profile, error) {
	query = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(query, "@")))
	if query == "" {
		return nil, apperr.Invalid("query required")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.store.Search(ctx, query, limit)
}

func (s *Service) SendFriendRequest(ctx context.Context, me, target int64) (string, error) {
	if me == target {
		return "", apperr.Invalid("cannot friend yourself")
	}
	if _, err := s.store.GetProfile(ctx, target); err != nil {
		return "", err
	}
	status := domain.RelPendingOut
	err := s.store.InTx(ctx, func(tx repository.Tx) error {
		blocked, err := blockedEither(ctx, tx, me, target)
		if err != nil {
			return err
		}
		if blocked {
			return apperr.Forbidden("user is blocked")
		}
		mine, err := tx.GetRel(ctx, me, target)
		if err != nil {
			return err
		}
		switch mine {
		case domain.RelFriend:
			return apperr.Conflict("already friends")
		case domain.RelPendingOut:
			return apperr.Conflict("request already sent")
		case domain.RelPendingIn:
			if err := putBoth(ctx, tx, me, target, domain.RelFriend); err != nil {
				return err
			}
			status = domain.RelFriend
			return s.emit(ctx, tx, "friend.accepted", me, []int64{target}, map[string]string{
				"user_id": id.Format(me), "friend_id": id.Format(target),
			})
		default:
			if err := tx.PutRel(ctx, me, target, domain.RelPendingOut); err != nil {
				return err
			}
			if err := tx.PutRel(ctx, target, me, domain.RelPendingIn); err != nil {
				return err
			}
			return s.emit(ctx, tx, "friend.requested", me, []int64{target}, map[string]string{
				"from_id": id.Format(me), "to_id": id.Format(target),
			})
		}
	})
	return status, err
}

func (s *Service) Accept(ctx context.Context, me, from int64) error {
	return s.store.InTx(ctx, func(tx repository.Tx) error {
		mine, err := tx.GetRel(ctx, me, from)
		if err != nil {
			return err
		}
		if mine != domain.RelPendingIn {
			return apperr.NotFound("friend request not found")
		}
		if err := putBoth(ctx, tx, me, from, domain.RelFriend); err != nil {
			return err
		}
		return s.emit(ctx, tx, "friend.accepted", me, []int64{from}, map[string]string{
			"user_id": id.Format(me), "friend_id": id.Format(from),
		})
	})
}

func (s *Service) Decline(ctx context.Context, me, from int64) error {
	return s.store.InTx(ctx, func(tx repository.Tx) error {
		mine, err := tx.GetRel(ctx, me, from)
		if err != nil {
			return err
		}
		if mine != domain.RelPendingIn {
			return apperr.NotFound("friend request not found")
		}
		return delBoth(ctx, tx, me, from)
	})
}

func (s *Service) RemoveFriend(ctx context.Context, me, other int64) error {
	return s.store.InTx(ctx, func(tx repository.Tx) error {
		mine, err := tx.GetRel(ctx, me, other)
		if err != nil {
			return err
		}
		if mine != domain.RelFriend {
			return apperr.NotFound("friend not found")
		}
		if err := delBoth(ctx, tx, me, other); err != nil {
			return err
		}
		return s.emit(ctx, tx, "friend.removed", me, []int64{other}, map[string]string{
			"user_id": id.Format(me), "friend_id": id.Format(other),
		})
	})
}

func (s *Service) Block(ctx context.Context, me, other int64) error {
	if me == other {
		return apperr.Invalid("cannot block yourself")
	}
	return s.store.InTx(ctx, func(tx repository.Tx) error {
		if err := delBoth(ctx, tx, me, other); err != nil {
			return err
		}
		if err := tx.PutRel(ctx, me, other, domain.RelBlocked); err != nil {
			return err
		}
		return s.emit(ctx, tx, "user.blocked", me, nil, map[string]string{
			"user_id": id.Format(me), "target_id": id.Format(other),
		})
	})
}

func (s *Service) Unblock(ctx context.Context, me, other int64) error {
	return s.store.InTx(ctx, func(tx repository.Tx) error {
		mine, err := tx.GetRel(ctx, me, other)
		if err != nil {
			return err
		}
		if mine != domain.RelBlocked {
			return apperr.NotFound("block not found")
		}
		return tx.DelRel(ctx, me, other)
	})
}

func (s *Service) List(ctx context.Context, me int64, relType string) ([]domain.Profile, error) {
	return s.store.ListByRel(ctx, me, relType)
}

func (s *Service) FriendIDs(ctx context.Context, me int64) ([]int64, error) {
	return s.store.FriendIDs(ctx, me)
}

func (s *Service) IsBlocked(ctx context.Context, a, b int64) (bool, error) {
	return s.store.IsBlocked(ctx, a, b)
}

func (s *Service) emit(ctx context.Context, tx repository.Tx, typ string, actor int64, recipients []int64, payload map[string]string) error {
	eventID := s.ids.Next()
	env, err := eventx.New(id.Format(eventID), typ, payload)
	if err != nil {
		return err
	}
	env.RecipientIDs = formatIDs(recipients)
	raw, err := env.Bytes()
	if err != nil {
		return err
	}
	return tx.AddEvent(ctx, domain.Event{ID: eventID, Topic: domain.Topic, Key: id.Format(actor), Payload: raw})
}

func putBoth(ctx context.Context, tx repository.Tx, a, b int64, typ string) error {
	if err := tx.PutRel(ctx, a, b, typ); err != nil {
		return err
	}
	return tx.PutRel(ctx, b, a, typ)
}

func delBoth(ctx context.Context, tx repository.Tx, a, b int64) error {
	if err := tx.DelRel(ctx, a, b); err != nil {
		return err
	}
	return tx.DelRel(ctx, b, a)
}

func blockedEither(ctx context.Context, tx repository.Tx, a, b int64) (bool, error) {
	ab, err := tx.GetRel(ctx, a, b)
	if err != nil {
		return false, err
	}
	ba, err := tx.GetRel(ctx, b, a)
	if err != nil {
		return false, err
	}
	return ab == domain.RelBlocked || ba == domain.RelBlocked, nil
}

func formatIDs(ids []int64) []string {
	out := make([]string, len(ids))
	for i, v := range ids {
		out[i] = id.Format(v)
	}
	return out
}

func discriminator() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(9999))
	if err != nil {
		return "", err
	}
	v := int(n.Int64()) + 1
	return leftPad4(v), nil
}

func leftPad4(v int) string {
	s := "0000"
	n := v
	digits := []byte(s)
	for i := 3; i >= 0; i-- {
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits)
}
