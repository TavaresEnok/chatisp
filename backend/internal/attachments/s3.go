package attachments

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// etagVersionPrefix marks an object_version that is the ETag captured at
// creation, used when the bucket has no object versioning.
const etagVersionPrefix = "etag:"

// S3Config is explicit: no ambient credentials, metadata lookup or public ACL.
// Endpoint is an operator-controlled HTTPS S3-compatible endpoint. The bucket
// MUST be private.
//
// Versioned (the default) requires bucket versioning: every write returns a
// version id and reads, links and deletions name it. Unversioned is for
// S3-compatible services without versioning (Garage 2.x, tested 05/11/2026):
// the object identity is the ETag captured at creation, creation refuses an
// existing key, reads require If-Match, and links carry no versionId. Keys
// are already unique (opaque tenant namespace + random UUID).
//
// CAFile adds a PEM root to the system pool; TLS verification is never
// disabled. PublicEndpoint, when set, is the HTTPS endpoint the browser uses
// for signed links (same bucket, reached through another host name); the
// signature covers that host, so the proxy in front must keep the Host header.
type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey, SessionToken string
	PathStyle                                                    bool
	Unversioned                                                  bool
	CAFile                                                       string
	PublicEndpoint                                               string
}
type S3Storage struct {
	client      *s3.Client
	signer      *s3.PresignClient
	bucket      string
	unversioned bool
}

func NewS3(c S3Config) (*S3Storage, error) { return newS3(c, nil) }

func validEndpoint(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}

// transport injection is package-private and used only for simulated SDK tests.
func newS3(c S3Config, transport http.RoundTripper) (*S3Storage, error) {
	if !validEndpoint(c.Endpoint) || (c.PublicEndpoint != "" && !validEndpoint(c.PublicEndpoint)) || c.Region == "" || c.Bucket == "" || strings.ContainsAny(c.Bucket, "/\\") || c.AccessKey == "" || c.SecretKey == "" {
		return nil, ErrStorage
	}
	if transport == nil {
		base := http.DefaultTransport.(*http.Transport).Clone()
		if c.CAFile != "" {
			pem, err := os.ReadFile(c.CAFile)
			if err != nil {
				return nil, ErrStorage
			}
			pool, err := x509.SystemCertPool()
			if err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, ErrStorage
			}
			base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
		}
		transport = base
	} else if c.CAFile != "" {
		return nil, ErrStorage
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	cfg := aws.Config{Region: c.Region, HTTPClient: client, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: c.AccessKey, SecretAccessKey: c.SecretKey, SessionToken: c.SessionToken, Source: "explicit-attachment-config"}, nil
	}), RetryMaxAttempts: 1}
	api := s3.NewFromConfig(cfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(c.Endpoint); o.UsePathStyle = c.PathStyle })
	signing := api
	if c.PublicEndpoint != "" {
		signing = s3.NewFromConfig(cfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(c.PublicEndpoint); o.UsePathStyle = c.PathStyle })
	}
	return &S3Storage{client: api, signer: s3.NewPresignClient(signing), bucket: c.Bucket, unversioned: c.Unversioned}, nil
}

func notFound(e error) bool {
	var apiErr smithy.APIError
	return errors.As(e, &apiErr) && (apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey")
}

func (s *S3Storage) Create(ctx context.Context, key string, body io.ReadSeeker, n int64) (string, error) {
	if s.unversioned {
		// Services without versioning may ignore If-None-Match (Garage 2.5
		// does); refuse a key that already exists before writing.
		if _, e := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}); e == nil || !notFound(e) {
			return "", ErrStorage
		}
	}
	out, e := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(n), ContentType: aws.String("application/octet-stream"), IfNoneMatch: aws.String("*")})
	if e != nil {
		return "", ErrStorage
	}
	if s.unversioned {
		etag := strings.Trim(aws.ToString(out.ETag), `"`)
		if etag == "" || strings.ContainsAny(etag, `" `) {
			return "", ErrStorage
		}
		return etagVersionPrefix + etag, nil
	}
	if out.VersionId == nil || *out.VersionId == "" || *out.VersionId == "null" {
		return "", ErrStorage
	}
	return *out.VersionId, nil
}

func (s *S3Storage) Read(ctx context.Context, key, version string) (io.ReadCloser, error) {
	if version == "" || version == "null" {
		return nil, ErrStorage
	}
	if etag, ok := strings.CutPrefix(version, etagVersionPrefix); ok {
		out, e := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), IfMatch: aws.String(`"` + etag + `"`)})
		if e != nil {
			return nil, ErrStorage
		}
		if strings.Trim(aws.ToString(out.ETag), `"`) != etag {
			out.Body.Close()
			return nil, ErrStorage
		}
		return out.Body, nil
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
	in := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ResponseContentType: aws.String("application/octet-stream"), ResponseContentDisposition: aws.String(disposition), ResponseCacheControl: aws.String("private, no-store")}
	if !strings.HasPrefix(version, etagVersionPrefix) {
		in.VersionId = aws.String(version)
	}
	out, e := s.signer.PresignGetObject(ctx, in, func(o *s3.PresignOptions) { o.Expires = ttl })
	if e != nil {
		return "", ErrStorage
	}
	return out.URL, nil
}

func (s *S3Storage) Delete(ctx context.Context, key, version string) error {
	if s.unversioned || strings.HasPrefix(version, etagVersionPrefix) {
		// One object per key: the key is unique and never rewritten.
		if _, e := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}); e != nil {
			return ErrStorage
		}
		return nil
	}
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

// Walker lists every stored object with the version string Read accepts.
// It is the backup contract: the mirror copies exactly what the database can
// reference. Delete markers are not objects.
type Walker interface {
	Walk(ctx context.Context, fn func(key, version string, size int64) error) error
}

// Walk implements Walker for S3 and S3-compatible services. Unversioned mode
// lists current objects and uses the ETag identity; versioned mode lists every
// non-delete-marker version.
func (s *S3Storage) Walk(ctx context.Context, fn func(key, version string, size int64) error) error {
	if s.unversioned {
		var token *string
		for {
			out, e := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), ContinuationToken: token})
			if e != nil {
				return ErrStorage
			}
			for _, o := range out.Contents {
				etag := strings.Trim(aws.ToString(o.ETag), `"`)
				if etag == "" || strings.ContainsAny(etag, `" `) {
					return ErrStorage
				}
				if e = fn(aws.ToString(o.Key), etagVersionPrefix+etag, aws.ToInt64(o.Size)); e != nil {
					return e
				}
			}
			if !aws.ToBool(out.IsTruncated) {
				return nil
			}
			token = out.NextContinuationToken
		}
	}
	var keyMarker, versionMarker *string
	for {
		out, e := s.client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(s.bucket), KeyMarker: keyMarker, VersionIdMarker: versionMarker})
		if e != nil {
			return ErrStorage
		}
		for _, v := range out.Versions {
			if aws.ToString(v.VersionId) == "" || aws.ToString(v.VersionId) == "null" {
				return ErrStorage
			}
			if e = fn(aws.ToString(v.Key), aws.ToString(v.VersionId), aws.ToInt64(v.Size)); e != nil {
				return e
			}
		}
		if !aws.ToBool(out.IsTruncated) {
			return nil
		}
		keyMarker, versionMarker = out.NextKeyMarker, out.NextVersionIdMarker
	}
}

// Unversioned reports whether object identity is the ETag captured at creation.
func (s *S3Storage) Unversioned() bool { return s.unversioned }
