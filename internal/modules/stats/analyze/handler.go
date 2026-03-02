package analyze

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

// Handler exposes analytics endpoints to admin users.
type Handler struct{ db *gorm.DB }

func NewHandler(db *gorm.DB) *Handler { return &Handler{db: db} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/analyze", authMW)
	g.GET("", h.list)
	g.GET("/today", h.today)
	g.GET("/week", h.week)
	g.GET("/aggregate", h.aggregate)
	g.GET("/like", h.like)
	g.GET("/traffic-source", h.trafficSource)
	g.GET("/device", h.deviceDistribution)
	g.GET("/total", h.total)
	g.GET("/paths", h.topPaths)
	g.DELETE("", h.cleanOld)
}

func (h *Handler) like(c *gin.Context) {
	type activityPayload struct {
		Payload map[string]interface{} `gorm:"column:payload"`
	}
	var rows []activityPayload
	if err := h.db.Model(&models.ActivityModel{}).
		Select("payload").
		Where("type = ?", "0").
		Find(&rows).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	likeMap := map[string]map[string]struct{}{}
	for _, row := range rows {
		id := stringFromAny(row.Payload["id"])
		if id == "" {
			continue
		}
		ip := stringFromAny(row.Payload["ip"])
		ips, ok := likeMap[id]
		if !ok {
			ips = map[string]struct{}{}
			likeMap[id] = ips
		}
		if ip != "" {
			ips[ip] = struct{}{}
		}
	}

	ids := make([]string, 0, len(likeMap))
	for id := range likeMap {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		ipSet := likeMap[id]
		ips := make([]string, 0, len(ipSet))
		for ip := range ipSet {
			ips = append(ips, ip)
		}
		sort.Strings(ips)
		out = append(out, gin.H{
			"id":  id,
			"ips": ips,
		})
	}

	response.OK(c, out)
}

func (h *Handler) trafficSource(c *gin.Context) {
	var aq analyzeQuery
	if err := c.ShouldBindQuery(&aq); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	from, to := rangeOrDefault(aq, 7*24*time.Hour)

	type refererCount struct {
		Referer string `gorm:"column:referer"`
		Count   int64  `gorm:"column:count"`
	}
	var rows []refererCount
	if err := h.db.Model(&models.AnalyzeModel{}).
		Select("referer, COUNT(*) as count").
		Where("timestamp >= ? AND timestamp <= ?", from, to).
		Group("referer").
		Order("count DESC").
		Find(&rows).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	categories := map[string]int64{
		"direct": 0,
		"search": 0,
		"social": 0,
		"other":  0,
	}
	hostCount := map[string]int64{}

	searchEngines := []string{
		"google",
		"bing",
		"baidu",
		"sogou",
		"so.com",
		"360.cn",
		"yahoo",
		"duckduckgo",
		"yandex",
	}
	socialNetworks := []string{
		"twitter",
		"x.com",
		"facebook",
		"weibo",
		"zhihu",
		"douban",
		"reddit",
		"linkedin",
		"instagram",
		"tiktok",
		"youtube",
		"bilibili",
		"t.me",
		"telegram",
		"discord",
	}

	for _, row := range rows {
		referer := strings.ToLower(strings.TrimSpace(row.Referer))
		if referer == "" {
			categories["direct"] += row.Count
			continue
		}

		parsed, err := url.Parse(referer)
		if err != nil {
			categories["other"] += row.Count
			continue
		}
		host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
		if host == "" {
			categories["other"] += row.Count
			continue
		}

		isSearch := containsAny(host, searchEngines)
		isSocial := containsAny(host, socialNetworks)
		if isSearch {
			categories["search"] += row.Count
		} else if isSocial {
			categories["social"] += row.Count
		} else {
			categories["other"] += row.Count
		}
		hostCount[host] += row.Count
	}

	details := make([]gin.H, 0, len(hostCount))
	for source, count := range hostCount {
		details = append(details, gin.H{
			"source": source,
			"count":  count,
		})
	}
	sort.Slice(details, func(i, j int) bool {
		return details[i]["count"].(int64) > details[j]["count"].(int64)
	})
	if len(details) > 10 {
		details = details[:10]
	}

	categoryList := make([]gin.H, 0, 4)
	categoriesInOrder := []struct {
		key  string
		name string
	}{
		{key: "direct", name: "直接访问"},
		{key: "search", name: "搜索引擎"},
		{key: "social", name: "社交媒体"},
		{key: "other", name: "其他来源"},
	}
	for _, item := range categoriesInOrder {
		value := categories[item.key]
		if value <= 0 {
			continue
		}
		categoryList = append(categoryList, gin.H{
			"name":  item.name,
			"value": value,
		})
	}

	response.OK(c, gin.H{
		"categories": categoryList,
		"details":    details,
	})
}

