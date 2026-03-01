package configs

import (
	_ "embed"
	"errors"
	"regexp"
	"sync"
)

const configKey = "configs"

var errAIReviewProviderNotEnabled = errors.New("no enabled ai provider for comment ai review")

//go:embed form_schema.template.json
var formSchemaTemplateRaw []byte

//go:embed email-template/owner.template.ejs
var ownerTemplateRaw string

//go:embed email-template/guest.template.ejs
var guestTemplateRaw string

//go:embed email-template/newsletter.template.ejs
var newsletterTemplateRaw string

var providerNameUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var (
	formSchemaLoadOnce sync.Once
	formSchemaTemplate map[string]interface{}
	formSchemaLoadErr  error
)

type providerSelectOption struct {
	Label string
	Value string
}
