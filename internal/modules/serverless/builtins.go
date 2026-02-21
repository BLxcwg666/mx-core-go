package serverless

import (
	"fmt"
	"strings"

	"github.com/mx-space/core/internal/models"
)

const snippetTypeFunction models.SnippetType = "function"

type builtInSnippet struct {
	Reference string
	Name      string
	Method    string
	Code      string
}

var builtInSnippets = []builtInSnippet{
	{
		Reference: "built-in",
		Name:      "ip",
		Method:    "GET",
		Code: strings.TrimSpace(`
const DEFAULT_TIMEOUT = 5000

function pickString(value) {
  if (Array.isArray(value)) {
    return String(value[0] ?? '').trim()
  }
  if (value === undefined || value === null) {
    return ''
  }
  return String(value).trim()
}

export default async function handler(ctx) {
  const queryIP = pickString(ctx?.query?.ip || ctx?.req?.query?.ip)
  const ip = queryIP || pickString(ctx?.ip || ctx?.req?.ip)

  if (!ip) {
    ctx.throws(422, 'ip is empty')
  }

  const cache = ctx.storage.cache
  const cached = await cache.get(ip)
  if (cached) {
    return cached
  }

  const result = {
    ip,
    countryName: '',
    regionName: '',
    cityName: '',
    ownerDomain: '',
    ispDomain: '',
  }

  const { axios } = await ctx.getService('http')
  const { data } = await axios
    .get('http://ip-api.com/json/' + encodeURIComponent(ip) + '?lang=zh-CN', {
      timeout: DEFAULT_TIMEOUT,
    })
    .catch(() => ({ data: null }))

  if (data && typeof data === 'object') {
    if (data.query) result.ip = data.query
    if (data.country) result.countryName = data.country
    if (data.regionName) result.regionName = data.regionName
    if (data.city) result.cityName = data.city
    if (data.org) result.ownerDomain = data.org
    if (data.isp) result.ispDomain = data.isp
  }

  await cache.set(ip, result)
  return result
}
`),
	},
	{
		Reference: "built-in",
		Name:      "geocode_location",
		Method:    "GET",
		Code: strings.TrimSpace(`
function pickString(value) {
  if (Array.isArray(value)) {
    return String(value[0] ?? '').trim()
  }
  if (value === undefined || value === null) {
    return ''
  }
  return String(value).trim()
}

export default async function handler(ctx) {
  const latitude = pickString(ctx?.query?.latitude)
  const longitude = pickString(ctx?.query?.longitude)

  if (!latitude || !longitude) {
    ctx.throws(400, 'latitude and longitude are required')
  }

  const { axios } = await ctx.getService('http')
  const config = await ctx.getService('config')
  const adminExtra = await config.get('adminExtra')
  const gaodemapKey =
    pickString(adminExtra && (adminExtra.gaodemapKey || adminExtra.gaodeMapKey)) ||
    pickString(ctx?.secret?.gaodemapKey || ctx?.secret?.gaodeMapKey)

  if (!gaodemapKey) {
    ctx.throws(400, 'gaodemap api key is not configured')
  }

  const { data } = await axios
    .get('https://restapi.amap.com/v3/geocode/regeo', {
      params: {
        key: gaodemapKey,
        location: longitude + ',' + latitude,
      },
    })
    .catch(() => ({ data: null }))

  if (!data) {
    ctx.throws(500, 'gaodemap api request failed')
  }

  return data
}
`),
	},
	{
		Reference: "built-in",
		Name:      "geocode_search",
		Method:    "GET",
		Code: strings.TrimSpace(`
function pickString(value) {
  if (Array.isArray(value)) {
    return String(value[0] ?? '').trim()
  }
  if (value === undefined || value === null) {
    return ''
  }
  return String(value).trim()
}

export default async function handler(ctx) {
  const source = pickString(ctx?.query?.keywords)
  const keywords = source.replace(/\s+/g, '|')

  if (!keywords) {
    ctx.throws(400, 'keywords is required')
  }

  const { axios } = await ctx.getService('http')
  const config = await ctx.getService('config')
  const adminExtra = await config.get('adminExtra')
  const gaodemapKey =
    pickString(adminExtra && (adminExtra.gaodemapKey || adminExtra.gaodeMapKey)) ||
    pickString(ctx?.secret?.gaodemapKey || ctx?.secret?.gaodeMapKey)

  if (!gaodemapKey) {
    ctx.throws(422, 'gaodemap api key is not configured')
  }

  const { data } = await axios
    .get('https://restapi.amap.com/v3/place/text', {
      params: {
        key: gaodemapKey,
        keywords,
      },
    })
    .catch(() => ({ data: null }))

  if (!data) {
    ctx.throws(500, 'gaodemap api request failed')
  }

  return data
}
`),
	},
}

