package telegram

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/chechiachang/abot/internal/config"
)

type R2ObjectStore interface {
	Put(context.Context, string, []byte) error
	PresignGet(context.Context, string, time.Duration) (string, error)
}

type R2Uploader struct {
	store    R2ObjectStore
	bucket   string
	expiry   time.Duration
	maxBytes int64
}

func NewR2Uploader(cfg config.R2Config) (*R2Uploader, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Enabled() {
		return nil, nil
	}
	client := s3.NewFromConfig(aws.Config{
		Region:      cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(strings.TrimSpace(cfg.AccessKeyID), strings.TrimSpace(cfg.SecretAccessKey), ""),
		HTTPClient:  &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(strings.TrimSuffix(cfg.Endpoint, "/"))
		o.UsePathStyle = true
	})
	return &R2Uploader{store: &s3ObjectStore{client: client}, bucket: cfg.Bucket, expiry: cfg.PresignExpiry, maxBytes: cfg.MaxBytes}, nil
}

func (u *R2Uploader) Store(ctx context.Context, text string) (string, error) {
	if len(text) == 0 || int64(len(text)) > u.maxBytes || !utf8.ValidString(text) {
		return "", errors.New("response exceeds the R2 size limit")
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("could not create response object")
	}
	locator := u.bucket + "/abot/responses/" + hex.EncodeToString(random[:]) + ".txt"
	if err := u.store.Put(ctx, locator, []byte(text)); err != nil {
		return "", errors.New("could not upload long response")
	}
	return locator, nil
}

func (u *R2Uploader) Presign(ctx context.Context, locator string, expiry time.Duration) (string, error) {
	bucket, key, err := splitObjectLocator(locator)
	if err != nil || bucket != u.bucket || !strings.HasPrefix(key, "abot/responses/") {
		return "", errors.New("invalid long-response object locator")
	}
	if expiry < time.Second || expiry >= 24*time.Hour {
		return "", errors.New("invalid long-response link expiry")
	}
	link, err := u.store.PresignGet(ctx, locator, expiry)
	if err != nil {
		return "", errors.New("could not create long-response link")
	}
	return link, nil
}

type s3ObjectStore struct{ client *s3.Client }

func (s *s3ObjectStore) Put(ctx context.Context, locator string, body []byte) error {
	bucket, key, err := splitObjectLocator(locator)
	if err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(body),
		ContentType:  aws.String("text/plain; charset=utf-8"),
		CacheControl: aws.String("no-store"),
	})
	return err
}

func (s *s3ObjectStore) PresignGet(ctx context.Context, locator string, expiry time.Duration) (string, error) {
	bucket, key, err := splitObjectLocator(locator)
	if err != nil {
		return "", err
	}
	request, err := s3.NewPresignClient(s.client).PresignGetObject(ctx,
		&s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", err
	}
	return request.URL, nil
}

func splitObjectLocator(locator string) (string, string, error) {
	bucket, key, found := strings.Cut(locator, "/")
	if !found || bucket == "" || key == "" || strings.Contains(key, "..") || strings.ContainsAny(key, "\\?#") {
		return "", "", errors.New("invalid object locator")
	}
	return bucket, key, nil
}
