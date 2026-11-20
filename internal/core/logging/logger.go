package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kasabot/internal/core/apperr"
	"kasabot/internal/core/config"
	"kasabot/internal/core/reqctx"
)

const (
	appLogPrefix       = "app-"
	errorLogPrefix     = "errors-"
	logFileExt         = ".log"
	logDateLayout      = "2006-01-02"
	errorIDBytes       = 4 // 8 hex-символів
	requestIDBytes     = 8 // 16 hex-символів
	maxRequestIDLength = 64
	logCleanupInterval = 24 * time.Hour
	logDirPerm         = 0o755
	logFilePerm        = 0o644
)

// ErrorEvent передається у зовнішній хук (наприклад, для скорочених сповіщень у Telegram у Плані #004).
type ErrorEvent struct {
	ErrorID     string
	RequestID   string
	ErrorCode   apperr.ErrorCode
	Message     string
	WorkspaceID int64
	UserID      int64
	OccurredAt  time.Time
}

// ErrorHook — колбек, який викликається при фіксації помилки через LogError.
type ErrorHook func(ctx context.Context, event ErrorEvent)

// Logger обгортає стандартний *slog.Logger із записом у щоденні файли та окремий файл помилок.
type Logger struct {
	slog          *slog.Logger
	sink          *dailySink
	location      *time.Location
	now           func() time.Time
	mu            sync.RWMutex
	errorHook     ErrorHook
	stopCleanupCh chan struct{}
	closeOnce     sync.Once
}

// LoggerOption дозволяє налаштувати логер (зокрема підмінити годинник або stdout у тестах).
type LoggerOption func(*loggerOptions)

type loggerOptions struct {
	stdout io.Writer
	now    func() time.Time
}

// WithLoggerClock задає функцію поточного часу (корисно для тестування зміни дня).
func WithLoggerClock(now func() time.Time) LoggerOption {
	return func(o *loggerOptions) {
		if now != nil {
			o.now = now
		}
	}
}

// WithLoggerStdout задає потік для дублювання логів (за замовчуванням os.Stdout).
func WithLoggerStdout(w io.Writer) LoggerOption {
	return func(o *loggerOptions) {
		o.stdout = w
	}
}

// NewLogger створює структурований JSON-логер, який пише події в app-YYYY-MM-DD.log, помилки додатково в errors-YYYY-MM-DD.log та дублює в stdout.
func NewLogger(cfg *config.Config, opts ...LoggerOption) (*Logger, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	loc := cfg.DefaultLocation
	if loc == nil {
		loc = time.UTC
	}

	options := &loggerOptions{
		stdout: os.Stdout,
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(options)
	}

	if err := os.MkdirAll(cfg.LogDir, logDirPerm); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", cfg.LogDir, err)
	}

	sink := &dailySink{
		logDir:        cfg.LogDir,
		retentionDays: cfg.LogRetentionDays,
		location:      loc,
		now:           options.now,
		stdout:        options.stdout,
	}
	if options.stdout != nil {
		sink.baseOutHandler = slog.NewJSONHandler(options.stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}

	// Відкриваємо app-файл за поточний день і виконуємо первинне очищення старих логів
	if err := sink.ensureAppFile(options.now().In(loc).Format(logDateLayout)); err != nil {
		return nil, fmt.Errorf("init log files: %w", err)
	}
	sink.cleanupOldLogs()

	l := &Logger{
		slog:          slog.New(&dailyFileHandler{sink: sink}),
		sink:          sink,
		location:      loc,
		now:           options.now,
		stopCleanupCh: make(chan struct{}),
	}

	if cfg.LogRetentionDays > 0 {
		go l.runDailyCleanup()
	}

	return l, nil
}

// Slog повертає базовий *slog.Logger для використання в ядрі та модулях (DRY).
func (l *Logger) Slog() *slog.Logger {
	return l.slog
}

// SetErrorHook реєструє хук сповіщень про помилки (наприклад, для Telegram у Плані #004).
func (l *Logger) SetErrorHook(hook ErrorHook) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errorHook = hook
}

// Close закриває відкриті файли логів та зупиняє фонову ротацію.
func (l *Logger) Close() error {
	var closeErr error
	l.closeOnce.Do(func() {
		close(l.stopCleanupCh)
		closeErr = l.sink.close()
	})
	return closeErr
}

func (l *Logger) runDailyCleanup() {
	ticker := time.NewTicker(logCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.sink.cleanupOldLogs()
		case <-l.stopCleanupCh:
			return
		}
	}
}

// NewErrorID генерує короткий унікальний ідентифікатор помилки (8 hex-символів).
func NewErrorID() string {
	return randomHex(errorIDBytes)
}

func newRequestID() string {
	return randomHex(requestIDBytes)
}

