package textmacro

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
)

// macroPattern matches [[ ... ]] blocks in text.
var macroPattern = regexp.MustCompile(`\[\[\s*(.*?)\s*\]\]`)

// Service handles text macro expansion for posts, notes, and pages.
type Service struct {
	cfgSvc *appconfigs.Service
}

// NewService creates a new text macro service.
func NewService(cfgSvc *appconfigs.Service) *Service {
	return &Service{cfgSvc: cfgSvc}
}

// Fields stores macro context variables.
type Fields map[string]interface{}

// Process expands all [[ ... ]] macros in text.
// If macros are disabled in TextOptions, it returns text unchanged.
func (s *Service) Process(text string, fields Fields) string {
	cfg, err := s.cfgSvc.Get()
	if err != nil || cfg == nil || !cfg.TextOptions.Macros {
		return text
	}

	return macroPattern.ReplaceAllStringFunc(text, func(match string) string {
		inner := macroPattern.FindStringSubmatch(match)
		if len(inner) < 2 {
			return match
		}
		expr := strings.TrimSpace(inner[1])
		if expr == "" {
			return match
		}

		// $variable — field substitution.
		if strings.HasPrefix(expr, "$") {
			varName := strings.TrimSpace(strings.TrimPrefix(expr, "$"))
			if val, ok := fields[varName]; ok {
				return stringify(val)
			}
			return match
		}

		// ?condition|trueVal|falseVal? / ??condition|trueVal|falseVal??.
		if strings.HasPrefix(expr, "?") && strings.HasSuffix(expr, "?") {
			return processConditional(expr, fields, match)
		}

		// #functionCall() — JavaScript execution.
		if strings.HasPrefix(expr, "#") {
			return processJSCall(strings.TrimPrefix(expr, "#"), fields, match)
		}

		return match
	})
}

func processConditional(expr string, fields Fields, fallback string) string {
	inner := strings.TrimSpace(expr)
	for strings.HasPrefix(inner, "?") {
		inner = strings.TrimPrefix(inner, "?")
	}
	for strings.HasSuffix(inner, "?") {
		inner = strings.TrimSuffix(inner, "?")
	}
	inner = strings.TrimSpace(inner)

	parts := strings.SplitN(inner, "|", 3)
	if len(parts) < 3 {
		return fallback
	}

	condition := normalizeConditionalToken(parts[0])
	trueVal := normalizeConditionalToken(parts[1])
	falseVal := normalizeConditionalToken(parts[2])
	if condition == "" {
		return fallback
	}

	op := ""
	for _, candidate := range []string{"==", "!=", ">", "<", "&&", "||"} {
		if strings.Contains(condition, candidate) {
			op = candidate
			break
		}
	}
	if op == "" {
		return fallback
	}

	left, right, found := strings.Cut(condition, op)
	if !found {
		return fallback
	}
	leftName := strings.TrimPrefix(strings.TrimSpace(left), "$")
	rightValue := strings.TrimSpace(right)
	leftValue, _ := fields[leftName]

	cond := false
	switch op {
	case ">":
		lf, lok := asFloat(leftValue)
		rf, rok := strconv.ParseFloat(rightValue, 64)
		cond = lok && rok == nil && lf > rf
	case "<":
		lf, lok := asFloat(leftValue)
		rf, rok := strconv.ParseFloat(rightValue, 64)
		cond = lok && rok == nil && lf < rf
	case "==":
		cond = looseEqual(leftValue, rightValue)
	case "!=":
		cond = !looseEqual(leftValue, rightValue)
	case "&&":
		cond = truthy(leftValue) && truthy(rightValue)
	case "||":
		cond = truthy(leftValue) || truthy(rightValue)
	}

	if cond {
		return trueVal
	}
	return falseVal
}

