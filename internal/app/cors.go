package app

import (
	"net/url"
	"strings"
)

// extractOriginHost returns the "host[:port]" portion of an origin URL.
func extractOriginHost(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return origin
	}
	return u.Host
}

// matchOriginPattern reports whether host matches the given wildcard pattern.
func matchOriginPattern(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:]
		return strings.HasSuffix(host, suffix)
	}
	if strings.HasSuffix(pattern, ":*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(host, prefix)
	}
	return false
}
