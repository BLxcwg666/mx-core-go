package comment

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
)

// defaultBlockedKeywords are built-in keywords that always trigger spam detection.
// Ported from mx-core's block-keywords.json.
var defaultBlockedKeywords = []string{
	"测试", "spam", "casino", "porn", "viagra", "cialis",
	"sex", "gambling", "lottery", "poker", "blackjack",
	"药", "代孕", "代开", "发票", "刷单", "兼职",
	"加微信", "加QQ", "加qq", "网赚", "信用卡套现",
	"棋牌", "彩票", "赌博", "博彩", "老虎机",
	"百家乐", "时时彩", "北京赛车", "幸运飞艇",
	"色情", "成人", "约炮", "一夜情",
}

// hasChinese reports whether s contains at least one CJK Unified Ideograph.
func hasChinese(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// checkSpam determines whether a comment should be flagged as spam.
// Returns true if the comment is spam.
func checkSpam(cm *models.CommentModel, opts *config.CommentOptions, masterName string) bool {
	if !opts.AntiSpam {
		return false
	}

	// Master (admin) comments are never spam.
	if strings.TrimSpace(masterName) != "" && strings.EqualFold(cm.Author, masterName) {
		return false
	}

	// Check blocked IPs (supports regex patterns).
	ip := strings.TrimSpace(cm.IP)
	if ip != "" {
		for _, pattern := range opts.BlockIPs {
			pattern = strings.TrimSpace(pattern)
			if pattern == "" {
				continue
			}
			if pattern == ip {
				return true
			}
			if re, err := regexp.Compile(pattern); err == nil {
				if re.MatchString(ip) {
					return true
				}
			}
		}
	}

	// Check text against spam keywords (config + defaults).
	text := cm.Text
	allKeywords := make([]string, 0, len(opts.SpamKeywords)+len(defaultBlockedKeywords))
	allKeywords = append(allKeywords, opts.SpamKeywords...)
	allKeywords = append(allKeywords, defaultBlockedKeywords...)

	for _, kw := range allKeywords {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		// Try exact substring first (fast path).
		if strings.Contains(strings.ToLower(text), strings.ToLower(kw)) {
			return true
		}
		// Try as regex pattern.
		if re, err := regexp.Compile("(?i)" + kw); err == nil {
			if re.MatchString(text) {
				return true
			}
		}
	}

	// Reject comments without Chinese characters if DisableNoChinese is set.
	if opts.DisableNoChinese && !hasChinese(text) {
		return true
	}

	return false
}