func randomHex(numBytes int) string {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(buf)
}

// LogError фіксує помилку рівня ERROR із коротким error_id, error_code та наявними в контексті ідентифікаторами і викликає ErrorHook.
func (l *Logger) LogError(ctx context.Context, code apperr.ErrorCode, msg string, attrs ...slog.Attr) string {
	errorID := NewErrorID()
	reqID := reqctx.RequestIDFromCtx(ctx)
	wsID, hasWS := reqctx.WorkspaceIDFromCtx(ctx)
	userID, hasUser := reqctx.UserIDFromCtx(ctx)

	baseAttrs := make([]slog.Attr, 0, 5+len(attrs))
	baseAttrs = append(baseAttrs,
		slog.String("error_id", errorID),
		slog.String("error_code", string(code)),
	)
	if reqID != "" {
		baseAttrs = append(baseAttrs, slog.String("request_id", reqID))
	}
	if hasWS {
		baseAttrs = append(baseAttrs, slog.Int64("workspace_id", wsID))
	}
	if hasUser {
		baseAttrs = append(baseAttrs, slog.Int64("user_id", userID))
	}
	baseAttrs = append(baseAttrs, attrs...)

	l.slog.LogAttrs(ctx, slog.LevelError, msg, baseAttrs...)

	l.mu.RLock()
	hook := l.errorHook
	l.mu.RUnlock()

	if hook != nil {
		hook(ctx, ErrorEvent{
			ErrorID:     errorID,
			RequestID:   reqID,
			ErrorCode:   code,
			Message:     msg,
			WorkspaceID: wsID,
			UserID:      userID,
			OccurredAt:  l.now().In(l.location),
		})
	}

	return errorID
}

// RequestLoggingMiddleware додає request_id у контекст і заголовок X-Request-ID та логує метадані HTTP-запиту без тіла і секретів.
func RequestLoggingMiddleware(logger *Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := sanitizeRequestID(r.Header.Get("X-Request-ID"))
			if reqID == "" {
				reqID = newRequestID()
			}

			ctx := reqctx.WithRequestID(r.Context(), reqID)
			w.Header().Set("X-Request-ID", reqID)

			rw := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
			start := logger.now()

			next.ServeHTTP(rw, r.WithContext(ctx))

			durationMs := logger.now().Sub(start).Milliseconds()

			level := slog.LevelInfo
			if rw.status >= http.StatusInternalServerError {
				level = slog.LevelError
			}

			attrs := make([]slog.Attr, 0, 7)
			attrs = append(attrs,
				slog.String("request_id", reqID),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.status),
				slog.Int64("duration_ms", durationMs),
			)
			if wsID, ok := reqctx.WorkspaceIDFromCtx(ctx); ok {
				attrs = append(attrs, slog.Int64("workspace_id", wsID))
			}
			if userID, ok := reqctx.UserIDFromCtx(ctx); ok {
				attrs = append(attrs, slog.Int64("user_id", userID))
			}

			logger.slog.LogAttrs(ctx, level, "http_request", attrs...)
		})
	}
}

