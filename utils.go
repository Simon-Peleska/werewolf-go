package main

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/gorilla/websocket"
	"github.com/jmoiron/sqlx"
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

// ============================================================================
// Test Helpers
// ============================================================================

// Role IDs in the database (based on insert order in initDB)
const (
	RoleVillager = "1"
	RoleWerewolf = "2"
	RoleSeer     = "3"
	RoleDoctor   = "4"
	RoleWitch    = "5"
	RoleHunter   = "6"
	RoleCupid    = "7"
	RoleGuard    = "8"
	RoleMason    = "9"
	RoleWolfCub  = "10"
)

func getFreePort() (int, error) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// TestContext holds test infrastructure including logger
type TestContext struct {
	t       *testing.T
	logger  *TestLogger
	baseURL string
	cleanup func()
}

// newTestContext creates a test context with server and logger
func newTestContext(t *testing.T) *TestContext {
	logger := NewTestLogger(t)

	port, err := getFreePort()
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	var dbErr error
	// Use shared cache mode so all connections see the same in-memory database
	db, dbErr = sqlx.Connect("sqlite3", "file::memory:?mode=memory&cache=shared")
	if dbErr != nil {
		t.Fatalf("Failed to connect to test database: %v", dbErr)
	}

	if err := initDB(); err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	logger.LogDB("after initDB")
	logger.Debug("Database initialized on port %d", port)

	funcMap := template.FuncMap{
		"subtract": func(a, b int) int { return a - b },
	}
	var tmplErr error
	templates, tmplErr = template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if tmplErr != nil {
		t.Fatalf("Failed to parse templates: %v", tmplErr)
	}

	hub = &Hub{
		clients:    make(map[*websocket.Conn]*Client),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *websocket.Conn),
	}
	go hub.run()

	// Create handlers with optional logging wrapper
	mux := http.NewServeMux()

	wrapHandler := func(pattern string, handler http.HandlerFunc) {
		if logger.logRequests {
			mux.Handle(pattern, &LoggingHandler{Handler: handler, Logger: logger.AppLogger})
		} else {
			mux.HandleFunc(pattern, handler)
		}
	}

	wrapHandler("/", handleIndex)
	wrapHandler("/signup", handleSignup)
	wrapHandler("/login", handleLogin)
	wrapHandler("/game", handleGame)
	wrapHandler("/ws", handleWebSocket)
	wrapHandler("/game/component", handleGameComponent)
	wrapHandler("/game/character", handleCharacterInfo)
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go server.ListenAndServe()
	time.Sleep(20 * time.Millisecond)

	cleanup := func() {
		logger.LogDB("before cleanup")
		logger.Debug("Cleaning up test server")
		server.Close()
		db.Close()
		logger.Close()
	}

	return &TestContext{
		t:       t,
		logger:  logger,
		baseURL: fmt.Sprintf("http://localhost:%d", port),
		cleanup: cleanup,
	}
}

// startTestServer starts a test server and returns the base URL and a cleanup function.
// The cleanup function MUST be called at the end of each test iteration to properly
// close the server and database before the next iteration starts.
func startTestServer(t *testing.T) (baseURL string, cleanup func()) {
	ctx := newTestContext(t)
	return ctx.baseURL, ctx.cleanup
}

// Default timeout for browser operations
const browserTimeout = 30 * time.Second

// TestBrowser wraps browser setup for tests
type TestBrowser struct {
	browser *rod.Browser
	t       *testing.T
	logger  *TestLogger
}

// newTestBrowser creates a test browser and returns it along with a cleanup function.
// The cleanup function MUST be called at the end of each test iteration.
func newTestBrowser(t *testing.T) (*TestBrowser, func()) {
	return newTestBrowserWithLogger(t, nil)
}

// newTestBrowserWithLogger creates a test browser with a logger
func newTestBrowserWithLogger(t *testing.T, logger *TestLogger) (*TestBrowser, func()) {
	path, found := launcher.LookPath()
	if !found {
		t.Skip("Chrome/Chromium not found, skipping browser test")
	}

	u, err := launcher.New().Bin(path).Headless(true).Launch()
	if err != nil {
		t.Fatalf("Failed to launch browser: %v", err)
	}

	browser := rod.New().ControlURL(u)
	if err := browser.Connect(); err != nil {
		t.Fatalf("Failed to connect to browser: %v", err)
	}

	if logger != nil {
		logger.Debug("Browser launched and connected")
	}

	tb := &TestBrowser{browser: browser, t: t, logger: logger}
	cleanup := func() {
		if logger != nil {
			logger.Debug("Closing browser")
		}
		browser.MustClose()
	}

	return tb, cleanup
}

