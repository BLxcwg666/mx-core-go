package link

import (
	"errors"
	"time"

	"github.com/mx-space/core/internal/models"
)

type ApplyLinkDTO struct {
	Name        string            `json:"name"        binding:"required"`
	Author      string            `json:"author"`
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
	Modified    *time.Time       `json:"modified"`
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

type linkPassData struct {
	Name        string
	URL         string
	Description string
}

type linkApplyData struct {
	AuthorName  string
	Name        string
	URL         string
	Description string
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

var linkPassTpl = `<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;background:#f5f5f5;padding:20px">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:8px;padding:24px">
  <h2 style="color:#333">你的友链已通过审核</h2>
  <p>您提交的友链 <a href="{{.URL}}">{{.Name}}</a> 已通过审核，欢迎交换友链！</p>
  {{if .Description}}<p>站点描述：{{.Description}}</p>{{end}}
</div>
</body>
</html>`

var linkApplyTpl = `<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;background:#f5f5f5;padding:20px">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:8px;padding:24px">
  <h2 style="color:#333">收到新的友链申请</h2>
  {{if .AuthorName}}<p>来自 {{.AuthorName}} 的友链请求：</p>{{end}}
  <p>站点标题：{{.Name}}</p>
  <p>站点网站：<a href="{{.URL}}">{{.URL}}</a></p>
  {{if .Description}}<p>站点描述：{{.Description}}</p>{{end}}
</div>
</body>
</html>`

func toResponse(l *models.LinkModel, showEmail bool) linkResponse {
	var modified *time.Time
	if !l.UpdatedAt.IsZero() && l.UpdatedAt.Year() > 1 {
		m := l.UpdatedAt
		modified = &m
	}
	r := linkResponse{
		ID: l.ID, Name: l.Name, URL: l.URL, Avatar: l.Avatar,
		Description: l.Description, Type: l.Type, State: l.State,
		Created: l.CreatedAt, Modified: modified,
	}
	if showEmail {
		r.Email = l.Email
	}
	return r
}
