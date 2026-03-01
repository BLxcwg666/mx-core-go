package user

import (
	"errors"
	"time"
)

type UpdateUserDTO struct {
	Name         *string                 `json:"name"`
	Introduce    *string                 `json:"introduce"`
	Avatar       *string                 `json:"avatar"`
	Mail         *string                 `json:"mail"`
	URL          *string                 `json:"url"`
	SocialIDs    *map[string]interface{} `json:"social_ids"`
	SocialIDsAlt *map[string]interface{} `json:"socialIds"`
}

type ChangePasswordDTO struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

type LoginDTO struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type RegisterDTO struct {
	Username string `json:"username" binding:"required,min=3"`
	Password string `json:"password" binding:"required,min=6"`
	Name     string `json:"name"`
}

type userResponse struct {
	ID            string                 `json:"id"`
	Username      string                 `json:"username"`
	Name          string                 `json:"name"`
	Introduce     string                 `json:"introduce"`
	Avatar        string                 `json:"avatar"`
	Mail          string                 `json:"mail"`
	URL           string                 `json:"url"`
	SocialIDs     map[string]interface{} `json:"social_ids"`
	LastLoginTime *time.Time             `json:"last_login_time"`
	LastLoginIP   string                 `json:"last_login_ip"`
}

type publicUserResponse struct {
	ID        string                 `json:"id"`
	Username  string                 `json:"username"`
	Name      string                 `json:"name"`
	Introduce string                 `json:"introduce"`
	Avatar    string                 `json:"avatar"`
	Mail      string                 `json:"mail"`
	URL       string                 `json:"url"`
	SocialIDs map[string]interface{} `json:"social_ids"`
}

type loginResponse struct {
	Token string        `json:"token"`
	User  *userResponse `json:"user,omitempty"`
}

var (
	errUserNotFound       = errors.New("user not found")
	errWrongPassword      = errors.New("wrong password")
	errOwnerAlreadyExists = errors.New("owner already registered")
	errPasswordSameAsOld  = errors.New("password same as old")
)