// TestPlayer represents a player in tests with their page
type TestPlayer struct {
	Name       string
	SecretCode string
	Page       *rod.Page
	logger     *TestLogger
}

// p returns the page with timeout applied for element operations
func (tp *TestPlayer) p() *rod.Page {
	return tp.Page.Timeout(browserTimeout)
}

// logHTML logs the current HTML state if logging is enabled
func (tp *TestPlayer) logHTML(context string) {
	if tp.logger == nil {
		return
	}
	html, err := tp.Page.HTML()
	if err != nil {
		tp.logger.Debug("Failed to get HTML: %v", err)
		return
	}
	tp.logger.LogHTML(fmt.Sprintf("[%s] %s", tp.Name, context), html)
}

// newIncognitoPage creates a new incognito page with isolated session
func (tb *TestBrowser) newIncognitoPage(url string) *rod.Page {
	page := tb.browser.MustIncognito().MustPage(url)
	page.Timeout(browserTimeout).MustWaitLoad()
	return page
}

// signupPlayer signs up a new player and waits for redirect to game
// Uses incognito context so each player has their own session
func (tb *TestBrowser) signupPlayer(baseURL, name string) *TestPlayer {
	if tb.logger != nil {
		tb.logger.Debug("Signing up player: %s", name)
	}

	page := tb.newIncognitoPage(baseURL)
	p := page.Timeout(browserTimeout)
	p.MustElement("#signup-name").MustInput(name)
	p.MustElement("#btn-signup").MustClick()
	// Wait for HTMX redirect to /game
	time.Sleep(20 * time.Millisecond)

	player := &TestPlayer{
		Name:   name,
		Page:   page,
		logger: tb.logger,
	}
	player.logHTML("after signup")

	if tb.logger != nil {
		tb.logger.LogDB(fmt.Sprintf("after signup: %s", name))
	}

	return player
}

// signupPlayerNoRedirect signs up but expects failure (stays on page)
// Uses incognito context for isolated session
func (tb *TestBrowser) signupPlayerNoRedirect(baseURL, name string) *TestPlayer {
	if tb.logger != nil {
		tb.logger.Debug("Attempting signup (expecting failure): %s", name)
	}

	page := tb.newIncognitoPage(baseURL)
	p := page.Timeout(browserTimeout)
	p.MustElement("#signup-name").MustInput(name)
	p.MustElement("#btn-signup").MustClick()
	time.Sleep(20 * time.Millisecond)

	player := &TestPlayer{
		Name:   name,
		Page:   page,
		logger: tb.logger,
	}
	player.logHTML("after failed signup attempt")

	return player
}

// waitForGame waits for the game content to load
func (tp *TestPlayer) waitForGame() {
	time.Sleep(20 * time.Millisecond)
	tp.logHTML("waitForGame")
}

// reload reloads the page and waits for it to load
func (tp *TestPlayer) reload() {
	tp.p().MustReload().MustWaitLoad()
	tp.logHTML("after reload")
}

// getSecretCode reads the secret code from the game page
func (tp *TestPlayer) getSecretCode() string {
	el, err := tp.p().Element("code")
	if err != nil {
		return ""
	}
	code := strings.TrimSpace(el.MustText())
	if tp.logger != nil {
		tp.logger.Debug("[%s] Got secret code: %s", tp.Name, code)
	}
	return code
}

// getPlayerList returns the player list text
func (tp *TestPlayer) getPlayerList() string {
	el, err := tp.p().Element("#player-list")
	if err != nil {
		return ""
	}
	list := el.MustText()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Player list: %s", tp.Name, strings.ReplaceAll(list, "\n", ", "))
	}
	return list
}

// addRoleByID clicks the add button for a role by its ID
func (tp *TestPlayer) addRoleByID(roleID string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Adding role ID: %s", tp.Name, roleID)
		tp.logger.LogWebSocket("OUT", tp.Name, fmt.Sprintf(`{"action":"update_role","role_id":"%s","delta":"1"}`, roleID))
	}
	tp.p().MustElement("#btn-add-" + roleID).MustClick()
	// Wait for WebSocket round-trip and UI update
	time.Sleep(20 * time.Millisecond)
	tp.logHTML(fmt.Sprintf("after adding role %s", roleID))
}

// getRoleCountByID returns the count for a specific role by ID
func (tp *TestPlayer) getRoleCountByID(roleID string) string {
	el := tp.p().MustElement("#count-" + roleID)
	count := strings.TrimSpace(el.MustText())
	if tp.logger != nil {
		tp.logger.Debug("[%s] Role %s count: %s", tp.Name, roleID, count)
	}
	return count
}

