package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

// StringArray stores string lists as JSON, while tolerating legacy plain-string data.
type StringArray []string

func (a StringArray) Value() (driver.Value, error) {
	if a == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]string(a))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (a *StringArray) Scan(value interface{}) error {
	if a == nil {
		return fmt.Errorf("models.StringArray: Scan on nil pointer")
	}
	if value == nil {
		*a = []string{}
		return nil
	}

	var raw string
	switch v := value.(type) {
	case []byte:
		raw = string(v)
	case string:
		raw = v
	default:
		return fmt.Errorf("models.StringArray: unsupported Scan type %T", value)
	}

	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		*a = []string{}
		return nil
	}

	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		*a = arr
		return nil
	}

	var single string
	if err := json.Unmarshal([]byte(raw), &single); err == nil {
		if single == "" {
			*a = []string{}
		} else {
			*a = []string{single}
		}
		return nil
	}

	*a = []string{raw}
	return nil
}
