package jwt

import (
	"fmt"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

const defaultSecret = "mx-space-secret-change-me"

var secret = []byte(defaultSecret)

// SetSecret configures the JWT signing secret (call on startup).
func SetSecret(s string) {
	if s != "" {
		secret = []byte(s)
	}
}

// Claims is the JWT payload.
type Claims struct {
	UserID    string `json:"uid"`
	SessionID string `json:"sid,omitempty"`
	IP        string `json:"ip,omitempty"`
	UA        string `json:"ua,omitempty"`
	jwtlib.RegisteredClaims
}

type SignOptions struct {
	SessionID string
	IP        string
	UA        string
}

// Sign creates a signed JWT token for the given user ID.
func Sign(userID string, ttl time.Duration) (string, error) {
	return SignWithOptions(userID, ttl, SignOptions{})
}

// SignWithOptions creates a signed JWT token and attaches extra session metadata.
func SignWithOptions(userID string, ttl time.Duration, opts SignOptions) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		SessionID: opts.SessionID,
		IP:        opts.IP,
		UA:        opts.UA,
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwtlib.NewNumericDate(now),
		},
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// Parse validates a token string and returns the claims.
func Parse(tokenStr string) (*Claims, error) {
	token, err := jwtlib.ParseWithClaims(tokenStr, &Claims{}, func(t *jwtlib.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwtlib.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}