func sanitizeRequestID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > maxRequestIDLength {
		return ""
	}
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		isValid := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
		if !isValid {
			return ""
		}
	}
	return trimmed
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap повертає базовий http.ResponseWriter для підтримки http.ResponseController / Flusher.
func (w *statusResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Flush делегує скидання буфера базовому ResponseWriter, якщо він реалізує http.Flusher.
func (w *statusResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// dailySink володіє спільними файлами логів і поточним днем, якими користуються батьківський та клоновані хендлери.
type dailySink struct {
	mu            sync.Mutex
	logDir        string
	retentionDays int
	location      *time.Location
	now           func() time.Time
	stdout        io.Writer

	currentDay     string
	appFile        *os.File
	errFile        *os.File
	baseAppHandler slog.Handler
	baseErrHandler slog.Handler
	baseOutHandler slog.Handler
}

func (s *dailySink) ensureAppFile(day string) error {
	if s.currentDay == day && s.appFile != nil {
		return nil
	}

	dayChanged := s.currentDay != "" && s.currentDay != day

	if s.appFile != nil {
		_ = s.appFile.Close()
		s.appFile = nil
		s.baseAppHandler = nil
	}
	if s.errFile != nil {
		_ = s.errFile.Close()
		s.errFile = nil
		s.baseErrHandler = nil
	}

	appPath := filepath.Join(s.logDir, appLogPrefix+day+logFileExt)
	appF, err := os.OpenFile(appPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFilePerm)
	if err != nil {
		return err
	}

	s.appFile = appF
	s.currentDay = day
	s.baseAppHandler = slog.NewJSONHandler(appF, &slog.HandlerOptions{Level: slog.LevelDebug})

	if dayChanged {
		s.cleanupOldLogs()
	}
	return nil
}

// ensureErrFile відкриває errors-YYYY-MM-DD.log ліниво лише при першій помилці за добу.
func (s *dailySink) ensureErrFile(day string) error {
	if s.currentDay == day && s.errFile != nil {
		return nil
	}
	errPath := filepath.Join(s.logDir, errorLogPrefix+day+logFileExt)
	errF, err := os.OpenFile(errPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFilePerm)
	if err != nil {
		return err
	}
	s.errFile = errF
	s.baseErrHandler = slog.NewJSONHandler(errF, &slog.HandlerOptions{Level: slog.LevelDebug})
	return nil
}

func (s *dailySink) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	if s.appFile != nil {
		if err := s.appFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.appFile = nil
		s.baseAppHandler = nil
	}
	if s.errFile != nil {
		if err := s.errFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.errFile = nil
		s.baseErrHandler = nil
	}
	return firstErr
}

// cleanupOldLogs видаляє файли app-*.log та errors-*.log, старші за retentionDays (якщо retentionDays > 0).
func (s *dailySink) cleanupOldLogs() {
	if s.retentionDays <= 0 {
		return
	}

	entries, err := os.ReadDir(s.logDir)
	if err != nil {
		return
	}

	todayStr := s.now().In(s.location).Format(logDateLayout)
	today, err := time.ParseInLocation(logDateLayout, todayStr, s.location)
	if err != nil {
		return
	}
	cutoff := today.AddDate(0, 0, -s.retentionDays)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fileDate, ok := parseLogFileDate(entry.Name(), s.location)
		if !ok {
			continue
		}
		if fileDate.Before(cutoff) {
			_ = os.Remove(filepath.Join(s.logDir, entry.Name()))
		}
	}
}

func parseLogFileDate(name string, loc *time.Location) (time.Time, bool) {
	if !strings.HasSuffix(name, logFileExt) {
		return time.Time{}, false
	}
	var datePart string
	switch {
	case strings.HasPrefix(name, appLogPrefix):
		datePart = strings.TrimSuffix(strings.TrimPrefix(name, appLogPrefix), logFileExt)
	case strings.HasPrefix(name, errorLogPrefix):
		datePart = strings.TrimSuffix(strings.TrimPrefix(name, errorLogPrefix), logFileExt)
	default:
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(logDateLayout, datePart, loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

type handlerOp struct {
	group string
	attrs []slog.Attr
}

// dailyFileHandler — легкий декоратор над спільним dailySink, який зберігає лише власну послідовність WithAttrs/WithGroup клона.
type dailyFileHandler struct {
	sink *dailySink
	ops  []handlerOp
}

func (h *dailyFileHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (h *dailyFileHandler) Handle(ctx context.Context, r slog.Record) error {
	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()

	day := r.Time.In(h.sink.location).Format(logDateLayout)
	if err := h.sink.ensureAppFile(day); err != nil {
		if h.sink.baseOutHandler != nil {
			_ = applyOps(h.sink.baseOutHandler, h.ops).Handle(ctx, r)
		}
		return err
	}

	if h.sink.baseOutHandler != nil {
		_ = applyOps(h.sink.baseOutHandler, h.ops).Handle(ctx, r)
	}
	if h.sink.baseAppHandler != nil {
		if err := applyOps(h.sink.baseAppHandler, h.ops).Handle(ctx, r); err != nil {
			return err
		}
	}
	if r.Level >= slog.LevelError {
		if err := h.sink.ensureErrFile(day); err != nil {
			return err
		}
		if h.sink.baseErrHandler != nil {
			if err := applyOps(h.sink.baseErrHandler, h.ops).Handle(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *dailyFileHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	attrsCopy := make([]slog.Attr, len(attrs))
	copy(attrsCopy, attrs)

	newOps := make([]handlerOp, len(h.ops)+1)
	copy(newOps, h.ops)
	newOps[len(h.ops)] = handlerOp{attrs: attrsCopy}

	return &dailyFileHandler{
		sink: h.sink,
		ops:  newOps,
	}
}

func (h *dailyFileHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	newOps := make([]handlerOp, len(h.ops)+1)
	copy(newOps, h.ops)
	newOps[len(h.ops)] = handlerOp{group: name}

	return &dailyFileHandler{
		sink: h.sink,
		ops:  newOps,
	}
}

func applyOps(base slog.Handler, ops []handlerOp) slog.Handler {
	for _, op := range ops {
		if op.group != "" {
			base = base.WithGroup(op.group)
		} else if len(op.attrs) > 0 {
			base = base.WithAttrs(op.attrs)
		}
	}
	return base
}