type builtInSnippetIdentity struct {
	reference string
	name      string
}

func normalizeBuiltInIdentity(reference, name string) builtInSnippetIdentity {
	return builtInSnippetIdentity{
		reference: strings.ToLower(strings.TrimSpace(reference)),
		name:      strings.TrimSpace(name),
	}
}

func findBuiltInSnippet(reference, name string) *builtInSnippet {
	target := normalizeBuiltInIdentity(reference, name)
	for i := range builtInSnippets {
		snippet := &builtInSnippets[i]
		if normalizeBuiltInIdentity(snippet.Reference, snippet.Name) == target {
			return snippet
		}
	}
	return nil
}

func (h *Handler) ensureBuiltInSnippets() error {
	h.builtInMu.Lock()
	defer h.builtInMu.Unlock()

	if h.builtInReady {
		return nil
	}

	type marker struct{}
	nameSet := map[string]marker{}
	refSet := map[string]marker{}
	pending := map[builtInSnippetIdentity]builtInSnippet{}

	for _, snippet := range builtInSnippets {
		identity := normalizeBuiltInIdentity(snippet.Reference, snippet.Name)
		pending[identity] = snippet
		nameSet[snippet.Name] = marker{}
		refSet[strings.ToLower(strings.TrimSpace(snippet.Reference))] = marker{}
	}

	names := make([]string, 0, len(nameSet))
	for name := range nameSet {
		names = append(names, name)
	}

	references := make([]string, 0, len(refSet))
	for reference := range refSet {
		references = append(references, reference)
	}

	var existing []models.SnippetModel
	err := h.db.
		Where("name IN ?", names).
		Where("LOWER(reference) IN ?", references).
		Where("LOWER(type) = ?", string(snippetTypeFunction)).
		Find(&existing).Error
	if err != nil {
		return err
	}

	for i := range existing {
		current := &existing[i]
		identity := normalizeBuiltInIdentity(current.Reference, current.Name)
		if _, ok := pending[identity]; !ok {
			continue
		}
		delete(pending, identity)

		if current.BuiltIn {
			continue
		}
		if err := h.db.Model(&models.SnippetModel{}).
			Where("id = ?", current.ID).
			Update("built_in", true).Error; err != nil {
			return err
		}
	}

	for _, snippet := range pending {
		record := models.SnippetModel{
			Type:      snippetTypeFunction,
			Name:      snippet.Name,
			Reference: snippet.Reference,
			Raw:       snippet.Code,
			Method:    strings.ToUpper(strings.TrimSpace(snippet.Method)),
			Enable:    true,
			Private:   false,
			BuiltIn:   true,
		}
		if err := h.db.Create(&record).Error; err != nil {
			if isDuplicateSnippetError(err) {
				continue
			}
			return err
		}
	}

	h.builtInReady = true
	return nil
}

func isDuplicateSnippetError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique")
}

func (h *Handler) resetBuiltInSnippet(snippet *models.SnippetModel) error {
	if snippet == nil {
		return fmt.Errorf("snippet is nil")
	}

	preset := findBuiltInSnippet(snippet.Reference, snippet.Name)
	if preset == nil {
		return fmt.Errorf("built-in snippet not found: %s/%s", snippet.Reference, snippet.Name)
	}

	updates := map[string]interface{}{
		"raw":      preset.Code,
		"method":   strings.ToUpper(strings.TrimSpace(preset.Method)),
		"type":     string(snippetTypeFunction),
		"enable":   true,
		"private":  false,
		"built_in": true,
	}
	return h.db.Model(snippet).Updates(updates).Error
}
