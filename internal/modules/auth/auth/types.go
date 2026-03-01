package auth

import (
	"errors"
	"strings"
	"time"
)

type LoginDTO struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type RegisterDTO struct {
	Username string `json:"username" binding:"required,min=3"`
	Password string `json:"password" binding:"required,min=6"`
	Name     string `json:"name"`
}

type CreateTokenDTO struct {
	Name      string     `json:"name"       binding:"required"`
	Expired   *time.Time `json:"expired"`
	ExpiredAt *time.Time `json:"expired_at"`
}

type loginResponse struct {
	Token string `json:"token"`
}

type tokenResponse struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Token   string     `json:"token"`
	Expired *time.Time `json:"expired"`
	Created time.Time  `json:"created"`
}

var (
	errAuthUserNotFound       = errors.New("auth user not found")
	errAuthWrongPassword      = errors.New("auth wrong password")
	errOwnerAlreadyRegistered = errors.New("owner already registered")
)

func firstNonNilTime(values ...*time.Time) *time.Time {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func displayName(name, fallback string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return fallback
}