func normalizeConditionalToken(s string) string {
	s = strings.ReplaceAll(s, `"`, "")
	s = strings.ReplaceAll(s, `'`, "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

func processJSCall(code string, fields Fields, fallback string) string {
	vm := goja.New()
	isAuthenticated := boolFromAny(fields["_isAuthenticated"]) || boolFromAny(fields["isAuthenticated"])

	for k, v := range fields {
		setField(vm, k, v)
		if !strings.HasPrefix(k, "$") {
			setField(vm, "$"+k, v)
		}
	}

	registerBuiltins(vm, isAuthenticated)

	type result struct {
		val string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := vm.RunString(code)
		if err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{val: v.String()}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			return fallback
		}
		return r.val
	case <-time.After(1 * time.Second):
		vm.Interrupt("macro execution timeout")
		return fallback
	}
}

func setField(vm *goja.Runtime, key string, value interface{}) {
	if strings.TrimSpace(key) == "" {
		return
	}
	_ = vm.Set(key, toJSValue(vm, value))
}

func toJSValue(vm *goja.Runtime, value interface{}) goja.Value {
	switch v := value.(type) {
	case time.Time:
		return jsDateFromTime(vm, v)
	case *time.Time:
		if v == nil {
			return goja.Null()
		}
		return jsDateFromTime(vm, *v)
	default:
		return vm.ToValue(value)
	}
}

func jsDateFromTime(vm *goja.Runtime, t time.Time) goja.Value {
	v, err := vm.RunString(fmt.Sprintf("new Date(%d)", t.UnixMilli()))
	if err != nil {
		return vm.ToValue(t.Format(time.RFC3339))
	}
	return v
}

func registerBuiltins(vm *goja.Runtime, isAuthenticated bool) {
	// dayjs(value) with .format() and .fromNow().
	_ = vm.Set("dayjs", func(call goja.FunctionCall) goja.Value {
		t := time.Now()
		if parsed, ok := parseTimeArgument(call.Argument(0)); ok {
			t = parsed
		}
		obj := vm.NewObject()
		_ = obj.Set("format", func(c goja.FunctionCall) goja.Value {
			layout := c.Argument(0).String()
			return vm.ToValue(formatTimeByDayjsLayout(t, layout))
		})
		_ = obj.Set("fromNow", func(c goja.FunctionCall) goja.Value {
			return vm.ToValue(fromNowString(t, time.Now()))
		})
		return obj
	})

	_ = vm.Set("fromNow", func(call goja.FunctionCall) goja.Value {
		t, ok := parseTimeArgument(call.Argument(0))
		if !ok {
			return vm.ToValue("")
		}
		return vm.ToValue(fromNowString(t, time.Now()))
	})

	_ = vm.Set("onlyMe", func(call goja.FunctionCall) goja.Value {
		text := ""
		if len(call.Arguments) > 0 {
			text = call.Argument(0).String()
		}
		if isAuthenticated {
			return vm.ToValue(text)
		}
		return vm.ToValue("")
	})

	_ = vm.Set("center", func(call goja.FunctionCall) goja.Value {
		text := call.Argument(0).String()
		return vm.ToValue(fmt.Sprintf(`<p align="center">%s</p>`, text))
	})

	_ = vm.Set("right", func(call goja.FunctionCall) goja.Value {
		text := call.Argument(0).String()
		return vm.ToValue(fmt.Sprintf(`<p align="right">%s</p>`, text))
	})

	_ = vm.Set("opacity", func(call goja.FunctionCall) goja.Value {
		text := call.Argument(0).String()
		val := call.Argument(1).String()
		if val == "" || val == "undefined" {
			val = "0.8"
		}
		return vm.ToValue(fmt.Sprintf(`<span style="opacity: %s">%s</span>`, val, text))
	})

	_ = vm.Set("blur", func(call goja.FunctionCall) goja.Value {
		text := call.Argument(0).String()
		radius := call.Argument(1).String()
		if radius == "" || radius == "undefined" {
			radius = "1"
		}
		return vm.ToValue(fmt.Sprintf(`<span style="filter: blur(%spx)">%s</span>`, radius, text))
	})

	_ = vm.Set("color", func(call goja.FunctionCall) goja.Value {
		text := call.Argument(0).String()
		c := call.Argument(1).String()
		if c == "undefined" {
			c = ""
		}
		return vm.ToValue(fmt.Sprintf(`<span style="color: %s">%s</span>`, c, text))
	})

	_ = vm.Set("size", func(call goja.FunctionCall) goja.Value {
		text := call.Argument(0).String()
		sz := call.Argument(1).String()
		if sz == "" || sz == "undefined" {
			sz = "1em"
		}
		return vm.ToValue(fmt.Sprintf(`<span style="font-size: %s">%s</span>`, sz, text))
	})
}

func parseTimeArgument(value goja.Value) (time.Time, bool) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return time.Time{}, false
	}
	return parseTimeValue(value.Export())
}

