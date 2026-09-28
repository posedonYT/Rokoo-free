package service

import (
	"context"
	"path"
	"strings"

	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/id"
	"rokko.dev/services/media/internal/domain"
)

type Store interface {
	Insert(ctx context.Context, obj domain.Object) error
	Get(ctx context.Context, id int64) (domain.Object, error)
	MarkReady(ctx context.Context, id int64) error
}

type Signer interface {
	PresignPut(ctx context.Context, bucket, key, contentType string) (string, error)
	PresignGet(ctx context.Context, bucket, key string) (string, error)
	Exists(ctx context.Context, bucket, key string) (bool, error)
}

type Service struct {
	store  Store
	signer Signer
	ids    *id.Generator
	bucket string
}

func New(store Store, signer Signer, ids *id.Generator, bucket string) *Service {
	return &Service{store: store, signer: signer, ids: ids, bucket: bucket}
}

type Upload struct {
	MediaID   int64
	UploadURL string
}

func (s *Service) CreateUpload(ctx context.Context, owner int64, filename, contentType string, size int64) (Upload, error) {
	filename = path.Base(strings.TrimSpace(filename))
	if filename == "" || filename == "." || filename == "/" {
		return Upload{}, apperr.Invalid("filename required")
	}
	if !domain.AllowedType(contentType) {
		return Upload{}, apperr.Invalid("unsupported content type")
	}
	if size <= 0 || size > domain.MaxSize {
		return Upload{}, apperr.Invalid("file must be between 1 byte and 25MB")
	}
	obj := domain.Object{
		ID: s.ids.Next(), OwnerID: owner, Bucket: s.bucket,
		ObjectKey: id.Format(owner) + "/" + filename + "-" + id.Format(s.ids.Next()),
		Filename:  filename, ContentType: contentType, Size: size, Status: domain.StatusPending,
	}
	if err := s.store.Insert(ctx, obj); err != nil {
		return Upload{}, err
	}
	url, err := s.signer.PresignPut(ctx, obj.Bucket, obj.ObjectKey, contentType)
	if err != nil {
		return Upload{}, err
	}
	return Upload{MediaID: obj.ID, UploadURL: url}, nil
}

func (s *Service) Confirm(ctx context.Context, owner, mediaID int64) (domain.Object, error) {
	obj, err := s.owned(ctx, owner, mediaID)
	if err != nil {
		return domain.Object{}, err
	}
	ok, err := s.signer.Exists(ctx, obj.Bucket, obj.ObjectKey)
	if err != nil {
		return domain.Object{}, err
	}
	if !ok {
		return domain.Object{}, apperr.Invalid("upload not found")
	}
	if err := s.store.MarkReady(ctx, obj.ID); err != nil {
		return domain.Object{}, err
	}
	obj.Status = domain.StatusReady
	return obj, nil
}

func (s *Service) DownloadURL(ctx context.Context, owner, mediaID int64) (string, error) {
	obj, err := s.owned(ctx, owner, mediaID)
	if err != nil {
		return "", err
	}
	if obj.Status != domain.StatusReady {
		return "", apperr.Invalid("upload is not confirmed")
	}
	return s.signer.PresignGet(ctx, obj.Bucket, obj.ObjectKey)
}

func (s *Service) owned(ctx context.Context, owner, mediaID int64) (domain.Object, error) {
	obj, err := s.store.Get(ctx, mediaID)
	if err != nil {
		return domain.Object{}, err
	}
	if obj.OwnerID != owner {
		return domain.Object{}, apperr.Forbidden("not your file")
	}
	return obj, nil
}
