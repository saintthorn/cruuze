// Package blob stores attachment bytes, either on local disk or in an
// S3-compatible bucket.
package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound is returned by Get when there is nothing stored under a key.
var ErrNotFound = errors.New("blob: not found")

// Store holds opaque blobs under slash-separated keys.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get returns the blob; the caller closes it.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// Dir stores blobs as files under a directory. It suits development and
// single-server deployments with a persistent disk.
type Dir struct{ root string }

func NewDir(root string) (*Dir, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &Dir{root: abs}, nil
}

// path maps a key to a file, refusing keys that would escape the root.
func (d *Dir) path(key string) (string, error) {
	p := filepath.Join(d.root, filepath.FromSlash(key))
	if !strings.HasPrefix(p, d.root+string(filepath.Separator)) {
		return "", errors.New("blob: invalid key")
	}
	return p, nil
}

func (d *Dir) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	// Write to a temporary file first so a failed upload leaves nothing behind.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (d *Dir) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

func (d *Dir) Delete(_ context.Context, key string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// S3 stores blobs in an S3-compatible bucket (AWS S3, MinIO, Cloudflare R2,
// Railway buckets).
type S3 struct {
	client *minio.Client
	bucket string
}

// S3Config says where the bucket is. Endpoint is a host[:port] without a scheme.
type S3Config struct {
	Endpoint, Bucket, AccessKey, SecretKey, Region string
	// Insecure uses plain HTTP, for a local MinIO.
	Insecure bool
}

func NewS3(cfg S3Config) (*S3, error) {
	endpoint := strings.TrimPrefix(strings.TrimPrefix(cfg.Endpoint, "https://"), "http://")
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: !cfg.Insecure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, err
	}
	return &S3{client: client, bucket: cfg.Bucket}, nil
}

// EnsureBucket creates the bucket if it doesn't exist yet.
func (s *S3) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil || exists {
		return err
	}
	return s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{})
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject is lazy; Stat surfaces a missing key now rather than on first read.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return obj, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}
