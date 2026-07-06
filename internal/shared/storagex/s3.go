// Package storagex — Django default_storage (S3/B2, media bucket) bilan ishlash:
// fayl o'chirish (cleanup_expired_files), o'qish (broadcast/chat media) va
// yozish (sync_bot avatar).
package storagex

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Storage struct {
	client *minio.Client
	bucket string
}

func New(endpoint, accessKey, secretKey, region, bucket string) (*Storage, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	host := u.Host
	if host == "" {
		host = u.Path // "s3.example.com" (protokolsiz) berilgan holat
	}
	client, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: u.Scheme != "http",
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	return &Storage{client: client, bucket: bucket}, nil
}

// Delete — Django FileField name (masalan "media/abc.pdf") bo'yicha o'chirish.
func (s *Storage) Delete(ctx context.Context, name string) error {
	return s.client.RemoveObject(ctx, s.bucket, strings.TrimPrefix(name, "/"), minio.RemoveObjectOptions{})
}

func (s *Storage) Read(ctx context.Context, name string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, strings.TrimPrefix(name, "/"), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	return io.ReadAll(obj)
}

// Write — nom band bo'lsa Django'dagi kabi ustidan yozmaydi deb o'tirmaymiz:
// avatar/<bot_id>.jpg deterministik nom, ustidan yozish ATAYIN (eski avatar yangisiga almashadi).
func (s *Storage) Write(ctx context.Context, name string, content []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, strings.TrimPrefix(name, "/"),
		bytes.NewReader(content), int64(len(content)),
		minio.PutObjectOptions{ContentType: contentType})
	return err
}
