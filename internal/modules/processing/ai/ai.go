package ai

import (
	"fmt"
	"strings"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/taskqueue"
	"gorm.io/gorm"
)

// Service handles AI operations.
type Service struct {
	db      *gorm.DB
	cfgSvc  *configs.Service
	taskSvc *taskqueue.Service
}

func NewService(db *gorm.DB, cfgSvc *configs.Service, taskSvc *taskqueue.Service) *Service {
	return &Service{db: db, cfgSvc: cfgSvc, taskSvc: taskSvc}
}

// ReviewComment runs AI-based spam detection on a comment.
// Returns (isSpam, error). On AI failure it returns (false, err) so the
// comment is not silently blocked.
func ReviewComment(cfg *config.FullConfig, input CommentReviewInput) (bool, error) {
	if cfg == nil || !cfg.CommentOptions.AIReview {
		return false, nil
	}

	provider := selectAIProvider(cfg.AI, cfg.AI.CommentReviewModel)
	if provider == nil || strings.TrimSpace(provider.APIKey) == "" {
		return false, nil
	}

	reviewType := strings.ToLower(strings.TrimSpace(cfg.CommentOptions.AIReviewType))
	if reviewType == "" {
		reviewType = "binary"
	}

	if reviewType == "score" {
		return reviewCommentByScore(provider, input, cfg.CommentOptions.AIReviewThreshold)
	}
	return reviewCommentByBinary(provider, input)
}

func reviewCommentByScore(provider *config.AIProvider, input CommentReviewInput, threshold int) (bool, error) {
	if threshold <= 0 {
		threshold = 5
	}
	systemPrompt, prompt := buildCommentScorePrompt(input)
	raw, err := callAIWithSystemPrompt(provider, systemPrompt, prompt)
	if err != nil {
		return false, fmt.Errorf("ai comment score review: %w", err)
	}
	var output struct {
		Score               float64 `json:"score"`
		HasSensitiveContent bool    `json:"hasSensitiveContent"`
	}
	if err := unmarshalAIJSON(raw, &output); err != nil {
		return false, fmt.Errorf("ai comment score review parse: %w", err)
	}
	score := int(output.Score + 0.5)
	return score > threshold || output.HasSensitiveContent, nil
}

func reviewCommentByBinary(provider *config.AIProvider, input CommentReviewInput) (bool, error) {
	systemPrompt, prompt := buildCommentSpamPrompt(input)
	raw, err := callAIWithSystemPrompt(provider, systemPrompt, prompt)
	if err != nil {
		return false, fmt.Errorf("ai comment binary review: %w", err)
	}
	var output struct {
		IsSpam              bool `json:"isSpam"`
		HasSensitiveContent bool `json:"hasSensitiveContent"`
	}
	if err := unmarshalAIJSON(raw, &output); err != nil {
		return false, fmt.Errorf("ai comment binary review parse: %w", err)
	}
	return output.IsSpam || output.HasSensitiveContent, nil
}
