package attachments

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Config is explicit: no ambient credentials, metadata lookup or public ACL.
// Endpoint is an operator-controlled HTTPS S3-compatible endpoint. Bucket MUST
// be private, versioning enabled, and deny unversioned public reads by policy.
type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey, SessionToken string
	PathStyle                                                    bool
}
type S3Storage struct {
	client *s3.Client
	bucket string
}

func NewS3(c S3Config) (*S3Storage, error) { return newS3(c, nil) }

// transport injection is package-private and used only for simulated SDK tests.
func newS3(c S3Config, transport http.RoundTripper) (*S3Storage, error) {
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || c.Region == "" || c.Bucket == "" || strings.ContainsAny(c.Bucket, "/\\") || c.AccessKey == "" || c.SecretKey == "" {
		return nil, ErrStorage
	}
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	cfg := aws.Config{Region: c.Region, HTTPClient: client, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: c.AccessKey, SecretAccessKey: c.SecretKey, SessionToken: c.SessionToken, Source: "explicit-attachment-config"}, nil
	}), RetryMaxAttempts: 1}
	return &S3Storage{client: s3.NewFromConfig(cfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(c.Endpoint); o.UsePathStyle = c.PathStyle }), bucket: c.Bucket}, nil
}
func (s *S3Storage) Create(ctx context.Context, key string, body io.ReadSeeker, n int64) (string, error) {
	out, e := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(n), ContentType: aws.String("application/octet-stream"), IfNoneMatch: aws.String("*")})
	if e != nil || out.VersionId == nil || *out.VersionId == "" || *out.VersionId == "null" {
		return "", ErrStorage
	}
	return *out.VersionId, nil
}
func (s *S3Storage) Read(ctx context.Context, key, version string) (io.ReadCloser, error) {
	if version == "" || version == "null" {
		return nil, ErrStorage
	}
	out, e := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), VersionId: aws.String(version)})
	if e != nil {
		return nil, ErrStorage
	}
	if aws.ToString(out.VersionId) != version {
		out.Body.Close()
		return nil, ErrStorage
	}
	return out.Body, nil
}
func (s *S3Storage) Sign(ctx context.Context, key, version, name string, ttl time.Duration) (string, error) {
	if version == "" || version == "null" || ttl <= 0 || ttl > DownloadTTL {
		return "", ErrStorage
	}
	if _, e := safeName(name); e != nil {
		return "", e
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	out, e := s3.NewPresignClient(s.client).PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), VersionId: aws.String(version), ResponseContentType: aws.String("application/octet-stream"), ResponseContentDisposition: aws.String(disposition), ResponseCacheControl: aws.String("private, no-store")}, func(o *s3.PresignOptions) { o.Expires = ttl })
	if e != nil {
		return "", ErrStorage
	}
	return out.URL, nil
}
func (s *S3Storage) Delete(ctx context.Context, key, version string) error {
	if version != "" {
		_, e := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), VersionId: aws.String(version)})
		if e != nil {
			return ErrStorage
		}
		return nil
	}
	// Bounded recovery of writes whose response/DB commit was lost. Exact key
	// comparison prevents prefix-neighbor deletion. Never release quota until all
	// versions are gone. Create-only application policy normally allows one.
	out, e := s.client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(s.bucket), Prefix: aws.String(key), MaxKeys: aws.Int32(100)})
	if e != nil || aws.ToBool(out.IsTruncated) {
		return ErrStorage
	}
	for _, v := range out.Versions {
		if aws.ToString(v.Key) == key {
			if aws.ToString(v.VersionId) == "" {
				return ErrStorage
			}
			if e = s.Delete(ctx, key, aws.ToString(v.VersionId)); e != nil {
				return e
			}
		}
	}
	for _, v := range out.DeleteMarkers {
		if aws.ToString(v.Key) == key {
			if aws.ToString(v.VersionId) == "" {
				return ErrStorage
			}
			if e = s.Delete(ctx, key, aws.ToString(v.VersionId)); e != nil {
				return e
			}
		}
	}
	return nil
}
