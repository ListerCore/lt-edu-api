// Package objectstore keeps files in MinIO AIStor (S3-compatible). Objects are private: the API reads them back
// and serves them after checking who is asking.
package objectstore

import (
	"context"
	"errors"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"api/internal/core/port"
)

type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Region    string
	Bucket    string
}

type Store struct {
	client *minio.Client
	bucket string
	region string
}

var errNotConfigured = errors.New("object storage not configured")

// New connects lazily (no request is made here); an incomplete config returns a store that refuses every call
func New(cfg Config) (*Store, error) {
	if cfg.Endpoint == "" || cfg.AccessKey == "" || cfg.SecretKey == "" || cfg.Bucket == "" {
		return &Store{}, nil
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: cfg.UseSSL, Region: cfg.Region})
	if err != nil {
		return nil, err
	}
	return &Store{client: client, bucket: cfg.Bucket, region: cfg.Region}, nil
}

var _ port.ObjectStore = (*Store)(nil)

func (s *Store) Ready() bool { return s.client != nil }

// EnsureBucket creates the bucket when it does not exist yet (first start of a fresh AIStor)
func (s *Store) EnsureBucket(ctx context.Context) error {
	if s.client == nil {
		return errNotConfigured
	}
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil || exists {
		return err
	}
	return s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.region})
}

func (s *Store) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	if s.client == nil {
		return errNotConfigured
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, body, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	if s.client == nil {
		return nil, 0, errNotConfigured
	}
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		return nil, 0, err
	}
	return object, info.Size, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if s.client == nil {
		return errNotConfigured
	}
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}
