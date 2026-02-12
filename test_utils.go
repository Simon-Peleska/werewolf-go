package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// AppLogger provides logging utilities for the application
// Used by both the server and tests
type AppLogger struct {
	outputDir      string
	logRequests    bool
	logHTML        bool
	logDB          bool
	logWS          bool
	debug          bool
	requestLog     *os.File
	htmlLog        *os.File
	dbLog          *os.File
	wsLog          *os.File
	mu             sync.Mutex
	requestCount   int
	htmlCount      int
	wsMessageCount int
}

// Global application logger (used by server)
var appLogger *AppLogger

// LogConfig holds logging configuration
type LogConfig struct {
	OutputDir   string
	LogRequests bool
	LogHTML     bool
	LogDB       bool
	LogWS       bool
	Debug       bool
}

// NewAppLogger creates a new application logger
func NewAppLogger(config LogConfig) (*AppLogger, error) {
	al := &AppLogger{
		outputDir:   config.OutputDir,
		logRequests: config.LogRequests,
		logHTML:     config.LogHTML,
		logDB:       config.LogDB,
		logWS:       config.LogWS,
		debug:       config.Debug,
	}

	if al.outputDir == "" {
		return al, nil // No file logging, just in-memory state
	}

	// Open log files
	var err error
	if al.logRequests {
		path := fmt.Sprintf("%s/requests.log", al.outputDir)
		al.requestLog, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open request log: %w", err)
		}
	}
	if al.logHTML {
		path := fmt.Sprintf("%s/html_states.log", al.outputDir)
		al.htmlLog, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open HTML log: %w", err)
		}
	}
	if al.logDB {
		path := fmt.Sprintf("%s/database.log", al.outputDir)
		al.dbLog, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open database log: %w", err)
		}
	}
	if al.logWS {
		path := fmt.Sprintf("%s/websocket.log", al.outputDir)
		al.wsLog, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open WebSocket log: %w", err)
		}
	}

	return al, nil
}

// NewAppLoggerFromEnv creates a logger from environment variables
// Checks both LOG_* (server) and TEST_LOG_* (test) variants
func NewAppLoggerFromEnv() (*AppLogger, error) {
	// Helper to check both variants
	envBool := func(serverVar, testVar string) bool {
		return os.Getenv(serverVar) == "1" || os.Getenv(testVar) == "1"
	}
	envStr := func(serverVar, testVar string) string {
		if v := os.Getenv(serverVar); v != "" {
			return v
		}
		return os.Getenv(testVar)
	}

	config := LogConfig{
		OutputDir:   envStr("LOG_OUTPUT_DIR", "TEST_OUTPUT_DIR"),
		LogRequests: envBool("LOG_REQUESTS", "TEST_LOG_REQUESTS"),
		LogHTML:     envBool("LOG_HTML", "TEST_LOG_HTML"),
		LogDB:       envBool("LOG_DB", "TEST_LOG_DB"),
		LogWS:       envBool("LOG_WS", "TEST_LOG_WS"),
		Debug:       envBool("LOG_DEBUG", "TEST_DEBUG"),
	}
	return NewAppLogger(config)
}

// InitAppLogger initializes the global application logger
func InitAppLogger(config LogConfig) error {
	var err error
	appLogger, err = NewAppLogger(config)
	return err
}

// GetAppLogger returns the global application logger
func GetAppLogger() *AppLogger {
	return appLogger
}

// Close closes all open log files
func (al *AppLogger) Close() {
	if al.requestLog != nil {
		al.requestLog.Close()
	}
	if al.htmlLog != nil {
		al.htmlLog.Close()
	}
	if al.dbLog != nil {
		al.dbLog.Close()
	}
	if al.wsLog != nil {
		al.wsLog.Close()
	}
}

// LogRequest logs an HTTP request and response
func (al *AppLogger) LogRequest(method, url string, reqBody []byte, resp *http.Response, respBody []byte) {
	if !al.logRequests || al.requestLog == nil {
		return
	}

	al.mu.Lock()
	defer al.mu.Unlock()

	al.requestCount++
	timestamp := time.Now().Format("15:04:05.000")

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "\n========== REQUEST #%d [%s] ==========\n", al.requestCount, timestamp)
	fmt.Fprintf(&buf, "%s %s\n", method, url)

	if len(reqBody) > 0 {
		fmt.Fprintf(&buf, "\n--- Request Body ---\n")
		buf.Write(reqBody)
		buf.WriteString("\n")
	}

	if resp != nil {
		fmt.Fprintf(&buf, "\n--- Response [%d %s] ---\n", resp.StatusCode, resp.Status)
		for k, v := range resp.Header {
			fmt.Fprintf(&buf, "%s: %s\n", k, strings.Join(v, ", "))
		}
	}

	if len(respBody) > 0 {
		fmt.Fprintf(&buf, "\n--- Response Body ---\n")
		if len(respBody) > 5000 {
			buf.Write(respBody[:5000])
			fmt.Fprintf(&buf, "\n... (truncated, %d bytes total)\n", len(respBody))
		} else {
			buf.Write(respBody)
		}
		buf.WriteString("\n")
	}

	al.requestLog.Write(buf.Bytes())
}

