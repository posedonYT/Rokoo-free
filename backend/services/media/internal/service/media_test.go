package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/id"
	"rokko.dev/services/media/internal/domain"
)

type memStore struct{ items map[int64]domain.Object }

func (m *memStore) Insert(_ context.Context, obj domain.Object) error {
	m.items[obj.ID] = obj
	return nil
}
func (m *memStore) Get(_ context.Context, id int64) (domain.Object, error) {
	obj, ok := m.items[id]
	if !ok {
		return domain.Object{}, apperr.NotFound("media not found")
	}
	return obj, nil
}
func (m *memStore) MarkReady(_ context.Context, id int64) error {
	obj := m.items[id]
	obj.Status = domain.StatusReady
	m.items[id] = obj
	return nil
}

type memSigner struct{ present map[string]bool }

func (s *memSigner) PresignPut(context.Context, string, string, string) (string, error) {
	return "http://upload", nil
}
func (s *memSigner) PresignGet(context.Context, string, string) (string, error) {
	return "http://download", nil
}
func (s *memSigner) Exists(_ context.Context, _, key string) (bool, error) {
	return s.present[key], nil
}

func TestUploadFlow(t *testing.T) {
	store := &memStore{items: map[int64]domain.Object{}}
	signer := &memSigner{present: map[string]bool{}}
	svc := New(store, signer, id.New(10), "rokko")
	ctx := context.Background()
	_, err := svc.CreateUpload(ctx, 1, "a.exe", "application/x-msdownload", 10)
	require.Error(t, err)
	_, err = svc.CreateUpload(ctx, 1, "a.png", "image/png", domain.MaxSize+1)
	require.Error(t, err)
	up, err := svc.CreateUpload(ctx, 1, "a.png", "image/png", 128)
	require.NoError(t, err)
	_, err = svc.Confirm(ctx, 1, up.MediaID)
	require.Error(t, err)
	obj, _ := store.Get(ctx, up.MediaID)
	signer.present[obj.ObjectKey] = true
	ready, err := svc.Confirm(ctx, 1, up.MediaID)
	require.NoError(t, err)
	require.Equal(t, domain.StatusReady, ready.Status)
	_, err = svc.DownloadURL(ctx, 2, up.MediaID)
	require.Error(t, err)
	url, err := svc.DownloadURL(ctx, 1, up.MediaID)
	require.NoError(t, err)
	require.Equal(t, "http://download", url)
}
