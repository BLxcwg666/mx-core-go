package comment

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mx-space/core/internal/models"
	"go.uber.org/zap"
)

var ipLocationClient = &http.Client{Timeout: 5 * time.Second}

// appendIPLocation fills comment.location when commentOptions.recordIpLocation is on.
// It uses the same source as the built-in "ip" function and the original core's location format.
func (h *Handler) appendIPLocation(commentID, ip string) {
	ip = strings.TrimSpace(ip)
	if commentID == "" || ip == "" || h.cfgSvc == nil {
		return
	}
	if parsed := net.ParseIP(ip); parsed == nil || parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsUnspecified() {
		return
	}
	cfg, err := h.cfgSvc.Get()
	if err != nil || cfg == nil || !cfg.CommentOptions.RecordIPLocation {
		return
	}
	location, err := lookupIPLocation(ip)
	if err != nil {
		h.logger.Warn("lookup comment ip location failed", zap.String("ip", ip), zap.Error(err))
		return
	}
	if location == "" {
		return
	}
	if err := h.svc.db.Model(&models.CommentModel{}).Where("id = ?", commentID).Update("location", location).Error; err != nil {
		h.logger.Warn("save comment ip location failed", zap.String("id", commentID), zap.Error(err))
	}
}

func lookupIPLocation(ip string) (string, error) {
	resp, err := ipLocationClient.Get("http://ip-api.com/json/" + url.PathEscape(ip) + "?lang=zh-CN")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var data struct {
		Status     string `json:"status"`
		Country    string `json:"country"`
		RegionName string `json:"regionName"`
		City       string `json:"city"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if data.Status != "" && data.Status != "success" {
		return "", nil
	}
	location := data.Country
	if data.RegionName != "" && data.RegionName != data.City {
		location += data.RegionName
	}
	location += data.City
	return strings.TrimSpace(location), nil
}