// LogHTML logs HTML content
func (al *AppLogger) LogHTML(context string, html string) {
	if !al.logHTML || al.htmlLog == nil {
		return
	}

	al.mu.Lock()
	defer al.mu.Unlock()

	al.htmlCount++
	timestamp := time.Now().Format("15:04:05.000")

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "\n========== HTML STATE #%d [%s] ==========\n", al.htmlCount, timestamp)
	fmt.Fprintf(&buf, "Context: %s\n", context)
	fmt.Fprintf(&buf, "\n--- HTML ---\n")

	if len(html) > 10000 {
		buf.WriteString(html[:10000])
		fmt.Fprintf(&buf, "\n... (truncated, %d bytes total)\n", len(html))
	} else {
		buf.WriteString(html)
	}
	buf.WriteString("\n")

	al.htmlLog.Write(buf.Bytes())
}

// LogWebSocket logs a WebSocket message
func (al *AppLogger) LogWebSocket(direction, playerID, message string) {
	if !al.logWS || al.wsLog == nil {
		return
	}

	al.mu.Lock()
	defer al.mu.Unlock()

	al.wsMessageCount++
	timestamp := time.Now().Format("15:04:05.000")

	fmt.Fprintf(al.wsLog, "[%s] #%d %s [Player %s]: %s\n",
		timestamp, al.wsMessageCount, direction, playerID, message)
}

// LogDB dumps the current database state
func (al *AppLogger) LogDB(context string) {
	if !al.logDB || al.dbLog == nil || db == nil {
		return
	}

	al.mu.Lock()
	defer al.mu.Unlock()

	timestamp := time.Now().Format("15:04:05.000")

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "\n========== DATABASE DUMP [%s] ==========\n", timestamp)
	fmt.Fprintf(&buf, "Context: %s\n\n", context)

	// Get all tables from sqlite_master
	var tables []string
	tableRows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		fmt.Fprintf(&buf, "Error getting tables: %v\n", err)
		al.dbLog.Write(buf.Bytes())
		return
	}
	for tableRows.Next() {
		var name string
		if err := tableRows.Scan(&name); err == nil {
			tables = append(tables, name)
		}
	}
	tableRows.Close()

	for _, table := range tables {
		fmt.Fprintf(&buf, "--- Table: %s ---\n", table)

		rows, err := db.Query("SELECT * FROM " + table)
		if err != nil {
			fmt.Fprintf(&buf, "Error: %v\n\n", err)
			continue
		}

		cols, err := rows.Columns()
		if err != nil {
			fmt.Fprintf(&buf, "Error getting columns: %v\n\n", err)
			rows.Close()
			continue
		}

		fmt.Fprintf(&buf, "Columns: %s\n", strings.Join(cols, " | "))

		rowCount := 0
		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		for rows.Next() {
			rowCount++
			if err := rows.Scan(valuePtrs...); err != nil {
				fmt.Fprintf(&buf, "Error scanning row: %v\n", err)
				continue
			}

			var rowStr []string
			for _, v := range values {
				switch val := v.(type) {
				case nil:
					rowStr = append(rowStr, "NULL")
				case []byte:
					rowStr = append(rowStr, string(val))
				default:
					rowStr = append(rowStr, fmt.Sprintf("%v", val))
				}
			}
			fmt.Fprintf(&buf, "Row %d: %s\n", rowCount, strings.Join(rowStr, " | "))
		}
		rows.Close()

		if rowCount == 0 {
			fmt.Fprintf(&buf, "(empty)\n")
		}
		buf.WriteString("\n")
	}

	al.dbLog.Write(buf.Bytes())
}

// Debug logs a debug message if debug mode is enabled
func (al *AppLogger) Debug(format string, args ...any) {
	if !al.debug {
		return
	}
	log.Printf("[DEBUG] "+format, args...)
}

// IsEnabled returns true if any logging is enabled
func (al *AppLogger) IsEnabled() bool {
	return al.logRequests || al.logHTML || al.logDB || al.logWS || al.debug
}

// ============================================================================
// Test-specific wrapper
// ============================================================================

// TestLogger wraps AppLogger for test use with testing.T integration
type TestLogger struct {
	*AppLogger
	t *testing.T
}

