package models

// SnippetType represents the language/format of a snippet.
type SnippetType string

const (
	SnippetTypeJSON     SnippetType = "json"
	SnippetTypeJSON5    SnippetType = "json5"
	SnippetTypeFunction SnippetType = "function"
	SnippetTypeText     SnippetType = "text"
	SnippetTypeYAML     SnippetType = "yaml"
)

// SnippetModel stores code snippets.
type SnippetModel struct {
	Base
	Type      SnippetType `json:"type"       gorm:"not null"`
	Private   bool        `json:"private"    gorm:"default:false"`
	Raw       string      `json:"raw"        gorm:"type:longtext"`
	Name      string      `json:"name"       gorm:"not null;index"`
	Reference string      `json:"reference"  gorm:"not null;index"`
	Comment   string      `json:"comment"`
	Metatype  string      `json:"metatype"`
	Schema    string      `json:"schema"     gorm:"type:text"`
	Method    string      `json:"method"`
	Secret    string      `json:"-"` // encrypted
	Enable    bool        `json:"enable"     gorm:"default:true"`
	BuiltIn   bool        `json:"built_in"   gorm:"default:false"`
}

func (SnippetModel) TableName() string { return "snippets" }
