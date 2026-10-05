package markdown

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mx-space/core/internal/models"
)

// articleSnapshot is a unified view of a post, note, or page used for rendering.
type articleSnapshot struct {
	ID        string
	Title     string
	Text      string
	Slug      string
	NID       int
	CreatedAt time.Time
	UpdatedAt time.Time
	Type      string
	Category  *models.CategoryModel
	IsPrivate bool
}

// importDTO is the request body for POST /markdown/import.
type importDTO struct {
	Type string       `json:"type" binding:"required"`
	Data []importItem `json:"data" binding:"required"`
}

type importItem struct {
	Meta *importMeta `json:"meta"`
	Text string      `json:"text" binding:"required"`
}

type importMeta struct {
	Title      string   `json:"title"`
	Date       string   `json:"date"`
	Updated    string   `json:"updated"`
	Categories []string `json:"categories"`
	Tags       []string `json:"tags"`
	Slug       string   `json:"slug"`
}

// UnmarshalJSON accepts front matter as users write it (Hexo etc.): a single category can be a plain
// string, and titles or tags can be numbers.
func (m *importMeta) UnmarshalJSON(data []byte) error {
	var raw struct {
		Title      looseString  `json:"title"`
		Date       looseString  `json:"date"`
		Updated    looseString  `json:"updated"`
		Categories looseStrings `json:"categories"`
		Tags       looseStrings `json:"tags"`
		Slug       looseString  `json:"slug"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = importMeta{
		Title:      string(raw.Title),
		Date:       string(raw.Date),
		Updated:    string(raw.Updated),
		Categories: raw.Categories,
		Tags:       raw.Tags,
		Slug:       string(raw.Slug),
	}
	return nil
}

type looseString string

func (s *looseString) UnmarshalJSON(data []byte) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*s = looseString(scalarToString(v))
	return nil
}

type looseStrings []string

func (s *looseStrings) UnmarshalJSON(data []byte) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	var out []string
	add := func(item interface{}) {
		if str := strings.TrimSpace(scalarToString(item)); str != "" {
			out = append(out, str)
		}
	}
	switch val := v.(type) {
	case []interface{}:
		for _, item := range val {
			// Hexo allows nested category lists ([parent, child]); keep every name.
			if nested, ok := item.([]interface{}); ok {
				for _, n := range nested {
					add(n)
				}
				continue
			}
			add(item)
		}
	default:
		add(val)
	}
	*s = out
	return nil
}

func scalarToString(v interface{}) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(val)
	default:
		return fmt.Sprint(val)
	}
}
