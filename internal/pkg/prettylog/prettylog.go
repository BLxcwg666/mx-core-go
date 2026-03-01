package prettylog

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

const (
	ansiReset   = "\033[0m"
	ansiBlack   = "\033[30m"
	ansiRed     = "\033[31m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiBlue    = "\033[34m"
	ansiMagenta = "\033[35m"
	ansiCyan    = "\033[36m"
	ansiGray    = "\033[90m"
	ansiBgRed   = "\033[41m"
)

const (
	iconDebug = "⚙"
	iconInfo  = "ℹ"
	iconWarn  = "⚠"
	iconError = "✖"
	iconFatal = "✖"
	iconOK    = "✔"
	iconStart = "◐"
)

// HintKey is a special zap field key used to override the display level style.
const HintKey = "_pl"
const (
	HintSuccess = "success"
	HintReady   = "ready"
	HintStart   = "start"
)

var lastLogTimeMs atomic.Int64

func deltaMs() int64 {
	now := time.Now().UnixMilli()
	prev := lastLogTimeMs.Swap(now)
	if prev == 0 {
		return 0
	}
	return now - prev
}

var bufPool = buffer.NewPool()

// PrettyEncoder formats zap log entries in consola/pretty-logger style.
type PrettyEncoder struct {
	color  bool
	fields []field
}

type field struct {
	key string
	val string
}

// NewEncoder creates a PrettyEncoder. Set color=true for ANSI terminal output.
func NewEncoder(color bool) zapcore.Encoder {
	return &PrettyEncoder{color: color}
}

// ShouldColor returns true when terminal colors should be enabled.
func ShouldColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return true
}

// Clone implements zapcore.Encoder.
func (e *PrettyEncoder) Clone() zapcore.Encoder {
	clone := &PrettyEncoder{
		color:  e.color,
		fields: make([]field, len(e.fields)),
	}
	copy(clone.fields, e.fields)
	return clone
}

// EncodeEntry implements zapcore.Encoder — the main formatting method.
func (e *PrettyEncoder) EncodeEntry(entry zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	buf := bufPool.Get()

	hint := ""
	merged := make([]field, 0, len(e.fields)+len(fields))
	merged = append(merged, e.fields...)
	if len(fields) > 0 {
		tmp := &fieldCollector{}
		for _, f := range fields {
			f.AddTo(tmp)
		}
		for _, kv := range tmp.fields {
			if kv.key == HintKey {
				hint = kv.val
				continue
			}
			merged = append(merged, kv)
		}
	}

	filtered := merged[:0]
	for _, kv := range merged {
		if kv.key == HintKey {
			hint = kv.val
			continue
		}
		filtered = append(filtered, kv)
	}
	merged = filtered

	isBadge := entry.Level >= zapcore.ErrorLevel

	if isBadge {
		buf.AppendByte('\n')
	}

	timeStr := entry.Time.Format("2006-01-02 15:04:05")
	if e.color {
		buf.AppendString(ansiGray)
		buf.AppendString(timeStr)
		buf.AppendString(ansiReset)
	} else {
		buf.AppendString(timeStr)
	}
	buf.AppendByte(' ')

	if isBadge {
		label := " " + strings.ToUpper(entry.Level.String()) + " "
		if e.color {
			buf.AppendString(ansiBgRed)
			buf.AppendString(ansiBlack)
			buf.AppendString(label)
			buf.AppendString(ansiReset)
		} else {
			buf.AppendString(label)
		}
	} else {
		icon, iconColor := resolveIcon(entry.Level, hint)
		if e.color && iconColor != "" {
			buf.AppendString(iconColor)
			buf.AppendString(icon)
			buf.AppendString(ansiReset)
		} else {
			buf.AppendString(icon)
		}
	}
	buf.AppendByte(' ')

	if entry.LoggerName != "" {
		if e.color {
			buf.AppendString(ansiYellow)
			buf.AppendString("[")
			buf.AppendString(entry.LoggerName)
			buf.AppendString("]")
			buf.AppendString(ansiReset)
		} else {
			buf.AppendString("[")
			buf.AppendString(entry.LoggerName)
			buf.AppendString("]")
		}
		buf.AppendByte(' ')
	}

	buf.AppendString(entry.Message)

	for _, kv := range merged {
		buf.AppendByte(' ')
		buf.AppendString(kv.key)
		buf.AppendByte('=')
		if needsQuote(kv.val) {
			buf.AppendString(strconv.Quote(kv.val))
		} else {
			buf.AppendString(kv.val)
		}
	}

	delta := deltaMs()
	if delta > 0 {
		deltaStr := fmt.Sprintf(" +%dms", delta)
		if e.color {
			buf.AppendString(ansiYellow)
			buf.AppendString(deltaStr)
			buf.AppendString(ansiReset)
		} else {
			buf.AppendString(deltaStr)
		}
	}

	if isBadge {
		buf.AppendByte('\n')
	}

	buf.AppendByte('\n')
	return buf, nil
}

