package backup

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	appcfg "github.com/mx-space/core/internal/config"
)

type s3Uploader struct {
	endpoint     *url.URL
	bucket       string
	region       string
	customDomain string
	pathStyle    bool
	client       *s3.Client
}

// S3Uploader uploads binary payloads to S3-compatible object storage.
type S3Uploader interface {
	Upload(ctx context.Context, objectKey string, payload []byte, contentType string) (string, error)
}

// NewS3Uploader creates an uploader from runtime S3 options.
func NewS3Uploader(opts appcfg.S3Options) (S3Uploader, error) {
	return newS3Uploader(opts)
}

func newS3Uploader(opts appcfg.S3Options) (*s3Uploader, error) {
	bucket := strings.TrimSpace(opts.Bucket)
	region := strings.TrimSpace(opts.Region)
	accessKey := strings.TrimSpace(opts.AccessKeyID)
	secretKey := strings.TrimSpace(opts.SecretAccessKey)
	if bucket == "" || region == "" || accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("incomplete s3 config: bucket/region/access_key_id/secret_access_key are required")
	}

	endpointText := strings.TrimSpace(opts.Endpoint)
	var endpointURL *url.URL
	if endpointText != "" {
		if !strings.HasPrefix(endpointText, "http://") && !strings.HasPrefix(endpointText, "https://") {
			endpointText = "https://" + endpointText
		}
		endpointText = strings.TrimSuffix(endpointText, "/")
		parsed, err := url.Parse(endpointText)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("invalid s3 endpoint: %s", endpointText)
		}
		endpointURL = parsed
	}

	// Follows the "path-style" switch; forcing it whenever an endpoint was set broke providers
	// that only accept virtual-hosted style (e.g. Aliyun OSS).
	pathStyle := opts.PathStyleAccess

	awsCfg := aws.Config{
		Region: region,
		// Backups can be large; callers bound uploads with their context instead.
		HTTPClient:  &http.Client{Timeout: 10 * time.Minute},
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		// Recent SDKs add CRC32 trailers by default, which many S3-compatible services reject.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = pathStyle
		if endpointURL != nil {
			o.BaseEndpoint = aws.String(endpointURL.String())
		}
	})

	return &s3Uploader{
		endpoint:     endpointURL,
		bucket:       bucket,
		region:       region,
		customDomain: strings.TrimRight(strings.TrimSpace(opts.CustomDomain), "/"),
		pathStyle:    pathStyle,
		client:       client,
	}, nil
}

func (u *s3Uploader) Upload(ctx context.Context, objectKey string, payload []byte, contentType string) (string, error) {
	key := normalizeObjectKey(objectKey)
	if key == "" {
		return "", fmt.Errorf("invalid s3 object key")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	_, err := u.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(u.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(payload),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(int64(len(payload))),
	})
	if err != nil {
		return "", fmt.Errorf("s3 upload failed: %w", err)
	}

	return u.publicURL(key), nil
}

func (u *s3Uploader) publicURL(objectKey string) string {
	encodedKey := encodeObjectKey(objectKey)
	if u.customDomain != "" {
		return u.customDomain + "/" + encodedKey
	}

	if u.endpoint != nil {
		endpoint := *u.endpoint
		endpoint.RawQuery = ""
		endpoint.Fragment = ""

		// url.URL escapes Path itself; passing the escaped key here would encode it twice.
		if u.pathStyle {
			endpoint.Path = joinURLPath(endpoint.Path, u.bucket, objectKey)
			return endpoint.String()
		}

		host := endpoint.Hostname()
		if !strings.HasPrefix(strings.ToLower(host), strings.ToLower(u.bucket)+".") {
			host = u.bucket + "." + host
		}
		if port := endpoint.Port(); port != "" {
			endpoint.Host = host + ":" + port
		} else {
			endpoint.Host = host
		}
		endpoint.Path = joinURLPath(endpoint.Path, objectKey)
		return endpoint.String()
	}

	if u.pathStyle {
		return fmt.Sprintf("https://s3.%s.amazonaws.com/%s/%s", u.region, u.bucket, encodedKey)
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", u.bucket, u.region, encodedKey)
}

func normalizeObjectKey(key string) string {
	key = strings.TrimSpace(strings.ReplaceAll(key, "\\", "/"))
	key = strings.TrimPrefix(key, "/")
	for strings.Contains(key, "//") {
		key = strings.ReplaceAll(key, "//", "/")
	}
	return key
}

func encodeObjectKey(key string) string {
	key = normalizeObjectKey(key)
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func joinURLPath(parts ...string) string {
	segments := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		for _, seg := range strings.Split(p, "/") {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			segments = append(segments, seg)
		}
	}
	if len(segments) == 0 {
		return "/"
	}
	return "/" + strings.Join(segments, "/")
}

// S3Deleter removes objects previously uploaded through the same options.
type S3Deleter interface {
	DeleteByURL(ctx context.Context, publicURL string) error
}

// NewS3Deleter creates a deleter from runtime S3 options.
func NewS3Deleter(opts appcfg.S3Options) (S3Deleter, error) {
	return newS3Uploader(opts)
}

// DeleteByURL deletes the object behind a URL produced by Upload (custom domain, path-style or
// virtual-hosted). URLs that do not belong to this bucket are refused.
func (u *s3Uploader) DeleteByURL(ctx context.Context, publicURL string) error {
	key, ok := u.keyFromURL(publicURL)
	if !ok {
		return fmt.Errorf("url does not belong to the configured bucket")
	}
	_, err := u.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(u.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("s3 delete failed: %w", err)
	}
	return nil
}

func (u *s3Uploader) keyFromURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if u.customDomain != "" && strings.HasPrefix(raw, u.customDomain+"/") {
		return decodeObjectKey(strings.TrimPrefix(raw, u.customDomain+"/"))
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.TrimPrefix(parsed.EscapedPath(), "/")
	bucketPrefix := url.PathEscape(u.bucket) + "/"
	switch {
	case strings.HasPrefix(host, strings.ToLower(u.bucket)+"."):
		return decodeObjectKey(path)
	case strings.HasPrefix(path, bucketPrefix):
		if u.endpoint != nil && !strings.EqualFold(u.endpoint.Hostname(), host) {
			return "", false
		}
		return decodeObjectKey(strings.TrimPrefix(path, bucketPrefix))
	}
	return "", false
}

func decodeObjectKey(escaped string) (string, bool) {
	key, err := url.PathUnescape(escaped)
	if err != nil {
		return "", false
	}
	key = normalizeObjectKey(key)
	return key, key != ""
}
