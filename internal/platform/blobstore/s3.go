package blobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Options struct {
	Bucket, Region, Endpoint, Prefix string
	PathStyle, AllowHTTP             bool
}

func (o S3Options) Validate() error {
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(o.Bucket) {
		return errors.New("S3 bucket must be a valid general purpose bucket name")
	}
	if o.Prefix != "" {
		for _, part := range strings.Split(o.Prefix, "/") {
			if !component.MatchString(part) {
				return errors.New("invalid S3 key prefix")
			}
		}
	}
	if o.Endpoint != "" {
		u, err := url.Parse(o.Endpoint)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("S3 endpoint must be an HTTP(S) origin without credentials, query or path")
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && o.AllowHTTP) {
			return errors.New("S3 endpoint requires HTTPS; HTTP requires WAVE_S3_ALLOW_HTTP=true")
		}
	}
	return nil
}

type s3Backend struct {
	client         *s3.Client
	bucket, prefix string
}

func NewS3(ctx context.Context, o S3Options) (*Store, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithHTTPClient(&http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("S3 redirects are disabled") }}),
		awsconfig.WithRetryMaxAttempts(3),
	}
	if o.Region != "" {
		opts = append(opts, awsconfig.WithRegion(o.Region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load S3 configuration: %w", err)
	}
	if cfg.Region == "" {
		return nil, errors.New("S3 requires WAVE_S3_REGION or an AWS SDK region configuration")
	}
	client := s3.NewFromConfig(cfg, func(c *s3.Options) {
		c.UsePathStyle = o.PathStyle
		if o.Endpoint != "" {
			c.BaseEndpoint = aws.String(o.Endpoint)
		}
	})
	prefix := o.Prefix
	if prefix != "" {
		prefix += "/"
	}
	return &Store{backend: &s3Backend{client: client, bucket: o.Bucket, prefix: prefix}}, nil
}

func (s *s3Backend) put(ctx context.Context, key string, data []byte) error {
	// Content-derived keys are immutable by construction. Retrying a complete PUT
	// publishes the same bytes. Leave ACLs unset and honor bucket encryption policy.
	sum := sha256.Sum256(data)
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key),
		Body: bytes.NewReader(data), ContentLength: aws.Int64(int64(len(data))),
		ContentType:    aws.String("application/octet-stream"),
		ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(sum[:])),
	})
	return err
}

func (s *s3Backend) open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key)})
	if err != nil {
		return nil, err
	}
	if out.ContentLength == nil || *out.ContentLength < 0 {
		out.Body.Close()
		return nil, errors.New("S3 response missing object size")
	}
	return &s3Reader{ctx: ctx, backend: s, key: s.prefix + key, body: out.Body, size: *out.ContentLength, etag: out.ETag}, nil
}

// s3Reader implements seeking with ranged GETs, keeping HTTP downloads streaming
// while allowing net/http.ServeContent to handle Range and conditional requests.
// It is request-scoped and must not be shared between goroutines.
type s3Reader struct {
	ctx       context.Context
	backend   *s3Backend
	key       string
	body      io.ReadCloser
	size, pos int64
	etag      *string
	closed    bool
}

func (r *s3Reader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, errors.New("read from closed S3 object")
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.pos >= r.size {
		return 0, io.EOF
	}
	if r.body == nil {
		out, err := r.backend.client.GetObject(r.ctx, &s3.GetObjectInput{
			Bucket: aws.String(r.backend.bucket), Key: aws.String(r.key),
			Range: aws.String(fmt.Sprintf("bytes=%d-", r.pos)), IfMatch: r.etag,
		})
		if err != nil {
			return 0, err
		}
		expected := fmt.Sprintf("bytes %d-%d/%d", r.pos, r.size-1, r.size)
		if aws.ToString(out.ContentRange) != expected || aws.ToInt64(out.ContentLength) != r.size-r.pos {
			out.Body.Close()
			return 0, errors.New("S3 server returned an invalid byte range")
		}
		r.body = out.Body
	}
	if int64(len(p)) > r.size-r.pos {
		p = p[:r.size-r.pos]
	}
	n, err := r.body.Read(p)
	r.pos += int64(n)
	if err == io.EOF && r.pos < r.size {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (r *s3Reader) Seek(offset int64, whence int) (int64, error) {
	if r.closed {
		return 0, errors.New("seek on closed S3 object")
	}
	base := int64(0)
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = r.pos
	case io.SeekEnd:
		base = r.size
	default:
		return 0, errors.New("invalid seek origin")
	}
	next := base + offset
	if next < 0 || (offset > 0 && next < base) {
		return 0, errors.New("invalid seek offset")
	}
	if next != r.pos && r.body != nil {
		r.body.Close()
		r.body = nil
	}
	r.pos = next
	return next, nil
}
func (r *s3Reader) Close() error {
	r.closed = true
	if r.body != nil {
		err := r.body.Close()
		r.body = nil
		return err
	}
	return nil
}
