package bark

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ConfigFunc is called each time a push is attempted to get the latest Bark settings.
type ConfigFunc func() (key, serverURL, siteTitle string)

// Service sends iOS push notifications via the Bark API.
type Service struct {
	configFn   ConfigFunc
	httpClient *http.Client

	mu         sync.Mutex
	lastPushAt map[string]time.Time
	throttleD  time.Duration
}

// New creates a new Bark service. configFn is called on each push to retrieve settings.
func New(configFn ConfigFunc) *Service {
	return &Service{
		configFn:   configFn,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		lastPushAt: make(map[string]time.Time),
		throttleD:  10 * time.Minute,
	}
}

type pushPayload struct {
	DeviceKey string `json:"device_key"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Category  string `json:"category,omitempty"`
	Group     string `json:"group,omitempty"`
}

// Push sends a Bark notification immediately (no throttle).
func (s *Service) Push(title, body string) error {
	key, serverURL, siteTitle := s.configFn()
	if key == "" {
		return fmt.Errorf("bark key not configured")
	}
	if serverURL == "" {
		serverURL = "https://day.app"
	}

	payload := pushPayload{
		DeviceKey: key,
		Title:     fmt.Sprintf("[%s] %s", siteTitle, title),
		Body:      body,
		Category:  siteTitle,
		Group:     siteTitle,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := s.httpClient.Post(serverURL+"/push", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// ThrottlePush sends a Bark notification for a rate-limit event, but at most once per
func (s *Service) ThrottlePush(ip, path string) {
	key, _, _ := s.configFn()
	if key == "" {
		return
	}

	throttleKey := ip + "|" + path

	s.mu.Lock()
	last, ok := s.lastPushAt[throttleKey]
	if ok && time.Since(last) < s.throttleD {
		s.mu.Unlock()
		return
	}
	s.lastPushAt[throttleKey] = time.Now()
	s.mu.Unlock()

	_ = s.Push("疑似遭到攻击", fmt.Sprintf("IP: %s Path: %s", ip, path))
}
