package ai

import (
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
