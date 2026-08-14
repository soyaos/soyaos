// SilentCut reverse-pressure: S3 (proper AWS plus the compatible cohort —
// MinIO, B2, R2, Wasabi) is where studios push masters destined for the CDN.

package nas

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type s3Handle struct {
	mu     sync.Mutex
	cfg    Config
	remote s3Remote
	closed bool
}

type s3Remote interface {
	put(context.Context, string, string, io.Reader) (int64, error)
	close()
}

type minioRemote struct {
	client    *minio.Client
	transport *http.Transport
}

func openS3(_ context.Context, cfg Config) (NAS, error) {
	bucket := cfg.Bucket
	if bucket == "" {
		bucket = cfg.Share
	}
	if bucket == "" || cfg.Username == "" || cfg.Password == "" {
		return nil, errors.Join(ErrInvalidConfig, errors.New("s3 bucket, access key and secret key are required"))
	}
	rawEndpoint := cfg.Endpoint
	if rawEndpoint == "" {
		rawEndpoint = cfg.Host
	}
	endpoint, secure, err := s3Endpoint(rawEndpoint)
	if err != nil {
		return nil, err
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = timeoutFor(cfg)
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.Username, cfg.Password, cfg.SessionToken),
		Secure:       secure,
		Region:       region,
		Transport:    transport,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, errors.Join(ErrInvalidConfig, fmt.Errorf("s3 endpoint: %w", err))
	}
	client.SetAppInfo("soyaos-nas-connector", "alpha")
	return &s3Handle{cfg: cfg, remote: &minioRemote{client: client, transport: transport}}, nil
}

func (h *s3Handle) Write(ctx context.Context, name string, r io.Reader) (int64, error) {
	cleaned, err := cleanRemotePath(name)
	if err != nil {
		return 0, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, ErrClosed
	}
	bucket := h.cfg.Bucket
	if bucket == "" {
		bucket = h.cfg.Share
	}
	n, err := h.remote.put(ctx, bucket, cleaned, r)
	if err != nil {
		return n, fmt.Errorf("s3: put object: %w", err)
	}
	return n, nil
}

func (h *s3Handle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	if h.remote != nil {
		h.remote.close()
		h.remote = nil
	}
	wipe(&h.cfg)
	return nil
}

func (r *minioRemote) put(ctx context.Context, bucket, object string, body io.Reader) (int64, error) {
	counter := &countingReader{r: body}
	info, err := r.client.PutObject(ctx, bucket, object, counter, -1, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		return counter.n, err
	}
	if info.Size != counter.n {
		return counter.n, fmt.Errorf("server acknowledged %d bytes after client sent %d", info.Size, counter.n)
	}
	return counter.n, nil
}

func (r *minioRemote) close() { r.transport.CloseIdleConnections() }

func s3Endpoint(raw string) (endpoint string, secure bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, errors.Join(ErrInvalidConfig, errors.New("s3 endpoint is required"))
	}
	u, parseErr := url.Parse(raw)
	if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", false, errors.Join(ErrInvalidConfig, errors.New("s3 endpoint must be http(s)://host[:port] without credentials or path"))
	}
	return u.Host, u.Scheme == "https", nil
}
