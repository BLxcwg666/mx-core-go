package config

import (
	"fmt"
	"net"
	neturl "net/url"
	"strconv"
	"strings"
)

func (c DatabaseRuntimeConfig) DSNValue() string {
	if v := strings.TrimSpace(c.DSN); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.URL); v != "" {
		return v
	}

	host := strings.TrimSpace(c.Host)
	if host == "" {
		host = defaultDBHost
	}
	port := c.Port
	if port == 0 {
		port = defaultDBPort
	}
	user := strings.TrimSpace(c.User)
	if user == "" {
		user = strings.TrimSpace(c.Username)
	}
	if user == "" {
		user = defaultDBUser
	}
	password := strings.TrimSpace(c.Password)
	if password == "" {
		password = defaultDBPassword
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = strings.TrimSpace(c.DBName)
	}
	if name == "" {
		name = defaultDBName
	}
	charset := strings.TrimSpace(c.Charset)
	if charset == "" {
		charset = defaultDBCharset
	}
	loc := strings.TrimSpace(c.Loc)
	if loc == "" {
		loc = defaultDBLoc
	}

	params := neturl.Values{}
	for key, value := range c.Params {
		k := strings.TrimSpace(key)
		v := strings.TrimSpace(value)
		if k != "" && v != "" {
			params.Set(k, v)
		}
	}
	if params.Get("charset") == "" {
		params.Set("charset", charset)
	}
	if params.Get("parseTime") == "" {
		params.Set("parseTime", strconv.FormatBool(c.ParseTime))
	}
	if params.Get("loc") == "" {
		params.Set("loc", loc)
	}

	auth := ""
	if user != "" || password != "" {
		auth = user
		if password != "" {
			auth += ":" + password
		}
		auth += "@"
	}

	dsn := fmt.Sprintf("%stcp(%s)/%s", auth, net.JoinHostPort(host, strconv.Itoa(port)), name)
	query := params.Encode()
	if query != "" {
		dsn += "?" + query
	}
	return dsn
}

func (c RedisRuntimeConfig) URLValue() string {
	if u := normalizeRedisRawURL(c.URL); u != "" {
		return u
	}

	host := strings.TrimSpace(c.Host)
	if host == "" {
		host = defaultRedisHost
	}
	port := c.Port
	if port == 0 {
		port = defaultRedisPort
	}
	db := c.DB
	if db < 0 {
		db = defaultRedisDB
	}

	scheme := strings.ToLower(strings.TrimSpace(c.Scheme))
	if scheme == "" {
		if c.TLS {
			scheme = "rediss"
		} else {
			scheme = "redis"
		}
	}
	if scheme != "redis" && scheme != "rediss" {
		scheme = "redis"
	}

	u := &neturl.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/" + strconv.Itoa(db),
	}
	username := strings.TrimSpace(c.Username)
	password := strings.TrimSpace(c.Password)
	if username != "" {
		if password != "" {
			u.User = neturl.UserPassword(username, password)
		} else {
			u.User = neturl.User(username)
		}
	} else if password != "" {
		u.User = neturl.UserPassword("", password)
	}

	if len(c.Params) > 0 {
		query := neturl.Values{}
		for key, value := range c.Params {
			k := strings.TrimSpace(key)
			v := strings.TrimSpace(value)
			if k != "" && v != "" {
				query.Set(k, v)
			}
		}
		if len(query) > 0 {
			u.RawQuery = query.Encode()
		}
	}

	return u.String()
}

func (c MeiliSearchRuntimeConfig) Endpoint() string {
	if c.URL != "" {
		if strings.HasPrefix(c.URL, "http://") || strings.HasPrefix(c.URL, "https://") {
			return c.URL
		}
		return "http://" + c.URL
	}

	host := c.Host
	if host == "" {
		host = defaultMeiliHost
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return strings.TrimRight(host, "/")
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return "http://" + host
	}

	port := c.Port
	if port == 0 {
		port = defaultMeiliPort
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}
