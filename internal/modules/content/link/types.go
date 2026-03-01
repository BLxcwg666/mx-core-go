package link

import (
	"errors"
	"time"

	"github.com/mx-space/core/internal/models"
)

type ApplyLinkDTO struct {
	Name        string            `json:"name"        binding:"required"`
	URL         string            `json:"url"         binding:"required,url"`
	Avatar      string            `json:"avatar"`
	Description string            `json:"description"`
	Email       string            `json:"email"`
	Type        *models.LinkType  `json:"type"`
	State       *models.LinkState `json:"state"`
}

type UpdateLinkDTO struct {
	Name        *string           `json:"name"`
	URL         *string           `json:"url"`
	Avatar      *string           `json:"avatar"`
	Description *string           `json:"description"`
	State       *models.LinkState `json:"state"`
	Type        *models.LinkType  `json:"type"`
	Email       *string           `json:"email"`
}

type AuditReasonDTO struct {
	State  models.LinkState `json:"state"  binding:"required"`
	Reason string           `json:"reason"`
}

type linkResponse struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	URL         string           `json:"url"`
	Avatar      string           `json:"avatar"`
	Description string           `json:"description"`
	Type        models.LinkType  `json:"type"`
	State       models.LinkState `json:"state"`
	Email       string           `json:"email,omitempty"`
	Created     time.Time        `json:"created"`
	Modified    time.Time        `json:"modified"`
}

type HealthResult struct {
	ID      string `json:"id"`
	Status  int    `json:"status"`
	Message string `json:"message,omitempty"`
}

type linkAuditData struct {
	Name       string
	URL        string
	StateLabel string
	Reason     string
}

var (
	errDuplicateLink      = errors.New("duplicate link")
	errLinkDisabled       = errors.New("link disabled")
	errSubpathLinkDisable = errors.New("subpath link disabled")
)

var linkAuditTpl = `<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;background:#f5f5f5;padding:20px">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:8px;padding:24px">
  <h2 style="color:#333">友链申请审核结果</h2>
  <p>您的友链申请（<a href="{{.URL}}">{{.Name}}</a>）审核结果如下：</p>
  <p>状态：<strong>{{.StateLabel}}</strong></p>
  {{if .Reason}}<p>原因：{{.Reason}}</p>{{end}}
</div>
</body>
</html>`

func toResponse(l *models.LinkModel, showEmail bool) linkResponse {
	r := linkResponse{
		ID: l.ID, Name: l.Name, URL: l.URL, Avatar: l.Avatar,
		Description: l.Description, Type: l.Type, State: l.State,
		Created: l.CreatedAt, Modified: l.UpdatedAt,
	}
	if showEmail {
		r.Email = l.Email
	}
	return r
}