func (h *Handler) deviceDistribution(c *gin.Context) {
	var aq analyzeQuery
	if err := c.ShouldBindQuery(&aq); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	from, to := rangeOrDefault(aq, 7*24*time.Hour)

	type uaRow struct {
		UA map[string]interface{} `gorm:"column:ua"`
	}
	var rows []uaRow
	if err := h.db.Model(&models.AnalyzeModel{}).
		Select("ua").
		Where("timestamp >= ? AND timestamp <= ?", from, to).
		Find(&rows).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	browserCount := map[string]int64{}
	osCount := map[string]int64{}
	deviceCount := map[string]int64{}
	for _, row := range rows {
		browser := nestedName(row.UA, "browser")
		if browser == "" {
			browser = "Unknown"
		}
		osName := nestedName(row.UA, "os")
		if osName == "" {
			osName = "Unknown"
		}
		device := nestedName(row.UA, "device")
		if device == "" {
			device = stringFromAny(row.UA["type"])
		}
		if device == "" {
			device = "desktop"
		}

		browserCount[browser]++
		osCount[osName]++
		deviceCount[strings.ToLower(device)]++
	}

	deviceNameMap := map[string]string{
		"desktop": "桌面端",
		"mobile":  "移动端",
		"tablet":  "平板",
		"unknown": "未知",
	}

	response.OK(c, gin.H{
		"browsers": toCountList(browserCount, 10, nil),
		"os":       toCountList(osCount, 10, nil),
		"devices": toCountList(deviceCount, 0, func(key string) string {
			if mapped, ok := deviceNameMap[key]; ok {
				return mapped
			}
			if key == "" {
				return "桌面端"
			}
			return key
		}),
	})
}

func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)
	var aq analyzeQuery
	if err := c.ShouldBindQuery(&aq); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	tx := h.db.Model(&models.AnalyzeModel{}).Order("timestamp DESC")
	tx = applyFilter(tx, aq)

	var items []models.AnalyzeModel
	pag, err := pagination.Paginate(tx, q, &items)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Paged(c, items, pag)
}

func (h *Handler) today(c *gin.Context) {
	now := time.Now()
	start := beginningOfDay(now)
	h.listByRange(c, start, now)
}

func (h *Handler) week(c *gin.Context) {
	now := time.Now()
	start := beginningOfWeek(now)
	h.listByRange(c, start, now)
}

func (h *Handler) listByRange(c *gin.Context, from, to time.Time) {
	q := pagination.FromContext(c)
	tx := h.db.Model(&models.AnalyzeModel{}).
		Where("timestamp >= ? AND timestamp <= ?", from, to).
		Order("timestamp DESC")

	var items []models.AnalyzeModel
	pag, err := pagination.Paginate(tx, q, &items)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Paged(c, items, pag)
}

func (h *Handler) total(c *gin.Context) {
	var aq analyzeQuery
	if err := c.ShouldBindQuery(&aq); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	tx := h.db.Model(&models.AnalyzeModel{})
	tx = applyFilter(tx, aq)

	var count int64
	tx.Count(&count)
	response.OK(c, gin.H{"total": count})
}

func (h *Handler) topPaths(c *gin.Context) {
	limit := 20
	var results []pathCount
	h.db.Model(&models.AnalyzeModel{}).
		Select("path, COUNT(*) as count").
		Group("path").
		Order("count DESC").
		Limit(limit).
		Scan(&results)
	response.OK(c, gin.H{"data": results})
}