// canStartGame checks if the start button is enabled
func (tp *TestPlayer) canStartGame() bool {
	el := tp.p().MustElement("#btn-start")
	disabled, err := el.Attribute("disabled")
	canStart := err != nil || disabled == nil
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can start game: %v", tp.Name, canStart)
	}
	return canStart
}

// startGame clicks the start button
func (tp *TestPlayer) startGame() {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Starting game", tp.Name)
		tp.logger.LogWebSocket("OUT", tp.Name, `{"action":"start_game"}`)
		tp.logger.LogDB("before game start")
	}
	tp.p().MustElement("#btn-start").MustClick()
	time.Sleep(20 * time.Millisecond)
	tp.logHTML("after starting game")
	if tp.logger != nil {
		tp.logger.LogDB("after game start")
	}
}

// getRole returns the player's assigned role from character-info
func (tp *TestPlayer) getRole() string {
	tp.reload()
	time.Sleep(20 * time.Millisecond)

	el, err := tp.p().Element("#character-info")
	if err != nil {
		return ""
	}
	text := el.MustText()
	// Parse "Role: Villager" from the text
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Role:") {
			role := strings.TrimSpace(strings.TrimPrefix(line, "Role:"))
			if tp.logger != nil {
				tp.logger.Debug("[%s] Assigned role: %s", tp.Name, role)
			}
			return role
		}
	}
	return ""
}

// disconnect closes the player's page/connection
func (tp *TestPlayer) disconnect() {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Disconnecting", tp.Name)
	}
	tp.Page.MustClose()
}

// loginPlayer logs in an existing player (uses incognito for isolated session)
func (tb *TestBrowser) loginPlayer(baseURL, name, secretCode string) *TestPlayer {
	if tb.logger != nil {
		tb.logger.Debug("Logging in player: %s", name)
	}

	page := tb.newIncognitoPage(baseURL)
	p := page.Timeout(browserTimeout)
	p.MustElement("#login-name").MustInput(name)
	p.MustElement("#secret-code").MustInput(secretCode)
	p.MustElement("#btn-login").MustClick()
	time.Sleep(20 * time.Millisecond)

	player := &TestPlayer{
		Name:       name,
		SecretCode: secretCode,
		Page:       page,
		logger:     tb.logger,
	}
	player.logHTML("after login")

	return player
}

// loginPlayerNoRedirect tries to login but expects failure
// Uses incognito context for isolated session
func (tb *TestBrowser) loginPlayerNoRedirect(baseURL, name, secretCode string) *TestPlayer {
	if tb.logger != nil {
		tb.logger.Debug("Attempting login (expecting failure): %s", name)
	}

	page := tb.newIncognitoPage(baseURL)
	p := page.Timeout(browserTimeout)
	p.MustElement("#login-name").MustInput(name)
	p.MustElement("#secret-code").MustInput(secretCode)
	p.MustElement("#btn-login").MustClick()
	time.Sleep(20 * time.Millisecond)

	player := &TestPlayer{
		Name:       name,
		SecretCode: secretCode,
		Page:       page,
		logger:     tb.logger,
	}
	player.logHTML("after failed login attempt")

	return player
}

// hasErrorToast checks if an error toast appeared
func (tp *TestPlayer) hasErrorToast() bool {
	el, err := tp.p().Element(".toast-error")
	hasError := err == nil && el != nil
	if tp.logger != nil {
		tp.logger.Debug("[%s] Has error toast: %v", tp.Name, hasError)
	}
	return hasError
}

// getStatusMessage returns the status message from the lobby
func (tp *TestPlayer) getStatusMessage() string {
	el, err := tp.p().Element("#status-message")
	if err != nil {
		return ""
	}
	status := strings.TrimSpace(el.MustText())
	if tp.logger != nil {
		tp.logger.Debug("[%s] Status message: %s", tp.Name, status)
	}
	return status
}

// isOnGamePage checks if the player is on the game page
func (tp *TestPlayer) isOnGamePage() bool {
	url := tp.p().MustInfo().URL
	onGame := strings.Contains(url, "/game")
	if tp.logger != nil {
		tp.logger.Debug("[%s] On game page: %v (URL: %s)", tp.Name, onGame, url)
	}
	return onGame
}

// isOnIndexPage checks if the player is still on the index page
func (tp *TestPlayer) isOnIndexPage() bool {
	url := tp.p().MustInfo().URL
	return !strings.Contains(url, "/game")
}

// generateTestName creates a unique test name
func generateTestName(base string, n uint8) string {
	suffix := fmt.Sprintf("%d", n)
	if len(base) > 10 {
		base = base[:10]
	}
	return base + suffix
}
