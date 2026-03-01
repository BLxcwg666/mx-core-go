package link

import (
	"fmt"
	"net/url"
	"strings"
)

func extractDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	host = strings.TrimPrefix(host, "www.")
	return host
}

func normalizeApplyLinkURL(raw string, allowSubPath bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("invalid url")
	}
	origin := parsed.Scheme + "://" + parsed.Host
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if path != "/" && !allowSubPath {
		return "", errSubpathLinkDisable
	}
	if allowSubPath {
		return origin + path, nil
	}
	return origin, nil
}
