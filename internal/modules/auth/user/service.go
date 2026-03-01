package user

import (
	"errors"
	"time"

	"github.com/mx-space/core/internal/models"
	sessionpkg "github.com/mx-space/core/internal/pkg/session"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) GetOwner() (*models.UserModel, error) {
	var u models.UserModel
	if err := s.db.First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Service) GetByID(id string) (*models.UserModel, error) {
	var u models.UserModel
	if err := s.db.First(&u, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Service) Login(username, password, ip, ua string) (string, *models.UserModel, error) {
	var u models.UserModel
	if err := s.db.Select("id, username, name, avatar, password, mail, url, introduce, social_ids, last_login_time, last_login_ip").
		Where("username = ?", username).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			time.Sleep(3 * time.Second)
			return "", nil, errUserNotFound
		}
		return "", nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return "", nil, errWrongPassword
	}
	now := time.Now()
	s.db.Model(&u).Updates(map[string]interface{}{
		"last_login_time": now,
		"last_login_ip":   ip,
	})
	u.LastLoginTime = &now
	u.LastLoginIP = ip

	token, _, err := sessionpkg.Issue(s.db, u.ID, ip, ua, sessionpkg.DefaultTTL)
	return token, &u, err
}

func (s *Service) Register(dto *RegisterDTO) (*models.UserModel, error) {
	var count int64
	s.db.Model(&models.UserModel{}).Count(&count)
	if count > 0 {
		return nil, errOwnerAlreadyExists
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

func (s *Service) IsRegistered() bool {
	var count int64
	s.db.Model(&models.UserModel{}).Count(&count)
	return count > 0
}

func (s *Service) UpdateProfile(id string, dto *UpdateUserDTO) (*models.UserModel, error) {
	u, err := s.GetByID(id)
	if err != nil || u == nil {
		return u, err
	}
	updates := map[string]interface{}{}
	if dto.Name != nil {
		updates["name"] = *dto.Name
		u.Name = *dto.Name
	}
	if dto.Introduce != nil {
		updates["introduce"] = *dto.Introduce
		u.Introduce = *dto.Introduce
	}
	if dto.Avatar != nil {
		updates["avatar"] = *dto.Avatar
		u.Avatar = *dto.Avatar
	}
	if dto.Mail != nil {
		updates["mail"] = *dto.Mail
		u.Mail = *dto.Mail
	}
	if dto.URL != nil {
		updates["url"] = *dto.URL
		u.URL = *dto.URL
	}
	socialIDs := dto.SocialIDs
	if socialIDs == nil {
		socialIDs = dto.SocialIDsAlt
	}
	if socialIDs != nil {
		encoded, err := encodeSocialIDs(*socialIDs)
		if err != nil {
			return nil, err
		}
		updates["social_ids"] = encoded
		u.SocialIDs = encoded
	}
	return u, s.db.Model(u).Updates(updates).Error
}

func (s *Service) ChangePassword(id, oldPwd, newPwd string) error {
	var u models.UserModel
	if err := s.db.Select("id, password").First(&u, "id = ?", id).Error; err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(oldPwd)); err != nil {
		return errWrongPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(newPwd)); err == nil {
		return errPasswordSameAsOld
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPwd), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.db.Model(&u).Update("password", string(hash)).Error
}