func (h *Handler) aggregate(c *gin.Context) {
	now := time.Now()
	todayStart := beginningOfDay(now)
	monthStart := todayStart.AddDate(0, 0, -29)
	pathsStart := now.AddDate(0, 0, -7)

	dayAgg, err := h.getIPAndPVByRange(todayStart, now, "hour")
	if err != nil {
		response.InternalError(c, err)
		return
	}
	dateAgg, err := h.getIPAndPVByRange(monthStart, now, "date")
	if err != nil {
		response.InternalError(c, err)
		return
	}

	paths, err := h.getTopPathsByRange(pathsStart, now)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	total, err := h.getTotalStats()
	if err != nil {
		response.InternalError(c, err)
		return
	}

	todayIPs, err := h.getTodayIPs(todayStart, now)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	dayData := make([]gin.H, 0, 24*2)
	for i := 0; i < 24; i++ {
		key := fmt.Sprintf("%02d", i)
		val := dayAgg[key]
		label := key
		dayData = append(dayData,
			gin.H{"hour": label, "key": "ip", "value": val.IP},
			gin.H{"hour": label, "key": "pv", "value": val.PV},
		)
	}

	weekData := make([]gin.H, 0, 7*2)
	for i := 6; i >= 0; i-- {
		day := todayStart.AddDate(0, 0, -i)
		key := day.Format("2006-01-02")
		val := dateAgg[key]
		label := day.Format("Mon")
		weekData = append(weekData,
			gin.H{"day": label, "key": "ip", "value": val.IP},
			gin.H{"day": label, "key": "pv", "value": val.PV},
		)
	}

	monthData := make([]gin.H, 0, 30*2)
	for i := 29; i >= 0; i-- {
		day := todayStart.AddDate(0, 0, -i)
		key := day.Format("2006-01-02")
		val := dateAgg[key]
		label := day.Format("01-02")
		monthData = append(monthData,
			gin.H{"date": label, "key": "ip", "value": val.IP},
			gin.H{"date": label, "key": "pv", "value": val.PV},
		)
	}

	response.OK(c, gin.H{
		"today":     dayData,
		"weeks":     weekData,
		"months":    monthData,
		"paths":     paths,
		"total":     total,
		"today_ips": todayIPs,
		"todayIps":  todayIPs,
	})
}

// cleanOld deletes analytics older than 90 days (or the specified filter range).
func (h *Handler) cleanOld(c *gin.Context) {
	var aq analyzeQuery
	if err := c.ShouldBindQuery(&aq); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	tx := h.db.Model(&models.AnalyzeModel{})
	if aq.From != nil || aq.To != nil || aq.StartAt != nil || aq.EndAt != nil {
		tx = applyFilter(tx, aq)
	} else {
		cutoff := time.Now().AddDate(0, 0, -90)
		tx = tx.Where("timestamp < ?", cutoff)
	}
	result := tx.Delete(&models.AnalyzeModel{})
	response.OK(c, gin.H{"deleted": result.RowsAffected})
}

// applyFilter adds optional date-range and path WHERE clauses to tx.
func applyFilter(tx *gorm.DB, aq analyzeQuery) *gorm.DB {
	start := aq.StartAt
	if start == nil {
		start = aq.From
	}
	end := aq.EndAt
	if end == nil {
		end = aq.To
	}

	if start != nil {
		tx = tx.Where("timestamp >= ?", *start)
	}
	if end != nil {
		tx = tx.Where("timestamp <= ?", *end)
	}
	if aq.Path != "" {
		tx = tx.Where("path = ?", aq.Path)
	}
	return tx
}

func (h *Handler) getIPAndPVByRange(from, to time.Time, granularity string) (map[string]ipPV, error) {
	var rows []analyzeLite
	if err := h.db.Model(&models.AnalyzeModel{}).
		Select("ip, timestamp").
		Where("timestamp >= ? AND timestamp <= ?", from, to).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	type counter struct {
		pv  int64
		ips map[string]struct{}
	}
	counts := map[string]*counter{}
	for _, row := range rows {
		ts := row.Timestamp.In(time.Local)
		var key string
		switch granularity {
		case "hour":
			key = ts.Format("15")
		case "date":
			key = ts.Format("2006-01-02")
		default:
			key = ts.Format(time.RFC3339)
		}

		c, ok := counts[key]
		if !ok {
			c = &counter{ips: map[string]struct{}{}}
			counts[key] = c
		}
		c.pv++
		if row.IP != "" {
			c.ips[row.IP] = struct{}{}
		}
	}

	out := make(map[string]ipPV, len(counts))
	for key, val := range counts {
		out[key] = ipPV{IP: int64(len(val.ips)), PV: val.pv}
	}
	return out, nil
}

