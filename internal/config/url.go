package config

import "strings"

// APIPrefix is the versioned API mount point.
const APIPrefix = "/api/v2"

// APIBaseURL returns the public API base (".../api/v2") derived from server_url.
// server_url is accepted both with and without the API prefix, since the setup wizard fills in "<origin>/api/v2".
func (u URLConfig) APIBaseURL() string {
	base := strings.TrimRight(strings.TrimSpace(u.ServerURL), "/")
	if base == "" {
		return ""
	}
	if strings.HasSuffix(base, APIPrefix) {
		return base
	}
	return base + APIPrefix
}
