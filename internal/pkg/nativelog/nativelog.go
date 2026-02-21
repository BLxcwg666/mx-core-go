package nativelog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	EnvLogDir          = "MX_LOG_DIR"
	defaultSubBufSize  = 128
	defaultLogFilePerm = 0o644
	defaultLogDirPerm  = 0o755
)

// ResolveDir resolves native log directory path.
func ResolveDir() string {
	if dir := strings.TrimSpace(os.Getenv(EnvLogDir)); dir != "" {
		return dir
	}

	candidates := make([]string, 0, 4)
	if strings.EqualFold(strings.TrimSpace(os.Getenv("NODE_ENV")), "development") {
		candidates = append(candidates, filepath.Join(".", "tmp", "log"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".mx-space", "log"))
	}
	candidates = append(candidates, filepath.Join(".", "logs"))
	candidates = append(candidates, filepath.Join(".", "tmp", "log"))

	for _, dir := range candidates {
		info, err := os.Stat(dir)
		if err == nil && info.IsDir() {
			return dir
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return filepath.Join(".", "logs")
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
	mu  sync.Mutex
	dir string
}

// NewWriter creates a native log writer.
func NewWriter() (*Writer, error) {
	dir := ResolveDir()
	if err := os.MkdirAll(dir, defaultLogDirPerm); err != nil {
		return nil, err
	}
	_ = os.Setenv(EnvLogDir, dir)
	return &Writer{dir: dir}, nil
}

func (w *Writer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	path := filepath.Join(w.dir, TodayFilename(time.Now()))
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
