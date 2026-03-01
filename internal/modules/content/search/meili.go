package search

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type meiliClient struct {
	host      string
	apiKey    string
	indexName string
}

type meiliHTTPError struct {
	StatusCode int
	Body       string
}

func (e *meiliHTTPError) Error() string {
	return fmt.Sprintf("meili error %d: %s", e.StatusCode, e.Body)
}

func newMeiliClient(host, apiKey, indexName string) *meiliClient {
	if host == "" {
		host = "http://localhost:7700"
	}
	if indexName == "" {
		indexName = "mx-space"
	}
	return &meiliClient{host: host, apiKey: apiKey, indexName: indexName}
}

func (m *meiliClient) Search(q string) ([]SearchResult, error) {
	body, _ := json.Marshal(map[string]interface{}{"q": q, "limit": 20})
	data, err := m.do("POST", fmt.Sprintf("/indexes/%s/search", url.PathEscape(m.indexName)), body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Hits []map[string]interface{} `json:"hits"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	var results []SearchResult
	for _, hit := range resp.Hits {
		r := SearchResult{}
		if v, _ := hit["id"].(string); v != "" {
			r.ID = v
		}
		if v, _ := hit["title"].(string); v != "" {
			r.Title = v
		}
		if v, _ := hit["summary"].(string); v != "" {
			r.Summary = v
		}
		if v, _ := hit["type"].(string); v != "" {
			r.Type = v
		}
		if v, _ := hit["slug"].(string); v != "" {
			r.Slug = v
		}
		if v, _ := hit["nid"].(float64); v > 0 {
			r.NID = int(v)
		}
		results = append(results, r)
	}
	return results, nil
}

func (m *meiliClient) AddDocuments(docs []map[string]interface{}) error {
	if err := m.ensureIndex(); err != nil {
		return err
	}
	body, _ := json.Marshal(docs)
	_, err := m.do("POST", fmt.Sprintf("/indexes/%s/documents", url.PathEscape(m.indexName)), body)
	if isMeiliIndexNotFoundErr(err) {
		if ensureErr := m.ensureIndex(); ensureErr != nil {
			return ensureErr
		}
		_, err = m.do("POST", fmt.Sprintf("/indexes/%s/documents", url.PathEscape(m.indexName)), body)
	}
	return err
}

func (m *meiliClient) DeleteDocument(id string) error {
	_, err := m.do("DELETE", fmt.Sprintf("/indexes/%s/documents/%s", url.PathEscape(m.indexName), url.PathEscape(id)), nil)
	return err
}

func (m *meiliClient) ensureIndex() error {
	_, err := m.do("GET", fmt.Sprintf("/indexes/%s", url.PathEscape(m.indexName)), nil)
	if err == nil {
		return nil
	}
	if !isMeiliIndexNotFoundErr(err) {
		return err
	}

	body, _ := json.Marshal(map[string]interface{}{
		"uid":        m.indexName,
		"primaryKey": "id",
	})
	_, err = m.do("POST", "/indexes", body)
	if err != nil && !isMeiliIndexAlreadyExistsErr(err) {
		return err
	}

	for i := 0; i < 15; i++ {
		_, getErr := m.do("GET", fmt.Sprintf("/indexes/%s", url.PathEscape(m.indexName)), nil)
		if getErr == nil {
			return nil
		}
		if !isMeiliIndexNotFoundErr(getErr) {
			return getErr
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("meili index %q is not ready", m.indexName)
}

func isMeiliIndexNotFoundErr(err error) bool {
	var me *meiliHTTPError
	if !errors.As(err, &me) {
		return false
	}
	if me.StatusCode != http.StatusNotFound {
		return false
	}
	code := parseMeiliErrorCode(me.Body)
	return code == "" || code == "index_not_found"
}

func isMeiliIndexAlreadyExistsErr(err error) bool {
	var me *meiliHTTPError
	if !errors.As(err, &me) {
		return false
	}
	return parseMeiliErrorCode(me.Body) == "index_already_exists"
}

func parseMeiliErrorCode(body string) string {
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Code)
}

func (m *meiliClient) do(method, path string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, m.host+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, &meiliHTTPError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	return data, nil
}
