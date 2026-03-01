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

	pathStyle := opts.PathStyleAccess
	if opts.Endpoint != "" && !opts.PathStyleAccess {
		pathStyle = true
	}

	awsCfg := aws.Config{
		Region:      region,
		HTTPClient:  &http.Client{Timeout: 45 * time.Second},
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
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

		if u.pathStyle {
			endpoint.Path = joinURLPath(endpoint.Path, u.bucket, encodedKey)
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
		endpoint.Path = joinURLPath(endpoint.Path, encodedKey)
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
