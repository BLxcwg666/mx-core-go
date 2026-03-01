package backup

import (
	"archive/zip"
	"bytes"
	"time"

	"github.com/mx-space/core/internal/modules/system/core/configs"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	"gorm.io/gorm"
)

const backupRootDir = "mx-space-go"
const backupDBDir = backupRootDir + "/db"
const backupManifestFile = backupRootDir + "/manifest.json"
const backupFormat = "mx-core-go-bson"
const backupFormatVersion = 1
const defaultS3PathTemplate = "backups/{Y}/{m}/{filename}"
const EnvBackupDir = "MX_BACKUP_DIR"

var backupTableNames = []string{
	"users",
	"user_sessions",
	"api_tokens",
	"oauth2_tokens",
	"authn_credentials",
	"readers",
	"categories",
	"topics",
	"posts",
	"notes",
	"pages",
	"comments",
	"recentlies",
	"drafts",
	"draft_histories",
	"ai_summaries",
	"ai_deep_readings",
	"analyzes",
	"activities",
	"slug_trackers",
	"file_references",
	"webhooks",
	"webhook_events",
	"snippets",
	"projects",
	"links",
	"says",
	"subscribes",
	"meta_presets",
	"serverless_storages",
	"options",
}

var backupTableNameSet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(backupTableNames))
	for _, table := range backupTableNames {
		set[table] = struct{}{}
	}
	return set
}()

var restoreTableAliases = map[string]string{
	"metapresets":        "meta_presets",
	"sessions":           "user_sessions",
	"serverlessstorages": "serverless_storages",
	"authns":             "authn_credentials",
	"analyze_logs":       "analyzes",
	"recently":           "recentlies",
	"subscribers":        "subscribes",
}

var restoreColumnAliases = map[string]string{
	"_id":           "id",
	"created":       "created_at",
	"modified":      "updated_at",
	"createdat":     "created_at",
	"updatedat":     "updated_at",
	"userid":        "user_id",
	"ipaddress":     "ip",
	"useragent":     "ua",
	"reftype":       "ref_type",
	"refid":         "ref_id",
	"ref":           "ref_id",
	"parent":        "parent_id",
	"targetid":      "target_id",
	"commentsindex": "comments_index",
	"iswhispers":    "is_whispers",
	"parentid":      "parent_id",
	"readerid":      "reader_id",
	"publicat":      "public_at",
	"topicid":       "topic_id",
	"categoryid":    "category_id",
	"pinorder":      "pin_order",
	"readcount":     "read_count",
	"likecount":     "like_count",
	"nid":           "n_id",
}

var restoreColumnAliasesByTable = map[string]map[string]string{
	"notes": {
		"password": "password_hash",
	},
}

var restoreRefTypeAliases = map[string]string{
	"posts":      "post",
	"post":       "post",
	"notes":      "note",
	"note":       "note",
	"pages":      "page",
	"page":       "page",
	"recently":   "recently",
	"recentlies": "recently",
}

var legacyOptionKeyAliases = map[string]string{
	"seo":                          "seo",
	"url":                          "url",
	"mailoptions":                  "mail_options",
	"commentoptions":               "comment_options",
	"backupoptions":                "backup_options",
	"baidusearchoptions":           "baidu_search_options",
	"algoliasearchoptions":         "algolia_search_options",
	"adminextra":                   "admin_extra",
	"friendlinkoptions":            "friend_link_options",
	"s3options":                    "s3_options",
	"imagebedoptions":              "image_bed_options",
	"imagestorageoptions":          "image_storage_options",
	"textoptions":                  "text_options",
	"bingsearchoptions":            "bing_search_options",
	"meilisearchoptions":           "meili_search_options",
	"featurelist":                  "feature_list",
	"barkoptions":                  "bark_options",
	"authsecurity":                 "auth_security",
	"ai":                           "ai",
	"oauth":                        "oauth",
	"thirdpartyserviceintegration": "third_party_service_integration",
}

// Handler is the HTTP handler for backup operations.
type Handler struct {
	db     *gorm.DB
	cfgSvc *configs.Service
	rc     *pkgredis.Client
}

type backupManifest struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	Engine    string    `json:"engine"`
	CreatedAt time.Time `json:"created_at"`
	Tables    []string  `json:"tables"`
}

type backupEntryCandidate struct {
	File   *zip.File
	Format string
}

type tableColumn struct {
	DBType string
}

type backupItem struct {
	Filename string `json:"filename"`
	Size     string `json:"size"`
}

type backupArtifact struct {
	Filename string
	Path     string
	Buffer   *bytes.Buffer
}
