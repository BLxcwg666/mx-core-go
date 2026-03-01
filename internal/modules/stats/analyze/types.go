package analyze

import "time"

// analyzeQuery holds optional date-range and path filters for analytics queries.
type analyzeQuery struct {
	StartAt *time.Time `form:"start_at" time_format:"2006-01-02"`
	EndAt   *time.Time `form:"end_at"   time_format:"2006-01-02"`
	From    *time.Time `form:"from"     time_format:"2006-01-02"`
	To      *time.Time `form:"to"       time_format:"2006-01-02"`
	Path    string     `form:"path"`
}

// ipPV bundles unique-IP and page-view counts for a single time bucket.
type ipPV struct {
	IP int64 `json:"ip"`
	PV int64 `json:"pv"`
}

// totalStat aggregates the all-time API call count and unique-visitor count.
type totalStat struct {
	CallTime int64 `json:"call_time"`
	UV       int64 `json:"uv"`
}

// pathCount is a single path aggregation row returned by GROUP BY queries.
type pathCount struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

// analyzeLite is the minimal projection used when computing IP/PV histograms.
type analyzeLite struct {
	IP        string    `gorm:"column:ip"`
	Timestamp time.Time `gorm:"column:timestamp"`
}
