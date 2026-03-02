package snippet

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mx-space/core/internal/models"
)

func snippetToAggregateDoc(s *models.SnippetModel) map[string]interface{} {
	return map[string]interface{}{
		"_id":       s.ID,
		"id":        s.ID,
		"type":      normalizeSnippetType(s.Type),
		"name":      s.Name,
		"reference": s.Reference,
		"raw":       s.Raw,
		"comment":   s.Comment,
		"private":   s.Private,
		"enable":    s.Enable,
		"schema":    s.Schema,
		"metatype":  s.Metatype,
		"method":    s.Method,
		"built_in":  s.BuiltIn,
		"created":   s.CreatedAt,
		"updated":   s.UpdatedAt,
	}
}

func runAggregatePipeline(docs []map[string]interface{}, pipeline []map[string]interface{}) ([]map[string]interface{}, error) {
	current := make([]map[string]interface{}, len(docs))
	copy(current, docs)

	for _, stage := range pipeline {
		if len(stage) != 1 {
			return nil, fmt.Errorf("invalid aggregate stage")
		}

		for operator, raw := range stage {
			switch operator {
			case "$match":
				next, err := applyMatch(current, raw)
				if err != nil {
					return nil, err
				}
				current = next
			case "$sort":
				if err := applySort(current, raw); err != nil {
					return nil, err
				}
			case "$group":
				next, err := applyGroup(current, raw)
				if err != nil {
					return nil, err
				}
				current = next
			case "$project":
				next, err := applyProject(current, raw)
				if err != nil {
					return nil, err
				}
				current = next
			case "$skip":
				next, err := applySkip(current, raw)
				if err != nil {
					return nil, err
				}
				current = next
			case "$limit":
				next, err := applyLimit(current, raw)
				if err != nil {
					return nil, err
				}
				current = next
			default:
				return nil, fmt.Errorf("unsupported aggregate operator: %s", operator)
			}
		}
	}

	return current, nil
}

func applyMatch(docs []map[string]interface{}, raw interface{}) ([]map[string]interface{}, error) {
	condition, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("$match must be an object")
	}
	out := make([]map[string]interface{}, 0, len(docs))
	for _, doc := range docs {
		if matchesCondition(doc, condition) {
			out = append(out, doc)
		}
	}
	return out, nil
}

func matchesCondition(doc map[string]interface{}, condition map[string]interface{}) bool {
	for key, expected := range condition {
		switch key {
		case "$and":
			items, ok := toSlice(expected)
			if !ok {
				return false
			}
			for _, item := range items {
				nested, ok := item.(map[string]interface{})
				if !ok || !matchesCondition(doc, nested) {
					return false
				}
			}
		case "$or":
			items, ok := toSlice(expected)
			if !ok {
				return false
			}
			any := false
			for _, item := range items {
				nested, ok := item.(map[string]interface{})
				if ok && matchesCondition(doc, nested) {
					any = true
					break
				}
			}
			if !any {
				return false
			}
		default:
			actual := getFieldValue(doc, key)
			if !matchesValue(actual, expected) {
				return false
			}
		}
	}
	return true
}

