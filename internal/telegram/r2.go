package telegram

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const defaultR2MaxBytes int64 = 5 << 20

type R2Config struct {
	AccountID       string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Endpoint        string
	Prefix          string
	Expiry          time.Duration
	MaxBytes        int64
}

type R2ObjectStore interface {
	Put(context.Context, string, []byte, string) error
	PresignGet(context.Context, string, time.Duration) (string, error)
}

type R2Uploader struct {
	store    R2ObjectStore
	bucket   string
	prefix   string
	expiry   time.Duration
	maxBytes int64
}

func NewR2Uploader(ctx context.Context, cfg R2Config) (*R2Uploader, error) {
	cfg.AccountID, cfg.Bucket, cfg.AccessKeyID, cfg.SecretAccessKey = strings.TrimSpace(cfg.AccountID), strings.TrimSpace(cfg.Bucket), strings.TrimSpace(cfg.AccessKeyID), strings.TrimSpace(cfg.SecretAccessKey)
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if cfg.AccountID == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("R2 account ID, bucket, and credentials are required")
	}
	if endpoint == "" {
		endpoint = "https://" + cfg.AccountID + ".r2.cloudflarestorage.com"
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("R2 endpoint must be an HTTPS origin without credentials, path, or query")
	}
	if cfg.Expiry == 0 {
		cfg.Expiry = 15 * time.Minute
	}
	if cfg.Expiry < time.Minute || cfg.Expiry > 7*24*time.Hour {
		return nil, errors.New("R2 presigned URL expiry must be between 1m and 168h")
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = defaultR2MaxBytes
	}
	if cfg.MaxBytes < 1 || cfg.MaxBytes > 50<<20 {
		return nil, errors.New("R2 maximum response size must be between 1 byte and 50 MiB")
	}
	prefix := strings.Trim(cfg.Prefix, "/")
	if strings.Contains(prefix, "..") || strings.ContainsAny(prefix, "\\?#") {
		return nil, errors.New("R2 object prefix is invalid")
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, errors.New("initialize R2 client")
	}
	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
	return newR2Uploader(&s3ObjectStore{client: client}, cfg.Bucket, prefix, cfg.Expiry, cfg.MaxBytes), nil
}

func newR2Uploader(store R2ObjectStore, bucket, prefix string, expiry time.Duration, maxBytes int64) *R2Uploader {
	return &R2Uploader{store: store, bucket: bucket, prefix: prefix, expiry: expiry, maxBytes: maxBytes}
}

func (u *R2Uploader) Store(ctx context.Context, text string) (string, error) {
	if u == nil || u.store == nil || u.bucket == "" {
		return "", errors.New("long-response storage is not configured")
	}
	body := []byte(text)
	if len(body) == 0 || int64(len(body)) > u.maxBytes {
		return "", errors.New("response exceeds the configured long-response size limit")
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("could not create response object")
	}
	key := hex.EncodeToString(random[:]) + ".txt"
	if u.prefix != "" {
		key = u.prefix + "/" + key
	}
	locator := u.bucket + "/" + key
	if err := u.store.Put(ctx, locator, body, "text/plain; charset=utf-8"); err != nil {
		return "", errors.New("could not upload long response")
	}
	return locator, nil
}

func (u *R2Uploader) Presign(ctx context.Context, locator string, expiry time.Duration) (string, error) {
	if u == nil || u.store == nil {
		return "", errors.New("long-response storage is not configured")
	}
	bucket, key, err := splitObjectLocator(locator)
	if err != nil || bucket != u.bucket || (u.prefix != "" && !strings.HasPrefix(key, u.prefix+"/")) {
		return "", errors.New("invalid long-response object locator")
	}
	if expiry < time.Minute || expiry > 7*24*time.Hour {
		return "", errors.New("R2 presigned URL expiry must be between 1m and 168h")
	}
	link, err := u.store.PresignGet(ctx, locator, expiry)
	if err != nil {
		return "", errors.New("could not create long-response link")
	}
	return link, nil
}

// put & presign are deliberately separate to expose only the configured bucket and random key.
type s3ObjectStore struct{ client *s3.Client }

func (s *s3ObjectStore) Put(ctx context.Context, locator string, body []byte, contentType string) error {
	bucket, key, err := splitObjectLocator(locator)
	if err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(body), ContentType: aws.String(contentType)})
	if err != nil {
		return errors.New("R2 object upload failed")
	}
	return nil
}

func (s *s3ObjectStore) PresignGet(ctx context.Context, locator string, expiry time.Duration) (string, error) {
	bucket, key, err := splitObjectLocator(locator)
	if err != nil {
		return "", err
	}
	presigner := s3.NewPresignClient(s.client)
	request, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", errors.New("R2 link signing failed")
	}
	return request.URL, nil
}

func splitObjectLocator(value string) (string, string, error) {
	bucket, key, found := strings.Cut(value, "/")
	if !found || bucket == "" || key == "" || strings.Contains(key, "..") {
		return "", "", errors.New("invalid object locator")
	}
	return bucket, key, nil
}
