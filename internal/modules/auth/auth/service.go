package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/mx-space/core/internal/models"
	sessionpkg "github.com/mx-space/core/internal/pkg/session"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) Login(username, password, ip, ua string) (string, error) {
	var u models.UserModel
	if err := s.db.Select("id, password").
		Where("username = ?", username).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			time.Sleep(3 * time.Second)
			return "", errAuthUserNotFound
		}
		return "", err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		time.Sleep(3 * time.Second)
		return "", errAuthWrongPassword
	}
	token, _, err := sessionpkg.Issue(s.db, u.ID, ip, ua, sessionpkg.DefaultTTL)
	return token, err
}

func (s *Service) Register(dto *RegisterDTO) (*models.UserModel, error) {
	var count int64
	s.db.Model(&models.UserModel{}).Count(&count)
	if count > 0 {
		return nil, errOwnerAlreadyRegistered
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(dto.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	name := dto.Name
	if name == "" {
		name = dto.Username
	}
	u := models.UserModel{Username: dto.Username, Password: string(hash), Name: name}
	return &u, s.db.Create(&u).Error
}

func (s *Service) ListTokens(userID string) ([]models.APIToken, error) {
	var tokens []models.APIToken
	return tokens, s.db.Where("user_id = ? AND (expired_at IS NULL OR expired_at > ?)", userID, time.Now()).
		Order("created_at DESC").Find(&tokens).Error
}

func (s *Service) GetToken(userID, tokenID string) (*models.APIToken, error) {
	var t models.APIToken
	if err := s.db.Where("id = ? AND user_id = ? AND (expired_at IS NULL OR expired_at > ?)", tokenID, userID, time.Now()).
		First(&t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (s *Service) VerifyTokenString(token string) (bool, error) {
	var count int64
	err := s.db.Model(&models.APIToken{}).
		Where("token = ? AND (expired_at IS NULL OR expired_at > ?)", token, time.Now()).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Service) CreateToken(userID string, dto *CreateTokenDTO) (*models.APIToken, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	token := "txo" + hex.EncodeToString(b)

	t := models.APIToken{
		UserID:    userID,
		Token:     token,
		Name:      dto.Name,
		ExpiredAt: firstNonNilTime(dto.Expired, dto.ExpiredAt),
	}
	return &t, s.db.Create(&t).Error
}

func (s *Service) DeleteToken(userID, tokenID string) error {
	result := s.db.Where("id = ? AND user_id = ?", tokenID, userID).
		Delete(&models.APIToken{})
	if result.RowsAffected == 0 {
		return fmt.Errorf("token not found")
	}
	return result.Error
}