func (h *Handler) getTopPathsByRange(from, to time.Time) ([]pathCount, error) {
	var paths []pathCount
	err := h.db.Model(&models.AnalyzeModel{}).
		Select("path, COUNT(*) as count").
		Where("timestamp >= ? AND timestamp <= ?", from, to).
		Where("path <> ''").
		Group("path").
		Order("count DESC").
		Limit(50).
		Scan(&paths).Error
	return paths, err
}

func (h *Handler) getTodayIPs(from, to time.Time) ([]string, error) {
	var ips []string
	if err := h.db.Model(&models.AnalyzeModel{}).
		Distinct("ip").
		Where("timestamp >= ? AND timestamp <= ?", from, to).
		Where("ip <> ''").
		Pluck("ip", &ips).Error; err != nil {
		return nil, err
	}
	sort.Strings(ips)
	return ips, nil
}

func (h *Handler) getTotalStats() (totalStat, error) {
	callTime, hasCallTime, err := h.getOptionInt("apiCallTime")
	if err != nil {
		return totalStat{}, err
	}
	uv, hasUV, err := h.getOptionInt("uv")
	if err != nil {
		return totalStat{}, err
	}

	if !hasCallTime {
		if err := h.db.Model(&models.AnalyzeModel{}).Count(&callTime).Error; err != nil {
			return totalStat{}, err
		}
	}
	if !hasUV {
		if err := h.db.Model(&models.AnalyzeModel{}).Distinct("ip").Count(&uv).Error; err != nil {
			return totalStat{}, err
		}
	}

	return totalStat{CallTime: callTime, UV: uv}, nil
}

func (h *Handler) getOptionInt(name string) (value int64, found bool, err error) {
	var opt models.OptionModel
	if err := h.db.Where("name = ?", name).First(&opt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}

	raw := strings.TrimSpace(opt.Value)
	if raw == "" {
		return 0, true, nil
	}

	trimmed := strings.Trim(raw, "\"")
	if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return parsed, true, nil
	}

	var number float64
	if err := json.Unmarshal([]byte(raw), &number); err == nil {
		return int64(number), true, nil
	}
	return 0, true, nil
}

func rangeOrDefault(aq analyzeQuery, fallbackWindow time.Duration) (time.Time, time.Time) {
	start := aq.StartAt
	if start == nil {
		start = aq.From
	}
	end := aq.EndAt
	if end == nil {
		end = aq.To
	}

	to := time.Now()
	if end != nil {
		to = *end
	}
	from := to.Add(-fallbackWindow)
	if start != nil {
		from = *start
	}
	return from, to
}

func containsAny(host string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.Contains(host, pattern) {
			return true
		}
	}
	return false
}

func nestedName(raw map[string]interface{}, key string) string {
	nested, ok := raw[key]
	if !ok {
		return ""
	}
	switch value := nested.(type) {
	case map[string]interface{}:
		return stringFromAny(value["name"])
	case gin.H:
		return stringFromAny(value["name"])
	default:
		return ""
	}
}

func toCountList(counts map[string]int64, limit int, rename func(key string) string) []gin.H {
	list := make([]gin.H, 0, len(counts))
	for key, value := range counts {
		name := key
		if rename != nil {
			name = rename(key)
		}
		list = append(list, gin.H{
			"name":  name,
			"value": value,
		})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i]["value"].(int64) > list[j]["value"].(int64)
	})
	if limit > 0 && len(list) > limit {
		return list[:limit]
	}
	return list
}

func stringFromAny(raw interface{}) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10)
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return ""
	}
}

func beginningOfDay(t time.Time) time.Time {
	local := t.In(time.Local)
	y, m, d := local.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

func beginningOfWeek(t time.Time) time.Time {
	dayStart := beginningOfDay(t)
	return dayStart.AddDate(0, 0, -int(dayStart.Weekday()))
}
