package analyze

import (
	"encoding/json"
	"errors"
	"fmt"
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

// Middleware records each non-admin request as an analytics event.
func Middleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next() // handle request first to get status code

		// Track successful public GET requests.
		if c.Request.Method != "GET" {
			return
		}
		rawPath := strings.TrimSpace(c.Request.URL.Path)
		if rawPath != "/api" && !strings.HasPrefix(rawPath, "/api/") {
			return
		}
		path := normalizeAnalyzePath(rawPath)

		// Skip proxy paths
		if strings.HasPrefix(path, "/proxy") {
			return
		}
		if c.Writer.Status() < 200 || c.Writer.Status() >= 300 {
			return
		}

		// Skip bot user-agents
		if isBotUA(c.GetHeader("User-Agent")) {
			return
		}

		// Skip authenticated users (has Authorization header)
		if c.GetHeader("Authorization") != "" {
			return
		}

		ip := strings.TrimSpace(c.ClientIP())
		if ip == "" || ip == "127.0.0.1" || ip == "localhost" || ip == "::1" {
			return
		}

		ua := parseUA(c.GetHeader("User-Agent"))
		referer := c.GetHeader("Referer")

		go func() {
			_ = db.Create(&models.AnalyzeModel{
				IP:        ip,
				UA:        ua,
				Path:      path,
				Referer:   referer,
				Timestamp: time.Now(),
			}).Error
		}()
	}
}

// isBotUA returns true if the User-Agent string indicates a bot/crawler.
func isBotUA(ua string) bool {
	lower := strings.ToLower(ua)
	botKeywords := []string{"bot", "crawler", "spider", "headless", "wget", "curl", "python-requests", "go-http", "java/", "scrapy"}
	for _, kw := range botKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func normalizeAnalyzePath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return "/"
	}

	if p == "/api" {
		return "/"
	}
	if strings.HasPrefix(p, "/api/") {
		p = strings.TrimPrefix(p, "/api")
	}
	if strings.HasPrefix(p, "/v") {
		rest := p[2:]
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			if isDigits(rest[:slash]) {
				p = rest[slash:]
			}
		} else if isDigits(rest) {
			return "/"
		}
	}

	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

func isDigits(raw string) bool {
	if raw == "" {
		return false
	}
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func parseUA(ua string) map[string]interface{} {
	result := map[string]interface{}{
		"ua":      ua,
		"raw":     ua,
		"type":    "desktop",
		"browser": map[string]interface{}{"name": "Unknown"},
		"os":      map[string]interface{}{"name": "Unknown"},
	}
	lower := strings.ToLower(ua)

	switch {
	case strings.Contains(lower, "edg/"):
		result["browser"] = map[string]interface{}{"name": "Edge"}
	case strings.Contains(lower, "chrome/"):
		result["browser"] = map[string]interface{}{"name": "Chrome"}
	case strings.Contains(lower, "safari/") && strings.Contains(lower, "version/"):
		result["browser"] = map[string]interface{}{"name": "Safari"}
	case strings.Contains(lower, "firefox/"):
		result["browser"] = map[string]interface{}{"name": "Firefox"}
	}

	switch {
	case strings.Contains(lower, "windows"):
		result["os"] = map[string]interface{}{"name": "Windows"}
	case strings.Contains(lower, "mac os"):
		result["os"] = map[string]interface{}{"name": "macOS"}
	case strings.Contains(lower, "android"):
		result["os"] = map[string]interface{}{"name": "Android"}
	case strings.Contains(lower, "iphone") || strings.Contains(lower, "ipad") || strings.Contains(lower, "ios"):
		result["os"] = map[string]interface{}{"name": "iOS"}
	case strings.Contains(lower, "linux"):
		result["os"] = map[string]interface{}{"name": "Linux"}
	}

	switch {
	case strings.Contains(lower, "bot") || strings.Contains(lower, "crawler") || strings.Contains(lower, "spider"):
		result["type"] = "bot"
	case strings.Contains(lower, "tablet") || strings.Contains(lower, "ipad"):
		result["type"] = "tablet"
	case strings.Contains(lower, "mobile"):
		result["type"] = "mobile"
	default:
		result["type"] = "desktop"
	}
	return result
}

// Handler exposes analytics to admin.
type Handler struct{ db *gorm.DB }

func NewHandler(db *gorm.DB) *Handler { return &Handler{db: db} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/analyze", authMW)
	g.GET("", h.list)
	g.GET("/today", h.today)
	g.GET("/week", h.week)
	g.GET("/aggregate", h.aggregate)
	g.GET("/total", h.total)
	g.GET("/paths", h.topPaths)
	g.DELETE("", h.cleanOld)
}

type analyzeQuery struct {
	StartAt *time.Time `form:"start_at" time_format:"2006-01-02"`
	EndAt   *time.Time `form:"end_at"   time_format:"2006-01-02"`
	From    *time.Time `form:"from"     time_format:"2006-01-02"`
	To      *time.Time `form:"to"       time_format:"2006-01-02"`
	Path    string     `form:"path"`
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

// cleanOld deletes analytics older than 90 days.
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

type ipPV struct {
	IP int64 `json:"ip"`
	PV int64 `json:"pv"`
}

type totalStat struct {
	CallTime int64 `json:"call_time"`
	UV       int64 `json:"uv"`
}

type pathCount struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

type analyzeLite struct {
	IP        string    `gorm:"column:ip"`
	Timestamp time.Time `gorm:"column:timestamp"`
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

func beginningOfDay(t time.Time) time.Time {
	local := t.In(time.Local)
	y, m, d := local.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

func beginningOfWeek(t time.Time) time.Time {
	dayStart := beginningOfDay(t)
	return dayStart.AddDate(0, 0, -int(dayStart.Weekday()))
}