func resolveIcon(level zapcore.Level, hint string) (icon string, color string) {
	switch hint {
	case HintSuccess, HintReady:
		return iconOK, ansiGreen
	case HintStart:
		return iconStart, ansiMagenta
	}
	switch level {
	case zapcore.DebugLevel:
		return iconDebug, ansiGray
	case zapcore.InfoLevel:
		return iconInfo, ansiCyan
	case zapcore.WarnLevel:
		return iconWarn, ansiYellow
	case zapcore.ErrorLevel:
		return iconError, ansiRed
	case zapcore.FatalLevel, zapcore.DPanicLevel, zapcore.PanicLevel:
		return iconFatal, ansiRed
	default:
		return iconInfo, ansiCyan
	}
}

func needsQuote(s string) bool {
	if s == "" {
		return true
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == ' ' || r == '"' || r == '=' || r == '\n' || r == '\r' || r == '\t' {
			return true
		}
		i += size
	}
	return false
}

func (e *PrettyEncoder) addField(key, val string) {
	e.fields = append(e.fields, field{key: key, val: val})
}

func (e *PrettyEncoder) AddArray(key string, arr zapcore.ArrayMarshaler) error {
	enc := &fieldCollector{}
	if err := arr.MarshalLogArray(enc); err != nil {
		return err
	}
	parts := make([]string, len(enc.items))
	copy(parts, enc.items)
	e.addField(key, "["+strings.Join(parts, ",")+"]")
	return nil
}

func (e *PrettyEncoder) AddObject(key string, obj zapcore.ObjectMarshaler) error {
	enc := &fieldCollector{}
	if err := obj.MarshalLogObject(enc); err != nil {
		return err
	}
	parts := make([]string, 0, len(enc.fields))
	for _, kv := range enc.fields {
		parts = append(parts, kv.key+"="+kv.val)
	}
	e.addField(key, "{"+strings.Join(parts, " ")+"}")
	return nil
}

func (e *PrettyEncoder) AddBinary(key string, val []byte)          { e.addField(key, fmt.Sprintf("%x", val)) }
func (e *PrettyEncoder) AddByteString(key string, val []byte)      { e.addField(key, string(val)) }
func (e *PrettyEncoder) AddBool(key string, val bool)              { e.addField(key, strconv.FormatBool(val)) }
func (e *PrettyEncoder) AddComplex128(key string, val complex128)  { e.addField(key, fmt.Sprint(val)) }
func (e *PrettyEncoder) AddComplex64(key string, val complex64)    { e.addField(key, fmt.Sprint(val)) }
func (e *PrettyEncoder) AddDuration(key string, val time.Duration) { e.addField(key, val.String()) }
func (e *PrettyEncoder) AddFloat64(key string, val float64) {
	e.addField(key, strconv.FormatFloat(val, 'f', -1, 64))
}
func (e *PrettyEncoder) AddFloat32(key string, val float32) {
	e.addField(key, strconv.FormatFloat(float64(val), 'f', -1, 32))
}
func (e *PrettyEncoder) AddInt(key string, val int)     { e.addField(key, strconv.Itoa(val)) }
func (e *PrettyEncoder) AddInt64(key string, val int64) { e.addField(key, strconv.FormatInt(val, 10)) }
func (e *PrettyEncoder) AddInt32(key string, val int32) {
	e.addField(key, strconv.FormatInt(int64(val), 10))
}
func (e *PrettyEncoder) AddInt16(key string, val int16) {
	e.addField(key, strconv.FormatInt(int64(val), 10))
}
func (e *PrettyEncoder) AddInt8(key string, val int8) {
	e.addField(key, strconv.FormatInt(int64(val), 10))
}
func (e *PrettyEncoder) AddString(key string, val string)  { e.addField(key, val) }
func (e *PrettyEncoder) AddTime(key string, val time.Time) { e.addField(key, val.Format(time.RFC3339)) }
func (e *PrettyEncoder) AddUint(key string, val uint) {
	e.addField(key, strconv.FormatUint(uint64(val), 10))
}
func (e *PrettyEncoder) AddUint64(key string, val uint64) {
	e.addField(key, strconv.FormatUint(val, 10))
}
func (e *PrettyEncoder) AddUint32(key string, val uint32) {
	e.addField(key, strconv.FormatUint(uint64(val), 10))
}
func (e *PrettyEncoder) AddUint16(key string, val uint16) {
	e.addField(key, strconv.FormatUint(uint64(val), 10))
}
func (e *PrettyEncoder) AddUint8(key string, val uint8) {
	e.addField(key, strconv.FormatUint(uint64(val), 10))
}
func (e *PrettyEncoder) AddUintptr(key string, val uintptr) {
	e.addField(key, fmt.Sprintf("0x%x", val))
}
func (e *PrettyEncoder) AddReflected(key string, val interface{}) error {
	e.addField(key, fmt.Sprint(val))
	return nil
}
func (e *PrettyEncoder) OpenNamespace(key string) {
	for i := range e.fields {
		e.fields[i].key = key + "." + e.fields[i].key
	}
}

