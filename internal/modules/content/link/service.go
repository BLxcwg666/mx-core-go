package link

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) List(q pagination.Query, state *models.LinkState) ([]models.LinkModel, response.Pagination, error) {
	tx := s.db.Model(&models.LinkModel{}).Order("created_at DESC")
	if state != nil {
		tx = tx.Where("state = ?", *state)
	}
	var items []models.LinkModel
	pag, err := pagination.Paginate(tx, q, &items)
	return items, pag, err
}

// ListAllVisible returns all publicly visible links.
func (s *Service) ListAllVisible() ([]models.LinkModel, error) {
	var items []models.LinkModel
	err := s.db.
		Where("state NOT IN ?", []models.LinkState{models.LinkAudit, models.LinkReject}).
		Order("created_at DESC").
		Find(&items).Error
	return items, err
}

func (s *Service) GetByID(id string) (*models.LinkModel, error) {
	var l models.LinkModel
	if err := s.db.First(&l, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// Apply creates a link (Audit state for public, optionally Pass for admin).
func (s *Service) Apply(dto *ApplyLinkDTO, isAdmin bool) (*models.LinkModel, error) {
	var existed models.LinkModel
	err := s.db.Where("url = ? OR name = ?", dto.URL, dto.Name).First(&existed).Error
	if err == nil {
		if isAdmin {
			return nil, errDuplicateLink
		}
		switch existed.State {
		case models.LinkPass, models.LinkAudit:
			return nil, errDuplicateLink
		case models.LinkBanned:
			return nil, errLinkDisabled
		case models.LinkReject, models.LinkOutdate:
			if updateErr := s.db.Model(&existed).Update("state", models.LinkAudit).Error; updateErr != nil {
				return nil, updateErr
			}
			existed.State = models.LinkAudit
			return &existed, nil
		default:
			return nil, errDuplicateLink
		}
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	linkType := models.LinkTypeFriend
	if dto.Type != nil {
		linkType = *dto.Type
	}
	state := models.LinkAudit
	if isAdmin && dto.State != nil {
		state = *dto.State
	} else if isAdmin {
		state = models.LinkPass
	}

	l := models.LinkModel{
		Name: dto.Name, URL: dto.URL, Avatar: dto.Avatar,
		Description: dto.Description, Email: dto.Email,
		Type: linkType, State: state,
	}
	return &l, s.db.Create(&l).Error
}

func (s *Service) Update(id string, dto *UpdateLinkDTO) (*models.LinkModel, error) {
	l, err := s.GetByID(id)
	if err != nil || l == nil {
		return l, err
	}
	updates := map[string]interface{}{}
	if dto.Name != nil {
		updates["name"] = *dto.Name
	}
	if dto.URL != nil {
		updates["url"] = *dto.URL
	}
	if dto.Avatar != nil {
		updates["avatar"] = *dto.Avatar
	}
	if dto.Description != nil {
		updates["description"] = *dto.Description
	}
	if dto.State != nil {
		updates["state"] = *dto.State
	}
	if dto.Type != nil {
		updates["type"] = *dto.Type
	}
	if dto.Email != nil {
		updates["email"] = *dto.Email
	}
	return l, s.db.Model(l).Updates(updates).Error
}

func (s *Service) Delete(id string) error {
	return s.db.Delete(&models.LinkModel{}, "id = ?", id).Error
}

// Approve sets link state to Pass.
func (s *Service) Approve(id string) error {
	return s.db.Model(&models.LinkModel{}).Where("id = ?", id).Update("state", models.LinkPass).Error
}

// StateCount returns counts per state.
func (s *Service) StateCount() map[string]int64 {
	type row struct {
		State models.LinkState
		Count int64
	}
	var rows []row
	s.db.Model(&models.LinkModel{}).Select("state, COUNT(*) as count").Group("state").Scan(&rows)

	counts := map[string]int64{
		"pass": 0, "audit": 0, "outdate": 0, "banned": 0, "reject": 0,
		"friends": 0, "collection": 0,
	}
	stateNames := map[models.LinkState]string{
		models.LinkPass:    "pass",
		models.LinkAudit:   "audit",
		models.LinkOutdate: "outdate",
		models.LinkBanned:  "banned",
		models.LinkReject:  "reject",
	}
	for _, r := range rows {
		if name, ok := stateNames[r.State]; ok {
			counts[name] = r.Count
		}
	}

	var typeCounts []struct {
		Type  models.LinkType
		Count int64
	}
	s.db.Model(&models.LinkModel{}).Where("state = ?", models.LinkPass).
		Select("type, COUNT(*) as count").Group("type").Scan(&typeCounts)
	for _, tc := range typeCounts {
		if tc.Type == models.LinkTypeFriend {
			counts["friends"] = tc.Count
		} else if tc.Type == models.LinkTypeCollection {
			counts["collection"] = tc.Count
		}
	}
	return counts
}

func (s *Service) HealthCheck() map[string]HealthResult {
	var links []models.LinkModel
	s.db.Where("state = ?", models.LinkPass).Find(&links)

	result := make(map[string]HealthResult, len(links))
	client := &http.Client{Timeout: 10 * time.Second}

	for _, l := range links {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.URL, nil)
		cancel()
		if err != nil {
			result[l.ID] = HealthResult{ID: l.ID, Status: 0, Message: err.Error()}
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Mix-Space Friend Link Checker; +https://github.com/BLxcwg666/mx-core-go)")
		resp, err := client.Do(req)
		if err != nil {
			result[l.ID] = HealthResult{ID: l.ID, Status: 0, Message: err.Error()}
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			result[l.ID] = HealthResult{ID: l.ID, Status: resp.StatusCode,
				Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
		} else {
			result[l.ID] = HealthResult{ID: l.ID, Status: resp.StatusCode}
		}
	}
	return result
}
