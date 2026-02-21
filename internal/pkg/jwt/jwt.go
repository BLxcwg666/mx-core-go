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
	UserID string `json:"uid"`
	jwtlib.RegisteredClaims
}

// Sign creates a signed JWT token for the given user ID.
func Sign(userID string, ttl time.Duration) (string, error) {
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwtlib.NewNumericDate(time.Now()),
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
