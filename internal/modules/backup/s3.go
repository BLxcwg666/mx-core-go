package backup

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	appcfg "github.com/mx-space/core/internal/config"
)

type s3Uploader struct {
	endpoint     *url.URL
	bucket       string
	region       string
	accessKey    string
	secretKey    string
	customDomain string
	pathStyle    bool
	client       *http.Client
}

func newS3Uploader(opts appcfg.S3Options) (*s3Uploader, error) {
	bucket := strings.TrimSpace(opts.Bucket)
	region := strings.TrimSpace(opts.Region)
	accessKey := strings.TrimSpace(opts.AccessKeyID)
	secretKey := strings.TrimSpace(opts.SecretAccessKey)
	if bucket == "" || region == "" || accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("incomplete s3 config: bucket/region/access_key_id/secret_access_key are required")
	}

	endpoint := strings.TrimSpace(opts.Endpoint)
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://s3.%s.amazonaws.com", region)
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "https://" + endpoint
	}
	endpoint = strings.TrimSuffix(endpoint, "/")

	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid s3 endpoint: %s", endpoint)
	}

	pathStyle := opts.PathStyleAccess
	if opts.Endpoint != "" && !opts.PathStyleAccess {
		pathStyle = true
	}

	return &s3Uploader{
		endpoint:     parsed,
		bucket:       bucket,
		region:       region,
		accessKey:    accessKey,
		secretKey:    secretKey,
		customDomain: strings.TrimRight(strings.TrimSpace(opts.CustomDomain), "/"),
		pathStyle:    pathStyle,
		client:       &http.Client{Timeout: 45 * time.Second},
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

	requestURL, canonicalURI, host, err := u.buildTarget(key)
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()
	xAmzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(payload)

	headers := map[string]string{
		"content-length":       strconv.Itoa(len(payload)),
		"content-type":         contentType,
		"host":                 host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           xAmzDate,
	}

	sortedKeys := make([]string, 0, len(headers))
	for k := range headers {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	canonicalHeaderLines := make([]string, 0, len(sortedKeys))
	signedHeaders := make([]string, 0, len(sortedKeys))
	for _, k := range sortedKeys {
		canonicalHeaderLines = append(canonicalHeaderLines, k+":"+strings.TrimSpace(headers[k]))
		signedHeaders = append(signedHeaders, k)
	}
	canonicalHeaders := strings.Join(canonicalHeaderLines, "\n")
	signedHeadersStr := strings.Join(signedHeaders, ";")

	canonicalRequest := strings.Join([]string{
		http.MethodPut,
		canonicalURI,
		"",
		canonicalHeaders + "\n",
		signedHeadersStr,
		payloadHash,
	}, "\n")

	credentialScope := dateStamp + "/" + u.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		xAmzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(u.secretKey, dateStamp, u.region, "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	authorization := "AWS4-HMAC-SHA256 Credential=" + u.accessKey + "/" + credentialScope +
		", SignedHeaders=" + signedHeadersStr +
		", Signature=" + signature

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, requestURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Host = host
	for _, k := range sortedKeys {
		req.Header.Set(k, headers[k])
	}
	req.Header.Set("Authorization", authorization)

	resp, err := u.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", fmt.Errorf("s3 upload failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return u.publicURL(key), nil
}

func (u *s3Uploader) buildTarget(objectKey string) (requestURL, canonicalURI, host string, err error) {
	encodedKey := encodeObjectKey(objectKey)
	basePath := strings.TrimSuffix(u.endpoint.Path, "/")

	if u.pathStyle {
		canonicalURI = joinURLPath(basePath, u.bucket, encodedKey)
		host = u.endpoint.Host
		requestURL = u.endpoint.Scheme + "://" + host + canonicalURI
		return requestURL, canonicalURI, host, nil
	}

	host = u.endpoint.Host
	if !strings.HasPrefix(strings.ToLower(host), strings.ToLower(u.bucket)+".") {
		host = u.bucket + "." + host
	}
	canonicalURI = joinURLPath(basePath, encodedKey)
	requestURL = u.endpoint.Scheme + "://" + host + canonicalURI
	return requestURL, canonicalURI, host, nil
}

func (u *s3Uploader) publicURL(objectKey string) string {
	if u.customDomain != "" {
		return u.customDomain + "/" + objectKey
	}
	requestURL, _, _, err := u.buildTarget(objectKey)
	if err != nil {
		return ""
	}
	return requestURL
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

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}
