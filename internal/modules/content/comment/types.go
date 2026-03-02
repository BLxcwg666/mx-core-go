package comment

import (
	"errors"
	"time"

	"github.com/mx-space/core/internal/models"
)

const nestedReplyMax = 10

var (
	errCommentParentNotFound = errors.New("parent comment not found")
	errCommentRefNotFound    = errors.New("comment ref model not found")
	errCommentTooDeep        = errors.New("comment nested depth too deep")
)

type CreateCommentDTO struct {
	RefType  models.RefType         `json:"ref_type"`
	RefID    string                 `json:"ref_id"`
	Author   string                 `json:"author"    binding:"required"`
	Mail     string                 `json:"mail"`
	URL      string                 `json:"url"`
	Text     string                 `json:"text"      binding:"required"`
	ParentID *string                `json:"parent_id"`
	Meta     map[string]interface{} `json:"meta"`
}

type UpdateCommentStateDTO struct {
	State models.CommentState `json:"state" binding:"required"`
}

type ReplyCommentDTO struct {
	Author string                 `json:"author"`
	Mail   string                 `json:"mail"`
	URL    string                 `json:"url"`
	Text   string                 `json:"text"   binding:"required"`
	Meta   map[string]interface{} `json:"meta"`
}

type commentResponse struct {
	ID            string                 `json:"id"`
	RefType       string                 `json:"ref_type"`
	RefID         string                 `json:"ref_id"`
	Author        string                 `json:"author"`
	Mail          string                 `json:"mail,omitempty"`
	URL           string                 `json:"url"`
	Text          string                 `json:"text"`
	State         models.CommentState    `json:"state"`
	ParentID      *string                `json:"parent_id"`
	Parent        interface{}            `json:"parent,omitempty"`
	Children      []commentResponse      `json:"children"`
	CommentsIndex int                    `json:"comments_index"`
	Key           string                 `json:"key"`
	IP            string                 `json:"ip,omitempty"`
	Agent         string                 `json:"agent,omitempty"`
	Pin           bool                   `json:"pin"`
	IsWhispers    bool                   `json:"is_whispers"`
	Avatar        string                 `json:"avatar"`
	Location      string                 `json:"location"`
	Meta          map[string]interface{} `json:"meta,omitempty"`
	ReaderID      *string                `json:"reader_id,omitempty"`
	Source        string                 `json:"source,omitempty"`
	Ref           interface{}            `json:"ref,omitempty"`
	EditedAt      *time.Time             `json:"edited_at"`
	Created       time.Time              `json:"created"`
	Modified      time.Time              `json:"modified"`
}

type commentParentResponse struct {
	ID      string    `json:"id"`
	Author  string    `json:"author"`
	Text    string    `json:"text"`
	Created time.Time `json:"created"`
}

func toResponse(c *models.CommentModel, isAdmin bool) commentResponse {
	children := make([]commentResponse, len(c.Children))
	for i, ch := range c.Children {
		children[i] = toResponse(&ch, isAdmin)
	}
	r := commentResponse{
		ID: c.ID, RefType: refTypeForResponse(c.RefType), RefID: c.RefID,
		Author: c.Author, URL: c.URL, Text: c.Text,
		State: c.State, ParentID: c.ParentID, Children: children,
		CommentsIndex: c.CommentsIndex, Key: c.Key,
		Pin: c.Pin, IsWhispers: c.IsWhispers, Avatar: c.Avatar,
		Meta: c.Meta, ReaderID: c.ReaderID, Source: c.Source,
		Location: c.Location, EditedAt: c.EditedAt,
		Created: c.CreatedAt, Modified: c.UpdatedAt,
	}
	if isAdmin {
		r.IP = c.IP
		r.Mail = c.Mail
		r.Agent = c.Agent
	}
	return r
}
