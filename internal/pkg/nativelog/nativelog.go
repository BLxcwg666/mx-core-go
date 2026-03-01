package nativelog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mx-space/core/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	EnvLogDir          = "MX_LOG_DIR"
	EnvLogRotateSizeMB = "MX_LOG_ROTATE_SIZE_MB"
	EnvLogRotateKeep   = "MX_LOG_ROTATE_KEEP"
	defaultSubBufSize  = 128
	defaultLogFilePerm = 0o644
	defaultLogDirPerm  = 0o755
	defaultRotateSize  = 16 * 1024 * 1024
	defaultRotateKeep  = 10
)

var sessionStartedAt = time.Now()

// ResolveDir resolves native log directory path.
func ResolveDir() string {
	if dir := strings.TrimSpace(os.Getenv(EnvLogDir)); dir != "" {
		return config.ResolveRuntimePath(dir, "")
	}
	return config.ResolveRuntimePath("", "logs")
}

// TodayFilename returns daily native log filename.
func TodayFilename(now time.Time) string {
	return "stdout_" + now.Format("1-2-06") + ".log"
}

// TodayFilePath returns today's native log file path.
func TodayFilePath(now time.Time) string {
	return filepath.Join(ResolveDir(), TodayFilename(now))
}

// Writer writes logs into the native daily log file and pushes realtime frames.
type Writer struct {
	mu            sync.Mutex
	dir           string
	rotateMaxSize int64
	rotateKeep    int
}

// NewWriter creates a native log writer.
func NewWriter() (*Writer, error) {
	dir := ResolveDir()
	if err := os.MkdirAll(dir, defaultLogDirPerm); err != nil {
		return nil, err
	}
	_ = os.Setenv(EnvLogDir, dir)

	w := &Writer{
		dir:           dir,
		rotateMaxSize: resolveRotateMaxSize(),
		rotateKeep:    resolveRotateKeep(),
	}
	if err := w.prepareTodayFile(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	path := filepath.Join(w.dir, TodayFilename(time.Now()))
	if err := w.rotateIfNeeded(path, int64(len(p))); err != nil {
		return 0, err
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, defaultLogFilePerm)
	if err != nil {
		return 0, err
	}

	n, writeErr := file.Write(p)
	closeErr := file.Close()

	if n > 0 {
		Publish(string(p[:n]))
	}

	if writeErr != nil {
		return n, writeErr
	}
	if closeErr != nil {
		return n, closeErr
	}
	return n, nil
}

func (w *Writer) Sync() error {
	return nil
}

func (w *Writer) prepareTodayFile() error {
	path := filepath.Join(w.dir, TodayFilename(time.Now()))
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.Size() > 0 {
			if err := w.rotate(path); err != nil {
				return err
			}
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, defaultLogFilePerm)
	if err != nil {
		return err
	}
	return file.Close()
}

func (w *Writer) rotateIfNeeded(path string, incoming int64) error {
	if w.rotateMaxSize <= 0 {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Size()+incoming <= w.rotateMaxSize {
		return nil
	}
	return w.rotate(path)
}

func (w *Writer) rotate(path string) error {
	if w.rotateKeep <= 0 {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, defaultLogFilePerm)
		if err != nil {
			return err
		}
		return file.Close()
	}

	oldest := rotatedPath(path, w.rotateKeep)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	for i := w.rotateKeep - 1; i >= 1; i-- {
		src := rotatedPath(path, i)
		dst := rotatedPath(path, i+1)
		if err := os.Rename(src, dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	if err := os.Rename(path, rotatedPath(path, 1)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

func rotatedPath(path string, index int) string {
	return fmt.Sprintf("%s.%d", path, index)
}

func resolveRotateMaxSize() int64 {
	value := strings.TrimSpace(os.Getenv(EnvLogRotateSizeMB))
	if value == "" {
		return defaultRotateSize
	}

	mb, err := strconv.ParseInt(value, 10, 64)
	if err != nil || mb <= 0 {
		return defaultRotateSize
	}
	return mb * 1024 * 1024
}

func resolveRotateKeep() int {
	value := strings.TrimSpace(os.Getenv(EnvLogRotateKeep))
	if value == "" {
		return defaultRotateKeep
	}

	keep, err := strconv.Atoi(value)
	if err != nil || keep < 0 {
		return defaultRotateKeep
	}
	return keep
}

type streamHub struct {
	mu          sync.RWMutex
	nextID      int
	subscribers map[int]chan string
}

func newStreamHub() *streamHub {
	return &streamHub{
		subscribers: make(map[int]chan string),
	}
}

var globalStreamHub = newStreamHub()

// Subscribe subscribes realtime native log frames.
func Subscribe(buffer int) (int, <-chan string) {
	if buffer <= 0 {
		buffer = defaultSubBufSize
	}
	return globalStreamHub.subscribe(buffer)
}

// Unsubscribe unsubscribes realtime native log frames.
func Unsubscribe(id int) {
	globalStreamHub.unsubscribe(id)
}

// Publish pushes a native log frame to all current subscribers.
func Publish(message string) {
	if message == "" {
		return
	}
	globalStreamHub.publish(message)
}

func (h *streamHub) subscribe(buffer int) (int, <-chan string) {
	ch := make(chan string, buffer)

	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.subscribers[id] = ch
	h.mu.Unlock()

	return id, ch
}

func (h *streamHub) unsubscribe(id int) {
	h.mu.Lock()
	ch, ok := h.subscribers[id]
	if ok {
		delete(h.subscribers, id)
	}
	h.mu.Unlock()

	if ok {
		close(ch)
	}
}

func (h *streamHub) publish(message string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ch := range h.subscribers {
		select {
		case ch <- message:
		default:
		}
	}
}

type sessionFile struct {
	path  string
	index int
}

// SnapshotFilesSinceStartup returns today's native log files that were updated
// after this process started, ordered from oldest to newest.
func SnapshotFilesSinceStartup(now time.Time) ([]string, error) {
	dir := ResolveDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	todayName := TodayFilename(now)
	files := make([]sessionFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		index, ok := parseSessionFileIndex(todayName, entry.Name())
		if !ok {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(sessionStartedAt) {
			continue
		}

		files = append(files, sessionFile{
			path:  filepath.Join(dir, entry.Name()),
			index: index,
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].index > files[j].index
	})

	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.path)
	}
	return paths, nil
}

func parseSessionFileIndex(todayName, fileName string) (int, bool) {
	if fileName == todayName {
		return 0, true
	}

	prefix := todayName + "."
	if !strings.HasPrefix(fileName, prefix) {
		return 0, false
	}

	index, err := strconv.Atoi(strings.TrimPrefix(fileName, prefix))
	if err != nil || index <= 0 {
		return 0, false
	}
	return index, true
}

// NewZapLogger creates a zap logger with native log file output and realtime stream.
func NewZapLogger() (*zap.Logger, error) {
	writer, err := NewWriter()
	if err != nil {
		return nil, err
	}

	level := zap.NewAtomicLevelAt(zap.InfoLevel)
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000")

	encoder := zapcore.NewConsoleEncoder(encoderConfig)
	core := zapcore.NewTee(
		zapcore.NewCore(encoder, zapcore.Lock(os.Stdout), level),
		zapcore.NewCore(encoder, zapcore.AddSync(writer), level),
	)

	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	_ = zap.RedirectStdLog(logger)
	return logger, nil
}