type fieldCollector struct {
	fields []field
	items  []string
}

func (c *fieldCollector) addField(key, val string) {
	c.fields = append(c.fields, field{key: key, val: val})
}
func (c *fieldCollector) AddArray(key string, arr zapcore.ArrayMarshaler) error {
	c.addField(key, "<array>")
	return nil
}
func (c *fieldCollector) AddObject(key string, obj zapcore.ObjectMarshaler) error {
	c.addField(key, "<object>")
	return nil
}
func (c *fieldCollector) AddBinary(key string, val []byte)          { c.addField(key, fmt.Sprintf("%x", val)) }
func (c *fieldCollector) AddByteString(key string, val []byte)      { c.addField(key, string(val)) }
func (c *fieldCollector) AddBool(key string, val bool)              { c.addField(key, strconv.FormatBool(val)) }
func (c *fieldCollector) AddComplex128(key string, val complex128)  { c.addField(key, fmt.Sprint(val)) }
func (c *fieldCollector) AddComplex64(key string, val complex64)    { c.addField(key, fmt.Sprint(val)) }
func (c *fieldCollector) AddDuration(key string, val time.Duration) { c.addField(key, val.String()) }
func (c *fieldCollector) AddFloat64(key string, val float64) {
	c.addField(key, strconv.FormatFloat(val, 'f', -1, 64))
}
func (c *fieldCollector) AddFloat32(key string, val float32) {
	c.addField(key, strconv.FormatFloat(float64(val), 'f', -1, 32))
}
func (c *fieldCollector) AddInt(key string, val int)     { c.addField(key, strconv.Itoa(val)) }
func (c *fieldCollector) AddInt64(key string, val int64) { c.addField(key, strconv.FormatInt(val, 10)) }
func (c *fieldCollector) AddInt32(key string, val int32) {
	c.addField(key, strconv.FormatInt(int64(val), 10))
}
func (c *fieldCollector) AddInt16(key string, val int16) {
	c.addField(key, strconv.FormatInt(int64(val), 10))
}
func (c *fieldCollector) AddInt8(key string, val int8) {
	c.addField(key, strconv.FormatInt(int64(val), 10))
}
func (c *fieldCollector) AddString(key string, val string) { c.addField(key, val) }
func (c *fieldCollector) AddTime(key string, val time.Time) {
	c.addField(key, val.Format(time.RFC3339))
}
func (c *fieldCollector) AddUint(key string, val uint) {
	c.addField(key, strconv.FormatUint(uint64(val), 10))
}
func (c *fieldCollector) AddUint64(key string, val uint64) {
	c.addField(key, strconv.FormatUint(val, 10))
}
func (c *fieldCollector) AddUint32(key string, val uint32) {
	c.addField(key, strconv.FormatUint(uint64(val), 10))
}
func (c *fieldCollector) AddUint16(key string, val uint16) {
	c.addField(key, strconv.FormatUint(uint64(val), 10))
}
func (c *fieldCollector) AddUint8(key string, val uint8) {
	c.addField(key, strconv.FormatUint(uint64(val), 10))
}
func (c *fieldCollector) AddUintptr(key string, val uintptr) {
	c.addField(key, fmt.Sprintf("0x%x", val))
}
func (c *fieldCollector) AddReflected(key string, val interface{}) error {
	c.addField(key, fmt.Sprint(val))
	return nil
}
func (c *fieldCollector) OpenNamespace(_ string) {}