func parseTimeValue(v interface{}) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case *time.Time:
		if t == nil {
			return time.Time{}, false
		}
		return *t, true
	case int64:
		if t > 1_000_000_000_000 {
			return time.UnixMilli(t), true
		}
		return time.Unix(t, 0), true
	case int:
		tt := int64(t)
		if tt > 1_000_000_000_000 {
			return time.UnixMilli(tt), true
		}
		return time.Unix(tt, 0), true
	case float64:
		n := int64(t)
		if n > 1_000_000_000_000 {
			return time.UnixMilli(n), true
		}
		return time.Unix(n, 0), true
	case string:
		return parseTimeString(t)
	default:
		return parseTimeString(fmt.Sprint(v))
	}
}

func parseTimeString(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02",
		"2006/01/02 15:04:05",
		"2006/01/02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		if ts > 1_000_000_000_000 {
			return time.UnixMilli(ts), true
		}
		return time.Unix(ts, 0), true
	}
	return time.Time{}, false
}

func formatTimeByDayjsLayout(t time.Time, layout string) string {
	layout = strings.TrimSpace(layout)
	if layout == "" || layout == "undefined" {
		layout = "YYYY-MM-DDTHH:mm:ssZ"
	}

	goLayout := layout
	replacements := []struct {
		old string
		new string
	}{
		{"YYYY", "2006"},
		{"YY", "06"},
		{"MM", "01"},
		{"DD", "02"},
		{"HH", "15"},
		{"hh", "03"},
		{"mm", "04"},
		{"ss", "05"},
	}
	for _, repl := range replacements {
		goLayout = strings.ReplaceAll(goLayout, repl.old, repl.new)
	}
	return t.Format(goLayout)
}

func fromNowString(from, to time.Time) string {
	diff := to.Sub(from)
	past := diff >= 0
	if !past {
		diff = -diff
	}

	var text string
	switch {
	case diff < 45*time.Second:
		text = "a few seconds"
	case diff < 90*time.Second:
		text = "a minute"
	case diff < 45*time.Minute:
		text = fmt.Sprintf("%d minutes", int(math.Round(diff.Minutes())))
	case diff < 90*time.Minute:
		text = "an hour"
	case diff < 22*time.Hour:
		text = fmt.Sprintf("%d hours", int(math.Round(diff.Hours())))
	case diff < 36*time.Hour:
		text = "a day"
	case diff < 26*24*time.Hour:
		text = fmt.Sprintf("%d days", int(math.Round(diff.Hours()/24)))
	case diff < 45*24*time.Hour:
		text = "a month"
	case diff < 320*24*time.Hour:
		text = fmt.Sprintf("%d months", int(math.Round(diff.Hours()/(24*30))))
	case diff < 548*24*time.Hour:
		text = "a year"
	default:
		text = fmt.Sprintf("%d years", int(math.Round(diff.Hours()/(24*365))))
	}

	if past {
		return text + " ago"
	}
	return "in " + text
}

func stringify(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	case time.Time:
		return v.Format(time.RFC3339)
	case *time.Time:
		if v == nil {
			return ""
		}
		return v.Format(time.RFC3339)
	default:
		return fmt.Sprint(v)
	}
}

func looseEqual(left interface{}, right string) bool {
	if lf, ok := asFloat(left); ok {
		if rf, err := strconv.ParseFloat(strings.TrimSpace(right), 64); err == nil {
			return lf == rf
		}
	}
	return strings.TrimSpace(strings.ToLower(stringify(left))) == strings.TrimSpace(strings.ToLower(right))
}

func asFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		f, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(v)), 64)
		return f, err == nil
	}
}

func truthy(v interface{}) bool {
	switch n := v.(type) {
	case nil:
		return false
	case bool:
		return n
	case string:
		s := strings.TrimSpace(strings.ToLower(n))
		return s != "" && s != "0" && s != "false"
	default:
		if f, ok := asFloat(v); ok {
			return f != 0
		}
		return strings.TrimSpace(strings.ToLower(fmt.Sprint(v))) != ""
	}
}

func boolFromAny(v interface{}) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		s := strings.TrimSpace(strings.ToLower(b))
		return s == "1" || s == "true" || s == "yes"
	default:
		if f, ok := asFloat(v); ok {
			return f != 0
		}
		return false
	}
}