func matchesValue(actual, expected interface{}) bool {
	expr, ok := expected.(map[string]interface{})
	if !ok {
		return valuesEqual(actual, expected)
	}
	for operator, rhs := range expr {
		switch operator {
		case "$eq":
			if !valuesEqual(actual, rhs) {
				return false
			}
		case "$ne":
			if valuesEqual(actual, rhs) {
				return false
			}
		case "$in":
			candidates, ok := toSlice(rhs)
			if !ok {
				return false
			}
			found := false
			for _, candidate := range candidates {
				if valuesEqual(actual, candidate) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		case "$exists":
			exists, ok := toBool(rhs)
			if !ok {
				return false
			}
			if (actual != nil) != exists {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func applySort(docs []map[string]interface{}, raw interface{}) error {
	spec, ok := raw.(map[string]interface{})
	if !ok {
		return fmt.Errorf("$sort must be an object")
	}
	keys := make([]string, 0, len(spec))
	for key := range spec {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	sort.SliceStable(docs, func(i, j int) bool {
		left := docs[i]
		right := docs[j]
		for _, key := range keys {
			direction, ok := toInt(spec[key])
			if !ok || direction == 0 {
				direction = 1
			}
			compare := compareValues(getFieldValue(left, key), getFieldValue(right, key))
			if compare == 0 {
				continue
			}
			if direction >= 0 {
				return compare < 0
			}
			return compare > 0
		}
		return false
	})

	return nil
}

func applyGroup(docs []map[string]interface{}, raw interface{}) ([]map[string]interface{}, error) {
	spec, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("$group must be an object")
	}
	idExpr, hasID := spec["_id"]
	if !hasID {
		idExpr = nil
	}

	type bucket struct {
		id  interface{}
		doc map[string]interface{}
	}

	order := make([]string, 0)
	grouped := map[string]*bucket{}

	for _, doc := range docs {
		idValue := evalExpression(doc, idExpr)
		groupKey := normalizeGroupKey(idValue)

		entry, exists := grouped[groupKey]
		if !exists {
			entry = &bucket{
				id: idValue,
				doc: map[string]interface{}{
					"_id": idValue,
				},
			}
			grouped[groupKey] = entry
			order = append(order, groupKey)
		}

		for field, expression := range spec {
			if field == "_id" {
				continue
			}
			operation, ok := expression.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("unsupported group expression for %s", field)
			}
			sumExpr, ok := operation["$sum"]
			if !ok {
				return nil, fmt.Errorf("unsupported group accumulator for %s", field)
			}
			current, _ := toFloat(entry.doc[field])
			entry.doc[field] = current + evalSumExpression(doc, sumExpr)
		}
	}

	out := make([]map[string]interface{}, 0, len(order))
	for _, key := range order {
		item := grouped[key].doc
		for field, value := range item {
			number, ok := toFloat(value)
			if !ok {
				continue
			}
			if math.Mod(number, 1) == 0 {
				item[field] = int64(number)
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func applyProject(docs []map[string]interface{}, raw interface{}) ([]map[string]interface{}, error) {
	spec, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("$project must be an object")
	}

	includeMode := false
	for _, value := range spec {
		if isProjectInclude(value) {
			includeMode = true
			break
		}
	}

	out := make([]map[string]interface{}, 0, len(docs))
	for _, doc := range docs {
		if includeMode {
			item := map[string]interface{}{}
			for field, expression := range spec {
				if isProjectExclude(expression) {
					continue
				}
				if isProjectInclude(expression) {
					value := getFieldValue(doc, field)
					if value != nil {
						item[field] = value
					}
					continue
				}
				item[field] = evalExpression(doc, expression)
			}
			out = append(out, item)
			continue
		}

		item := cloneMap(doc)
		for field, expression := range spec {
			if isProjectExclude(expression) {
				delete(item, field)
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func applySkip(docs []map[string]interface{}, raw interface{}) ([]map[string]interface{}, error) {
	n, ok := toInt(raw)
	if !ok {
		return nil, fmt.Errorf("$skip must be a number")
	}
	if n <= 0 {
		return docs, nil
	}
	if n >= len(docs) {
		return []map[string]interface{}{}, nil
	}
	return docs[n:], nil
}

func applyLimit(docs []map[string]interface{}, raw interface{}) ([]map[string]interface{}, error) {
	n, ok := toInt(raw)
	if !ok {
		return nil, fmt.Errorf("$limit must be a number")
	}
	if n <= 0 {
		return []map[string]interface{}{}, nil
	}
	if n >= len(docs) {
		return docs, nil
	}
	return docs[:n], nil
}

func evalExpression(doc map[string]interface{}, expression interface{}) interface{} {
	switch value := expression.(type) {
	case string:
		if strings.HasPrefix(value, "$") {
			return getFieldValue(doc, strings.TrimPrefix(value, "$"))
		}
		return value
	case map[string]interface{}:
		out := make(map[string]interface{}, len(value))
		for key, nested := range value {
			out[key] = evalExpression(doc, nested)
		}
		return out
	default:
		return value
	}
}

func evalSumExpression(doc map[string]interface{}, expression interface{}) float64 {
	switch value := expression.(type) {
	case string:
		if strings.HasPrefix(value, "$") {
			number, ok := toFloat(getFieldValue(doc, strings.TrimPrefix(value, "$")))
			if ok {
				return number
			}
			return 0
		}
		number, ok := toFloat(value)
		if ok {
			return number
		}
		return 0
	default:
		number, ok := toFloat(value)
		if ok {
			return number
		}
		return 0
	}
}

func normalizeGroupKey(value interface{}) string {
	if value == nil {
		return "null"
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func getFieldValue(doc map[string]interface{}, path string) interface{} {
	if path == "" {
		return nil
	}

	current := interface{}(doc)
	for _, part := range strings.Split(path, ".") {
		switch value := current.(type) {
		case map[string]interface{}:
			next, ok := value[part]
			if !ok {
				return nil
			}
			current = next
		default:
			return nil
		}
	}
	return current
}

func compareValues(left, right interface{}) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return -1
	}
	if right == nil {
		return 1
	}

	if l, ok := toFloat(left); ok {
		if r, ok := toFloat(right); ok {
			switch {
			case l < r:
				return -1
			case l > r:
				return 1
			default:
				return 0
			}
		}
	}

	if l, ok := left.(time.Time); ok {
		if r, ok := right.(time.Time); ok {
			switch {
			case l.Before(r):
				return -1
			case l.After(r):
				return 1
			default:
				return 0
			}
		}
	}

	ls := fmt.Sprint(left)
	rs := fmt.Sprint(right)
	switch {
	case ls < rs:
		return -1
	case ls > rs:
		return 1
	default:
		return 0
	}
}

func valuesEqual(left, right interface{}) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	if l, ok := toFloat(left); ok {
		if r, ok := toFloat(right); ok {
			return l == r
		}
	}

	return fmt.Sprint(left) == fmt.Sprint(right)
}

func isProjectInclude(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	case string:
		return strings.HasPrefix(v, "$")
	default:
		return false
	}
}

func isProjectExclude(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return !v
	case float64:
		return v == 0
	case int:
		return v == 0
	case int64:
		return v == 0
	default:
		return false
	}
}

func cloneMap(src map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func toSlice(raw interface{}) ([]interface{}, bool) {
	switch value := raw.(type) {
	case []interface{}:
		return value, true
	default:
		return nil, false
	}
}

func toBool(raw interface{}) (bool, bool) {
	switch value := raw.(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(value)
		return parsed, err == nil
	default:
		return false, false
	}
}

func toInt(raw interface{}) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float32:
		return int(value), true
	case float64:
		return int(value), true
	case json.Number:
		n, err := value.Int64()
		return int(n), err == nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		return n, err == nil
	default:
		return 0, false
	}
}

func toFloat(raw interface{}) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case json.Number:
		f, err := value.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return f, err == nil
	default:
		return 0, false
	}
}