func (c *fieldCollector) AppendBool(v bool)              { c.items = append(c.items, strconv.FormatBool(v)) }
func (c *fieldCollector) AppendByteString(v []byte)      { c.items = append(c.items, string(v)) }
func (c *fieldCollector) AppendComplex128(v complex128)  { c.items = append(c.items, fmt.Sprint(v)) }
func (c *fieldCollector) AppendComplex64(v complex64)    { c.items = append(c.items, fmt.Sprint(v)) }
func (c *fieldCollector) AppendDuration(v time.Duration) { c.items = append(c.items, v.String()) }
func (c *fieldCollector) AppendFloat64(v float64) {
	c.items = append(c.items, strconv.FormatFloat(v, 'f', -1, 64))
}
func (c *fieldCollector) AppendFloat32(v float32) {
	c.items = append(c.items, strconv.FormatFloat(float64(v), 'f', -1, 32))
}
func (c *fieldCollector) AppendInt(v int)     { c.items = append(c.items, strconv.Itoa(v)) }
func (c *fieldCollector) AppendInt64(v int64) { c.items = append(c.items, strconv.FormatInt(v, 10)) }
func (c *fieldCollector) AppendInt32(v int32) {
	c.items = append(c.items, strconv.FormatInt(int64(v), 10))
}
func (c *fieldCollector) AppendInt16(v int16) {
	c.items = append(c.items, strconv.FormatInt(int64(v), 10))
}
func (c *fieldCollector) AppendInt8(v int8) {
	c.items = append(c.items, strconv.FormatInt(int64(v), 10))
}
func (c *fieldCollector) AppendString(v string)  { c.items = append(c.items, v) }
func (c *fieldCollector) AppendTime(v time.Time) { c.items = append(c.items, v.Format(time.RFC3339)) }
func (c *fieldCollector) AppendUint(v uint) {
	c.items = append(c.items, strconv.FormatUint(uint64(v), 10))
}
func (c *fieldCollector) AppendUint64(v uint64) { c.items = append(c.items, strconv.FormatUint(v, 10)) }
func (c *fieldCollector) AppendUint32(v uint32) {
	c.items = append(c.items, strconv.FormatUint(uint64(v), 10))
}
func (c *fieldCollector) AppendUint16(v uint16) {
	c.items = append(c.items, strconv.FormatUint(uint64(v), 10))
}
func (c *fieldCollector) AppendUint8(v uint8) {
	c.items = append(c.items, strconv.FormatUint(uint64(v), 10))
}
func (c *fieldCollector) AppendUintptr(v uintptr) { c.items = append(c.items, fmt.Sprintf("0x%x", v)) }
func (c *fieldCollector) AppendReflected(v interface{}) error {
	c.items = append(c.items, fmt.Sprint(v))
	return nil
}
func (c *fieldCollector) AppendArray(v zapcore.ArrayMarshaler) error { return v.MarshalLogArray(c) }
func (c *fieldCollector) AppendObject(v zapcore.ObjectMarshaler) error {
	c.items = append(c.items, "<object>")
	return nil
}

// SuccessField returns a zap field that hints the pretty encoder to use the
func SuccessField() zapcore.Field {
	return zapcore.Field{Key: HintKey, Type: zapcore.StringType, String: HintSuccess}
}

// ReadyField returns a zap field that hints the pretty encoder to use the
func ReadyField() zapcore.Field {
	return zapcore.Field{Key: HintKey, Type: zapcore.StringType, String: HintReady}
}

// StartField returns a zap field that hints the pretty encoder to use the
func StartField() zapcore.Field {
	return zapcore.Field{Key: HintKey, Type: zapcore.StringType, String: HintStart}
}

// Colorize wraps text in ANSI color codes. Returns the text unchanged if
func Colorize(color, text string) string {
	if color == "" {
		return text
	}
	return color + text + ansiReset
}

// Yellow wraps text in yellow ANSI color.
func Yellow(text string) string { return Colorize(ansiYellow, text) }

// Green wraps text in green ANSI color.
func Green(text string) string { return Colorize(ansiGreen, text) }

// Blue wraps text in blue ANSI color.
func Blue(text string) string { return Colorize(ansiBlue, text) }

// Red wraps text in red ANSI color.
func Red(text string) string { return Colorize(ansiRed, text) }

// Gray wraps text in gray ANSI color.
func Gray(text string) string { return Colorize(ansiGray, text) }

var processStartOnce sync.Once
var processStartTime time.Time

// MarkProcessStart records the process start time (call once at program start).
func MarkProcessStart() {
	processStartOnce.Do(func() {
		processStartTime = time.Now()
	})
}

// UptimeMs returns milliseconds since MarkProcessStart was called.
func UptimeMs() int64 {
	if processStartTime.IsZero() {
		return 0
	}
	return time.Since(processStartTime).Milliseconds()
}

func init() {
	MarkProcessStart()
}
