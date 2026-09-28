package storage

import (
	"context"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Minio struct{ client *minio.Client }

func New(endpoint, accessKey, secretKey string, secure bool) (*Minio, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
	})
	if err != nil {
		return nil, err
	}
	return &Minio{client: client}, nil
}

func (m *Minio) EnsureBucket(ctx context.Context, bucket string) error {
	var last error
	for attempt := 0; attempt < 20; attempt++ {
		exists, err := m.client.BucketExists(ctx, bucket)
		if err == nil {
			if exists {
				return nil
			}
			if err := m.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err == nil {
				return nil
			} else {
				last = err
			}
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return last
}

func (m *Minio) PresignPut(ctx context.Context, bucket, key, _ string) (string, error) {
	u, err := m.client.PresignedPutObject(ctx, bucket, key, time.Hour)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (m *Minio) PresignGet(ctx context.Context, bucket, key string) (string, error) {
	u, err := m.client.PresignedGetObject(ctx, bucket, key, time.Hour, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (m *Minio) Exists(ctx context.Context, bucket, key string) (bool, error) {
	_, err := m.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchKey" || resp.StatusCode == 404 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