// NewTestLogger creates a test logger from environment variables
func NewTestLogger(t *testing.T) *TestLogger {
	config := LogConfig{
		OutputDir:   os.Getenv("TEST_OUTPUT_DIR"),
		LogRequests: os.Getenv("TEST_LOG_REQUESTS") == "1",
		LogHTML:     os.Getenv("TEST_LOG_HTML") == "1",
		LogDB:       os.Getenv("TEST_LOG_DB") == "1",
		LogWS:       os.Getenv("TEST_LOG_WS") == "1",
		Debug:       os.Getenv("TEST_DEBUG") == "1",
	}

	// For tests, use specific log file paths from env
	al := &AppLogger{
		outputDir:   config.OutputDir,
		logRequests: config.LogRequests,
		logHTML:     config.LogHTML,
		logDB:       config.LogDB,
		logWS:       config.LogWS,
		debug:       config.Debug,
	}

	// Open log files from env paths
	if al.logRequests {
		if path := os.Getenv("TEST_REQUEST_LOG"); path != "" {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err == nil {
				al.requestLog = f
			}
		}
	}
	if al.logHTML {
		if path := os.Getenv("TEST_HTML_LOG"); path != "" {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err == nil {
				al.htmlLog = f
			}
		}
	}
	if al.logDB {
		if path := os.Getenv("TEST_DB_LOG"); path != "" {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err == nil {
				al.dbLog = f
			}
		}
	}
	if al.logWS {
		if path := os.Getenv("TEST_WS_LOG"); path != "" {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err == nil {
				al.wsLog = f
			}
		}
	}

	return &TestLogger{AppLogger: al, t: t}
}

// Debug logs a debug message using testing.T.Logf
func (tl *TestLogger) Debug(format string, args ...any) {
	if !tl.debug {
		return
	}
	tl.t.Logf("[DEBUG] "+format, args...)
}

// ============================================================================
// HTTP Middleware
// ============================================================================

// LoggingRoundTripper wraps http.RoundTripper to log requests
type LoggingRoundTripper struct {
	Transport http.RoundTripper
	Logger    *AppLogger
}

func (l *LoggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewBuffer(reqBody))
	}

	resp, err := l.Transport.RoundTrip(req)
	if err != nil {
		l.Logger.LogRequest(req.Method, req.URL.String(), reqBody, nil, nil)
		return resp, err
	}

	var respBody []byte
	if resp.Body != nil {
		respBody, _ = io.ReadAll(resp.Body)
		resp.Body = io.NopCloser(bytes.NewBuffer(respBody))
	}

	l.Logger.LogRequest(req.Method, req.URL.String(), reqBody, resp, respBody)
	return resp, err
}

// LoggingHandler wraps http.Handler to log requests/responses
// Note: WebSocket requests (/ws) are passed through without recording
// because they require http.Hijacker which ResponseRecorder doesn't support
type LoggingHandler struct {
	Handler http.Handler
	Logger  *AppLogger
}

func (l *LoggingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// WebSocket upgrades need http.Hijacker, so pass them through directly
	if r.URL.Path == "/ws" {
		l.Logger.LogRequest(r.Method, r.URL.String(), nil, nil, []byte("[WebSocket upgrade]"))
		l.Handler.ServeHTTP(w, r)
		return
	}

	// Skip static files
	if strings.HasPrefix(r.URL.Path, "/static/") {
		l.Handler.ServeHTTP(w, r)
		return
	}

	var reqBody []byte
	if r.Body != nil {
		reqBody, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewBuffer(reqBody))
	}

	// Use a response recorder
	rec := httptest.NewRecorder()
	l.Handler.ServeHTTP(rec, r)

	// Copy the recorded response to the actual response writer
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	respBody := rec.Body.Bytes()
	w.Write(respBody)

	// Log the request/response
	l.Logger.LogRequest(r.Method, r.URL.String(), reqBody, &http.Response{
		StatusCode: rec.Code,
		Status:     http.StatusText(rec.Code),
		Header:     rec.Header(),
	}, respBody)
}

// ============================================================================
// Global helper functions
// ============================================================================

// LogWSMessage logs a WebSocket message using the global logger
func LogWSMessage(direction, playerID, message string) {
	if appLogger != nil {
		appLogger.LogWebSocket(direction, playerID, message)
	}
}

// LogDBState logs the database state using the global logger
func LogDBState(context string) {
	if appLogger != nil {
		appLogger.LogDB(context)
	}
}

// DebugLog logs a debug message using the global logger
func DebugLog(format string, args ...any) {
	if appLogger != nil {
		appLogger.Debug(format, args...)
	}
}

// CloseAppLogger closes the global application logger
func CloseAppLogger() {
	if appLogger != nil {
		appLogger.Close()
	}
}
