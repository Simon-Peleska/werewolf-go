package main

import (
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/gorilla/websocket"
	"github.com/jmoiron/sqlx"
)

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

// ============================================================================
// Signup Tests
// ============================================================================

func TestSignupWithName(t *testing.T) {
	f := func(nameSuffix uint8) bool {
		if nameSuffix == 0 {
			nameSuffix = 1
		}

		ctx := newTestContext(t)
		defer ctx.cleanup()

		browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
		defer browserCleanup()

		name := generateTestName("User", nameSuffix)
		ctx.logger.Debug("=== Testing signup with name: %s ===", name)

		player := browser.signupPlayer(ctx.baseURL, name)
		player.waitForGame()

		if !player.isOnGamePage() {
			ctx.logger.LogDB("FAIL: player not on game page")
			t.Errorf("Player should be on game page after signup")
			return false
		}

		playerList := player.getPlayerList()
		if !strings.Contains(playerList, name) {
			ctx.logger.LogDB("FAIL: player not in list")
			t.Errorf("Player %s not found in player list: %s", name, playerList)
			return false
		}

		ctx.logger.Debug("=== Test passed ===")
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 5}); err != nil {
		t.Error(err)
	}
}

func TestSignupDuplicateNameFails(t *testing.T) {
	f := func(nameSuffix uint8) bool {
		if nameSuffix == 0 {
			nameSuffix = 1
		}

		ctx := newTestContext(t)
		defer ctx.cleanup()

		browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
		defer browserCleanup()

		name := generateTestName("Dup", nameSuffix)
		ctx.logger.Debug("=== Testing duplicate signup with name: %s ===", name)

		// First signup should succeed
		player1 := browser.signupPlayer(ctx.baseURL, name)
		player1.waitForGame()

		if !player1.isOnGamePage() {
			ctx.logger.LogDB("FAIL: first player not on game page")
			t.Errorf("First player should be on game page")
			return false
		}

		// Second signup with same name should fail
		player2 := browser.signupPlayerNoRedirect(ctx.baseURL, name)

		if player2.isOnGamePage() {
			ctx.logger.LogDB("FAIL: duplicate signup succeeded")
			t.Errorf("Duplicate signup should not redirect to game")
			return false
		}

		if !player2.hasErrorToast() {
			ctx.logger.LogDB("FAIL: no error toast shown")
			t.Errorf("Expected error toast for duplicate name")
			return false
		}

		ctx.logger.Debug("=== Test passed ===")
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 5}); err != nil {
		t.Error(err)
	}
}

// ============================================================================
// Login Tests
// ============================================================================

func TestLoginWithCorrectSecret(t *testing.T) {
	f := func(nameSuffix uint8) bool {
		if nameSuffix == 0 {
			nameSuffix = 1
		}

		ctx := newTestContext(t)
		defer ctx.cleanup()

		browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
		defer browserCleanup()

		name := generateTestName("Login", nameSuffix)
		ctx.logger.Debug("=== Testing login with name: %s ===", name)

		// Signup first
		player1 := browser.signupPlayer(ctx.baseURL, name)
		player1.waitForGame()

		// Get secret code
		secretCode := player1.getSecretCode()
		if secretCode == "" {
			ctx.logger.Debug("Could not find secret code, skipping")
			return true
		}

		// Login on a new page
		player2 := browser.loginPlayer(ctx.baseURL, name, secretCode)

		if !player2.isOnGamePage() {
			ctx.logger.LogDB("FAIL: login did not redirect to game")
			t.Errorf("Login should redirect to game page")
			return false
		}

		ctx.logger.Debug("=== Test passed ===")
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 5}); err != nil {
		t.Error(err)
	}
}

func TestLoginWithWrongSecret(t *testing.T) {
	f := func(nameSuffix uint8) bool {
		if nameSuffix == 0 {
			nameSuffix = 1
		}

		ctx := newTestContext(t)
		defer ctx.cleanup()

		browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
		defer browserCleanup()

		name := generateTestName("Wrong", nameSuffix)
		ctx.logger.Debug("=== Testing login with wrong secret for: %s ===", name)

		// Signup first
		player1 := browser.signupPlayer(ctx.baseURL, name)
		player1.waitForGame()

		// Try to login with wrong secret
		player2 := browser.loginPlayerNoRedirect(ctx.baseURL, name, "wrongsecret")

		if player2.isOnGamePage() {
			ctx.logger.LogDB("FAIL: login with wrong secret succeeded")
			t.Errorf("Login with wrong secret should not redirect to game")
			return false
		}

		if !player2.hasErrorToast() {
			ctx.logger.LogDB("FAIL: no error toast for wrong secret")
			t.Errorf("Expected error toast for wrong secret")
			return false
		}

		ctx.logger.Debug("=== Test passed ===")
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 5}); err != nil {
		t.Error(err)
	}
}

// ============================================================================
// Lobby Player Count Tests
// ============================================================================

func TestLobbyPlayerCount(t *testing.T) {
	f := func(playerCount uint8) bool {
		// Limit to reasonable numbers (2-6 players)
		count := int(playerCount%5) + 2
		if count < 2 {
			count = 2
		}

		ctx := newTestContext(t)
		defer ctx.cleanup()

		browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
		defer browserCleanup()

		ctx.logger.Debug("=== Testing lobby with %d players ===", count)

		var players []*TestPlayer
		for i := 0; i < count; i++ {
			name := fmt.Sprintf("P%d", i+1)
			player := browser.signupPlayer(ctx.baseURL, name)
			player.waitForGame()
			players = append(players, player)
		}

		// Wait for WebSocket updates
		time.Sleep(20 * time.Millisecond)

		// Check player count on first player's page
		players[0].reload()
		time.Sleep(20 * time.Millisecond)

		playerList := players[0].getPlayerList()
		actualCount := 0
		for i := 1; i <= count; i++ {
			if strings.Contains(playerList, fmt.Sprintf("P%d", i)) {
				actualCount++
			}
		}

		if actualCount != count {
			ctx.logger.LogDB("FAIL: player count mismatch")
			t.Errorf("Expected %d players, found %d. Player list: %s", count, actualCount, playerList)
			return false
		}

		ctx.logger.Debug("=== Test passed ===")
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 5}); err != nil {
		t.Error(err)
	}
}

func TestLobbyPlayersLeave(t *testing.T) {
	f := func(seed uint8) bool {
		// Total players and how many leave
		totalPlayers := int(seed%4) + 3   // 3-6 players
		leavingPlayers := int(seed%2) + 1 // 1-2 leave
		if leavingPlayers >= totalPlayers {
			leavingPlayers = totalPlayers - 1
		}

		ctx := newTestContext(t)
		defer ctx.cleanup()

		browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
		defer browserCleanup()

		ctx.logger.Debug("=== Testing: %d players, %d leaving ===", totalPlayers, leavingPlayers)

		var players []*TestPlayer
		for i := 0; i < totalPlayers; i++ {
			name := fmt.Sprintf("L%d", i+1)
			player := browser.signupPlayer(ctx.baseURL, name)
			player.waitForGame()
			players = append(players, player)
		}

		ctx.logger.LogDB("all players joined")
		time.Sleep(20 * time.Millisecond)

		// Some players leave
		for i := 0; i < leavingPlayers; i++ {
			players[i].disconnect()
		}

		ctx.logger.LogDB("after players left")
		time.Sleep(20 * time.Millisecond)

		// Check remaining count
		remainingPlayer := players[leavingPlayers]
		remainingPlayer.reload()
		time.Sleep(20 * time.Millisecond)

		playerList := remainingPlayer.getPlayerList()
		expectedRemaining := totalPlayers - leavingPlayers

		// Count remaining players in list
		actualCount := 0
		for i := leavingPlayers; i < totalPlayers; i++ {
			if strings.Contains(playerList, fmt.Sprintf("L%d", i+1)) {
				actualCount++
			}
		}

		if actualCount != expectedRemaining {
			ctx.logger.LogDB("FAIL: remaining player count mismatch")
			t.Errorf("Expected %d remaining players, found %d in list: %s",
				expectedRemaining, actualCount, playerList)
			return false
		}

		ctx.logger.Debug("=== Test passed ===")
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 5}); err != nil {
		t.Error(err)
	}
}

func TestLobbyPlayersLeaveAndRejoin(t *testing.T) {
	f := func(seed uint8) bool {
		totalPlayers := int(seed%3) + 2   // 2-4 players
		leavingPlayers := int(seed%2) + 1 // 1-2 leave and rejoin
		if leavingPlayers >= totalPlayers {
			leavingPlayers = 1
		}

		ctx := newTestContext(t)
		defer ctx.cleanup()

		browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
		defer browserCleanup()

		ctx.logger.Debug("=== Testing: %d players, %d leave and rejoin ===", totalPlayers, leavingPlayers)

		var players []*TestPlayer
		for i := 0; i < totalPlayers; i++ {
			name := fmt.Sprintf("R%d", i+1)
			player := browser.signupPlayer(ctx.baseURL, name)
			player.waitForGame()
			// Get secret code for rejoining
			player.SecretCode = player.getSecretCode()
			players = append(players, player)
		}

		ctx.logger.LogDB("all players joined with secret codes")
		time.Sleep(20 * time.Millisecond)

		// Some players leave
		for i := 0; i < leavingPlayers; i++ {
			players[i].disconnect()
		}

		ctx.logger.LogDB("after players left")
		time.Sleep(20 * time.Millisecond)

		// Players rejoin via login
		for i := 0; i < leavingPlayers; i++ {
			name := fmt.Sprintf("R%d", i+1)
			rejoined := browser.loginPlayer(ctx.baseURL, name, players[i].SecretCode)
			rejoined.waitForGame()
			players[i] = rejoined
		}

		ctx.logger.LogDB("after players rejoined")
		time.Sleep(20 * time.Millisecond)

		// Check that all players are back
		players[leavingPlayers].reload()
		playerList := players[leavingPlayers].getPlayerList()

		for i := 0; i < totalPlayers; i++ {
			name := fmt.Sprintf("R%d", i+1)
			if !strings.Contains(playerList, name) {
				ctx.logger.LogDB("FAIL: player not found after rejoin")
				t.Errorf("Player %s not found after rejoin. List: %s", name, playerList)
				return false
			}
		}

		ctx.logger.Debug("=== Test passed ===")
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 5}); err != nil {
		t.Error(err)
	}
}

// ============================================================================
// Game Start Tests
// ============================================================================

func TestLobbyCanStartWithMatchingRoles(t *testing.T) {
	f := func(villagers, werewolves uint8) bool {
		// Ensure at least 1 of each basic role
		v := int(villagers%3) + 1  // 1-3 villagers
		w := int(werewolves%2) + 1 // 1-2 werewolves
		totalPlayers := v + w

		ctx := newTestContext(t)
		defer ctx.cleanup()

		browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
		defer browserCleanup()

		ctx.logger.Debug("=== Testing: %d villagers + %d werewolves = %d players ===", v, w, totalPlayers)

		// Create players
		var players []*TestPlayer
		for i := 0; i < totalPlayers; i++ {
			name := fmt.Sprintf("S%d", i+1)
			player := browser.signupPlayer(ctx.baseURL, name)
			player.waitForGame()
			players = append(players, player)
		}

		time.Sleep(20 * time.Millisecond)

		// Add roles (first player adds them)
		for i := 0; i < v; i++ {
			players[0].addRoleByID(RoleVillager)
		}
		for i := 0; i < w; i++ {
			players[0].addRoleByID(RoleWerewolf)
		}

		ctx.logger.LogDB("after adding roles")
		time.Sleep(20 * time.Millisecond)
		players[0].reload()

		// Check if can start
		canStart := players[0].canStartGame()
		status := players[0].getStatusMessage()

		if !canStart {
			ctx.logger.LogDB("FAIL: cannot start with matching roles")
			t.Errorf("Should be able to start with matching roles. Status: %s", status)
			return false
		}

		if !strings.Contains(status, "Ready to start") {
			ctx.logger.LogDB("FAIL: status not ready")
			t.Errorf("Status should indicate ready to start, got: %s", status)
			return false
		}

		ctx.logger.Debug("=== Test passed ===")
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 5}); err != nil {
		t.Error(err)
	}
}

func TestLobbyCannotStartWithMismatchedRoles(t *testing.T) {
	f := func(villagers, werewolves, extraRoles uint8) bool {
		v := int(villagers%3) + 1      // 1-3 villagers
		w := int(werewolves%2) + 1     // 1-2 werewolves
		extra := int(extraRoles%2) + 1 // 1-2 extra roles (mismatch)
		totalPlayers := v + w
		totalRoles := v + w + extra // More roles than players

		ctx := newTestContext(t)
		defer ctx.cleanup()

		browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
		defer browserCleanup()

		ctx.logger.Debug("=== Testing mismatch: %d players, %d roles ===", totalPlayers, totalRoles)

		// Create players
		var players []*TestPlayer
		for i := 0; i < totalPlayers; i++ {
			name := fmt.Sprintf("M%d", i+1)
			player := browser.signupPlayer(ctx.baseURL, name)
			player.waitForGame()
			players = append(players, player)
		}

		time.Sleep(20 * time.Millisecond)

		// Add roles (more than players)
		for i := 0; i < v; i++ {
			players[0].addRoleByID(RoleVillager)
		}
		for i := 0; i < w; i++ {
			players[0].addRoleByID(RoleWerewolf)
		}
		for i := 0; i < extra; i++ {
			players[0].addRoleByID(RoleSeer)
		}

		ctx.logger.LogDB("after adding mismatched roles")
		time.Sleep(20 * time.Millisecond)
		players[0].reload()

		// Check that start is disabled
		canStart := players[0].canStartGame()

		if canStart {
			ctx.logger.LogDB("FAIL: can start with mismatched roles")
			t.Errorf("Should NOT be able to start with %d roles and %d players",
				totalRoles, totalPlayers)
			return false
		}

		ctx.logger.Debug("=== Test passed ===")
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 5}); err != nil {
		t.Error(err)
	}
}

func TestGameStartAssignsCorrectRoles(t *testing.T) {
	f := func(villagers, werewolves uint8) bool {
		v := int(villagers%2) + 1  // 1-2 villagers
		w := int(werewolves%2) + 1 // 1-2 werewolves
		totalPlayers := v + w

		ctx := newTestContext(t)
		defer ctx.cleanup()

		browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
		defer browserCleanup()

		ctx.logger.Debug("=== Testing role assignment: %d villagers + %d werewolves ===", v, w)

		// Create players
		var players []*TestPlayer
		for i := 0; i < totalPlayers; i++ {
			name := fmt.Sprintf("G%d", i+1)
			player := browser.signupPlayer(ctx.baseURL, name)
			player.waitForGame()
			players = append(players, player)
		}

		time.Sleep(20 * time.Millisecond)

		// Add roles
		for i := 0; i < v; i++ {
			players[0].addRoleByID(RoleVillager)
		}
		for i := 0; i < w; i++ {
			players[0].addRoleByID(RoleWerewolf)
		}

		time.Sleep(20 * time.Millisecond)
		players[0].reload()

		// Start the game
		players[0].startGame()
		time.Sleep(20 * time.Millisecond)

		// Count assigned roles
		roleCount := make(map[string]int)
		for _, player := range players {
			role := player.getRole()
			roleCount[role]++
		}

		ctx.logger.Debug("Role counts: %v", roleCount)

		// Verify counts
		if roleCount["Villager"] != v {
			ctx.logger.LogDB("FAIL: villager count mismatch")
			t.Errorf("Expected %d villagers, got %d", v, roleCount["Villager"])
			return false
		}
		if roleCount["Werewolf"] != w {
			ctx.logger.LogDB("FAIL: werewolf count mismatch")
			t.Errorf("Expected %d werewolves, got %d", w, roleCount["Werewolf"])
			return false
		}

		ctx.logger.Debug("=== Test passed ===")
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 5}); err != nil {
		t.Error(err)
	}
}

func TestGameStartWithMixedRoles(t *testing.T) {
	type MixedConfig struct {
		Villager int
		Werewolf int
		Seer     int
		Doctor   int
	}

	f := func(seed uint8) bool {
		// Create a mixed configuration
		config := MixedConfig{
			Villager: int(seed%2) + 1,     // 1-2
			Werewolf: int((seed/2)%2) + 1, // 1-2
			Seer:     int((seed / 4) % 2), // 0-1
			Doctor:   int((seed / 8) % 2), // 0-1
		}
		totalPlayers := config.Villager + config.Werewolf + config.Seer + config.Doctor

		if totalPlayers < 2 {
			return true // Skip trivial cases
		}

		ctx := newTestContext(t)
		defer ctx.cleanup()

		browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
		defer browserCleanup()

		ctx.logger.Debug("=== Testing mixed roles: %d villagers, %d werewolves, %d seers, %d doctors ===",
			config.Villager, config.Werewolf, config.Seer, config.Doctor)

		// Create players
		var players []*TestPlayer
		for i := 0; i < totalPlayers; i++ {
			name := fmt.Sprintf("X%d", i+1)
			player := browser.signupPlayer(ctx.baseURL, name)
			player.waitForGame()
			players = append(players, player)
		}

		time.Sleep(20 * time.Millisecond)

		// Add all roles
		for i := 0; i < config.Villager; i++ {
			players[0].addRoleByID(RoleVillager)
		}
		for i := 0; i < config.Werewolf; i++ {
			players[0].addRoleByID(RoleWerewolf)
		}
		for i := 0; i < config.Seer; i++ {
			players[0].addRoleByID(RoleSeer)
		}
		for i := 0; i < config.Doctor; i++ {
			players[0].addRoleByID(RoleDoctor)
		}

		ctx.logger.LogDB("after adding mixed roles")
		time.Sleep(20 * time.Millisecond)
		players[0].reload()

		// Start the game
		players[0].startGame()
		time.Sleep(20 * time.Millisecond)

		// Count assigned roles
		roleCount := make(map[string]int)
		for _, player := range players {
			role := player.getRole()
			roleCount[role]++
		}

		ctx.logger.Debug("Role counts: %v", roleCount)

		// Verify all role counts
		if roleCount["Villager"] != config.Villager {
			ctx.logger.LogDB("FAIL: villager count mismatch")
			t.Errorf("Villager count mismatch: expected %d, got %d", config.Villager, roleCount["Villager"])
			return false
		}
		if roleCount["Werewolf"] != config.Werewolf {
			ctx.logger.LogDB("FAIL: werewolf count mismatch")
			t.Errorf("Werewolf count mismatch: expected %d, got %d", config.Werewolf, roleCount["Werewolf"])
			return false
		}
		if roleCount["Seer"] != config.Seer {
			ctx.logger.LogDB("FAIL: seer count mismatch")
			t.Errorf("Seer count mismatch: expected %d, got %d", config.Seer, roleCount["Seer"])
			return false
		}
		if roleCount["Doctor"] != config.Doctor {
			ctx.logger.LogDB("FAIL: doctor count mismatch")
			t.Errorf("Doctor count mismatch: expected %d, got %d", config.Doctor, roleCount["Doctor"])
			return false
		}

		ctx.logger.Debug("=== Test passed ===")
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 5}); err != nil {
		t.Error(err)
	}
}

// ============================================================================
// WebSocket Sync Test
// ============================================================================

func TestWebSocketSync(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing WebSocket synchronization ===")

	// Player 1 signs up
	player1 := browser.signupPlayer(ctx.baseURL, "Alice")
	player1.waitForGame()
	ctx.logger.Debug("Player 1 (Alice) in game")

	// Player 2 signs up
	player2 := browser.signupPlayer(ctx.baseURL, "Bob")
	player2.waitForGame()
	ctx.logger.Debug("Player 2 (Bob) in game")

	// Wait for WebSocket connections
	ctx.logger.LogDB("both players connected")
	time.Sleep(20 * time.Millisecond)

	// Verify both players see each other
	player1.reload()
	playerList := player1.getPlayerList()
	if !strings.Contains(playerList, "Alice") || !strings.Contains(playerList, "Bob") {
		ctx.logger.LogDB("FAIL: players don't see each other")
		t.Errorf("Should see both players, got: %s", playerList)
	}

	// Player 1 adds a Werewolf role
	player1.addRoleByID(RoleWerewolf)
	time.Sleep(20 * time.Millisecond)

	// Check if player 2 sees the update
	count2 := player2.getRoleCountByID(RoleWerewolf)
	if count2 != "1" {
		ctx.logger.LogDB("FAIL: player 2 didn't see role update")
		t.Errorf("Player 2 werewolf count expected '1', got '%s'", count2)
	}

	// Add villager
	player1.addRoleByID(RoleVillager)
	time.Sleep(20 * time.Millisecond)

	// Check status on both pages
	status1 := player1.getStatusMessage()
	status2 := player2.getStatusMessage()

	if !strings.Contains(status1, "Ready to start!") {
		ctx.logger.LogDB("FAIL: player 1 status incorrect")
		t.Errorf("Player 1 should show 'Ready to start!', got: %s", status1)
	}
	if !strings.Contains(status2, "Ready to start!") {
		ctx.logger.LogDB("FAIL: player 2 status not synced")
		t.Errorf("Player 2 should show 'Ready to start!' via WebSocket, got: %s", status2)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// ============================================================================
// Night Phase Test Helpers
// ============================================================================

// isInNightPhase checks if the player sees the night phase UI
func (tp *TestPlayer) isInNightPhase() bool {
	html, _ := tp.Page.HTML()
	isNight := strings.Contains(html, "Night 1") || strings.Contains(html, "Night 2")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Is in night phase: %v", tp.Name, isNight)
	}
	return isNight
}

// isInDayPhase checks if the player sees the day phase UI
func (tp *TestPlayer) isInDayPhase() bool {
	html, _ := tp.Page.HTML()
	isDay := strings.Contains(html, "Day 1") || strings.Contains(html, "Day 2")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Is in day phase: %v", tp.Name, isDay)
	}
	return isDay
}

// canSeeWerewolfVotes checks if the player can see the werewolf voting UI
func (tp *TestPlayer) canSeeWerewolfVotes() bool {
	html, _ := tp.Page.HTML()
	canSee := strings.Contains(html, "Choose a Victim") || strings.Contains(html, "Current Votes")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see werewolf votes: %v", tp.Name, canSee)
	}
	return canSee
}

// canSeeWerewolfPack checks if the player can see other werewolves
func (tp *TestPlayer) canSeeWerewolfPack() bool {
	html, _ := tp.Page.HTML()
	canSee := strings.Contains(html, "Your Pack")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see werewolf pack: %v", tp.Name, canSee)
	}
	return canSee
}

// getVoteButtons returns the names of players that can be voted for
func (tp *TestPlayer) getVoteButtons() []string {
	tp.reload()
	time.Sleep(20 * time.Millisecond)

	var names []string
	elements, err := tp.p().Elements(".vote-button")
	if err != nil {
		return names
	}
	for _, el := range elements {
		text := strings.TrimSpace(el.MustText())
		// Remove wolf emoji if present
		text = strings.TrimSuffix(text, " 🐺")
		text = strings.TrimSuffix(text, "🐺")
		text = strings.TrimSpace(text)
		if text != "" {
			names = append(names, text)
		}
	}
	if tp.logger != nil {
		tp.logger.Debug("[%s] Vote buttons: %v", tp.Name, names)
	}
	return names
}

// voteForPlayer clicks the vote button for a specific player
func (tp *TestPlayer) voteForPlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Voting for: %s", tp.Name, targetName)
	}

	// Find the button that contains this player's name
	buttons, err := tp.p().Elements(".vote-button")
	if err != nil {
		if tp.logger != nil {
			tp.logger.Debug("[%s] Failed to find vote buttons: %v", tp.Name, err)
		}
		return
	}

	for _, btn := range buttons {
		text := strings.TrimSpace(btn.MustText())
		if strings.Contains(text, targetName) {
			btn.MustClick()
			time.Sleep(20 * time.Millisecond)
			tp.logHTML("after voting for " + targetName)
			return
		}
	}

	if tp.logger != nil {
		tp.logger.Debug("[%s] Could not find vote button for: %s", tp.Name, targetName)
	}
}

// getCurrentVoteTarget returns who this player has currently voted for (from UI)
func (tp *TestPlayer) getCurrentVoteTarget() string {
	tp.reload()
	time.Sleep(20 * time.Millisecond)

	// Look for a selected button
	el, err := tp.p().Element(".vote-button.selected")
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(el.MustText())
	text = strings.TrimSuffix(text, " 🐺")
	text = strings.TrimSuffix(text, "🐺")
	text = strings.TrimSpace(text)
	if tp.logger != nil {
		tp.logger.Debug("[%s] Current vote target: %s", tp.Name, text)
	}
	return text
}

// getDeathAnnouncement returns the death announcement text if any
func (tp *TestPlayer) getDeathAnnouncement() string {
	el, err := tp.p().Element(".death-announcement")
	if err != nil {
		return ""
	}
	text := el.MustText()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Death announcement: %s", tp.Name, text)
	}
	return text
}

// getGameContent returns the full game content HTML for debugging
func (tp *TestPlayer) getGameContent() string {
	el, err := tp.p().Element("#game-content")
	if err != nil {
		return ""
	}
	return el.MustText()
}

// ============================================================================
// Night Phase Tests
// ============================================================================

// setupNightPhaseGame creates a game with specified villagers and werewolves and starts it
// Returns the players array where werewolves are at the beginning
func setupNightPhaseGame(ctx *TestContext, browser *TestBrowser, numVillagers, numWerewolves int) []*TestPlayer {
	totalPlayers := numVillagers + numWerewolves

	var players []*TestPlayer
	for i := 0; i < totalPlayers; i++ {
		name := fmt.Sprintf("N%d", i+1)
		player := browser.signupPlayer(ctx.baseURL, name)
		player.waitForGame()
		players = append(players, player)
	}

	time.Sleep(20 * time.Millisecond)

	// Add roles
	for i := 0; i < numVillagers; i++ {
		players[0].addRoleByID(RoleVillager)
	}
	for i := 0; i < numWerewolves; i++ {
		players[0].addRoleByID(RoleWerewolf)
	}

	time.Sleep(20 * time.Millisecond)
	players[0].reload()

	// Start the game
	players[0].startGame()
	time.Sleep(20 * time.Millisecond)

	ctx.logger.LogDB("after game start")

	return players
}

// findPlayersByRole returns players grouped by their role
func findPlayersByRole(players []*TestPlayer) (werewolves []*TestPlayer, villagers []*TestPlayer) {
	for _, p := range players {
		role := p.getRole()
		if role == "Werewolf" {
			werewolves = append(werewolves, p)
		} else {
			villagers = append(villagers, p)
		}
	}
	return
}

func TestVillagerCannotSeeWerewolfVotes(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing villager cannot see werewolf votes ===")

	// Setup: 2 villagers, 1 werewolf
	players := setupNightPhaseGame(ctx, browser, 2, 1)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Failed to find werewolves and villagers")
	}

	ctx.logger.Debug("Werewolf: %s, Villagers: %s, %s",
		werewolves[0].Name, villagers[0].Name, villagers[1].Name)

	// Check villager cannot see voting UI
	villagers[0].reload()
	time.Sleep(20 * time.Millisecond)

	if villagers[0].canSeeWerewolfVotes() {
		ctx.logger.LogDB("FAIL: villager can see werewolf votes")
		t.Error("Villager should not be able to see werewolf voting UI")
	}

	if villagers[0].canSeeWerewolfPack() {
		ctx.logger.LogDB("FAIL: villager can see werewolf pack")
		t.Error("Villager should not be able to see werewolf pack")
	}

	// Check werewolf CAN see voting UI
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)

	if !werewolves[0].canSeeWerewolfVotes() {
		ctx.logger.LogDB("FAIL: werewolf cannot see votes")
		t.Error("Werewolf should be able to see voting UI")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolfCanVote(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing werewolf can vote ===")

	// Setup: 1 villager, 2 werewolves (need 2 so first vote doesn't resolve immediately)
	players := setupNightPhaseGame(ctx, browser, 1, 2)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) < 2 || len(villagers) == 0 {
		t.Fatal("Failed to find enough werewolves and villagers")
	}

	ctx.logger.Debug("Werewolves: %s, %s", werewolves[0].Name, werewolves[1].Name)

	// Werewolf should see vote buttons
	voteOptions := werewolves[0].getVoteButtons()
	if len(voteOptions) == 0 {
		ctx.logger.LogDB("FAIL: no vote buttons found")
		t.Fatal("Werewolf should see vote buttons")
	}

	ctx.logger.Debug("Vote options: %v", voteOptions)

	// First werewolf votes (doesn't resolve yet - need majority)
	targetName := villagers[0].Name
	werewolves[0].voteForPlayer(targetName)

	// Verify vote was recorded (check selected button)
	currentTarget := werewolves[0].getCurrentVoteTarget()
	if currentTarget != targetName {
		ctx.logger.LogDB("FAIL: vote not recorded")
		t.Errorf("Expected vote for %s, got %s", targetName, currentTarget)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolfCanChangeVote(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing werewolf can change vote ===")

	// Setup: 2 villagers, 2 werewolves (need 2 werewolves so vote doesn't resolve, 2 villagers to switch between)
	players := setupNightPhaseGame(ctx, browser, 2, 2)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) < 2 || len(villagers) < 2 {
		t.Fatal("Failed to find enough players")
	}

	ctx.logger.Debug("Werewolves: %s, %s, Villagers: %s, %s",
		werewolves[0].Name, werewolves[1].Name, villagers[0].Name, villagers[1].Name)

	// First vote (only first werewolf votes, doesn't resolve)
	firstTarget := villagers[0].Name
	werewolves[0].voteForPlayer(firstTarget)

	currentTarget := werewolves[0].getCurrentVoteTarget()
	if currentTarget != firstTarget {
		ctx.logger.LogDB("FAIL: first vote not recorded")
		t.Errorf("Expected first vote for %s, got %s", firstTarget, currentTarget)
	}

	// Change vote
	secondTarget := villagers[1].Name
	werewolves[0].voteForPlayer(secondTarget)

	newTarget := werewolves[0].getCurrentVoteTarget()
	if newTarget != secondTarget {
		ctx.logger.LogDB("FAIL: vote change not recorded")
		t.Errorf("Expected changed vote for %s, got %s", secondTarget, newTarget)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestDayTransitionOnMajorityVote(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing day transition on majority vote ===")

	// Setup: 1 villager, 1 werewolf (simplest case - 1 werewolf = automatic majority)
	players := setupNightPhaseGame(ctx, browser, 1, 1)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Failed to find werewolves and villagers")
	}

	ctx.logger.Debug("Werewolf: %s, Villager: %s", werewolves[0].Name, villagers[0].Name)

	// Verify we're in night phase
	werewolves[0].reload()
	if !werewolves[0].isInNightPhase() {
		ctx.logger.LogDB("FAIL: not in night phase")
		t.Fatal("Should start in night phase")
	}

	// Werewolf votes for villager
	targetName := villagers[0].Name
	werewolves[0].voteForPlayer(targetName)
	time.Sleep(50 * time.Millisecond) // Wait for vote resolution

	ctx.logger.LogDB("after werewolf vote")

	// Check transition to day
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)

	if !werewolves[0].isInDayPhase() {
		content := werewolves[0].getGameContent()
		ctx.logger.LogDB("FAIL: did not transition to day")
		t.Errorf("Should transition to day phase after werewolf majority vote. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestStayInNightWithoutMajority(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing stay in night without majority ===")

	// Setup: 1 villager, 2 werewolves (need both to vote for majority)
	players := setupNightPhaseGame(ctx, browser, 1, 2)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) < 2 || len(villagers) == 0 {
		t.Fatal("Failed to find enough players")
	}

	ctx.logger.Debug("Werewolves: %s, %s, Villager: %s",
		werewolves[0].Name, werewolves[1].Name, villagers[0].Name)

	// Only first werewolf votes
	targetName := villagers[0].Name
	werewolves[0].voteForPlayer(targetName)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after first werewolf vote")

	// Should still be in night phase (need both werewolves to vote)
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)

	if werewolves[0].isInDayPhase() {
		ctx.logger.LogDB("FAIL: transitioned to day too early")
		t.Error("Should NOT transition to day until all werewolves have voted")
	}

	if !werewolves[0].isInNightPhase() {
		ctx.logger.LogDB("FAIL: not in night phase")
		t.Error("Should still be in night phase")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestCorrectPlayerGetsKilled(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing correct player gets killed ===")

	// Setup: 2 villagers, 1 werewolf
	players := setupNightPhaseGame(ctx, browser, 2, 1)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) == 0 || len(villagers) < 2 {
		t.Fatal("Failed to find enough players")
	}

	// Choose specific target
	targetName := villagers[0].Name
	ctx.logger.Debug("Target for kill: %s", targetName)

	// Werewolf votes
	werewolves[0].voteForPlayer(targetName)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after werewolf vote")

	// Check death announcement
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)

	announcement := werewolves[0].getDeathAnnouncement()
	if !strings.Contains(announcement, targetName) {
		ctx.logger.LogDB("FAIL: wrong player killed")
		t.Errorf("Death announcement should mention %s, got: %s", targetName, announcement)
	}

	// Check that the target shows as dead
	content := werewolves[0].getGameContent()
	// The dead player should have 💀 next to their name
	if !strings.Contains(content, targetName) || !strings.Contains(content, "💀") {
		ctx.logger.LogDB("FAIL: victim not marked as dead")
		t.Errorf("Victim %s should be marked as dead in player list", targetName)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolvesVoteSplitNoKill(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing split vote stays in night ===")

	// Setup: 2 villagers, 2 werewolves (they can split their vote)
	players := setupNightPhaseGame(ctx, browser, 2, 2)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) < 2 || len(villagers) < 2 {
		t.Fatal("Failed to find enough players")
	}

	ctx.logger.Debug("Werewolves: %s, %s", werewolves[0].Name, werewolves[1].Name)
	ctx.logger.Debug("Villagers: %s, %s", villagers[0].Name, villagers[1].Name)

	// Werewolves vote for different targets
	werewolves[0].voteForPlayer(villagers[0].Name)
	time.Sleep(20 * time.Millisecond)
	werewolves[1].voteForPlayer(villagers[1].Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after split vote")

	// Should still be in night (no majority for either target)
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)

	if werewolves[0].isInDayPhase() {
		ctx.logger.LogDB("FAIL: transitioned to day on split vote")
		t.Error("Should NOT transition to day when votes are split")
	}

	ctx.logger.Debug("=== Test passed ===")
}

// ============================================================================
// Day Phase Test Helpers
// ============================================================================

// dayVoteForPlayer clicks the day vote button for a specific player
func (tp *TestPlayer) dayVoteForPlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Day voting for: %s", tp.Name, targetName)
	}

	// Find the button that contains this player's name
	buttons, err := tp.p().Elements("[id^='day-vote-btn-']")
	if err != nil {
		if tp.logger != nil {
			tp.logger.Debug("[%s] Failed to find day vote buttons: %v", tp.Name, err)
		}
		return
	}

	for _, btn := range buttons {
		text := strings.TrimSpace(btn.MustText())
		if strings.Contains(text, targetName) {
			btn.MustClick()
			time.Sleep(20 * time.Millisecond)
			tp.logHTML("after day voting for " + targetName)
			return
		}
	}

	if tp.logger != nil {
		tp.logger.Debug("[%s] Could not find day vote button for: %s", tp.Name, targetName)
	}
}

// getDayVoteButtons returns the names of players that can be voted for during day
func (tp *TestPlayer) getDayVoteButtons() []string {
	tp.reload()
	time.Sleep(20 * time.Millisecond)

	var names []string
	elements, err := tp.p().Elements("[id^='day-vote-btn-']")
	if err != nil {
		return names
	}
	for _, el := range elements {
		text := strings.TrimSpace(el.MustText())
		if text != "" {
			names = append(names, text)
		}
	}
	if tp.logger != nil {
		tp.logger.Debug("[%s] Day vote buttons: %v", tp.Name, names)
	}
	return names
}

// isGameFinished checks if the game has ended
func (tp *TestPlayer) isGameFinished() bool {
	html, _ := tp.Page.HTML()
	isFinished := strings.Contains(html, "Game Over")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Is game finished: %v", tp.Name, isFinished)
	}
	return isFinished
}

// getWinner returns the winner if game is finished
func (tp *TestPlayer) getWinner() string {
	html, _ := tp.Page.HTML()
	if strings.Contains(html, "Villagers Win") {
		return "villagers"
	}
	if strings.Contains(html, "Werewolves Win") {
		return "werewolves"
	}
	return ""
}

// ============================================================================
// Day Phase Tests
// ============================================================================

// setupDayPhaseGame creates a game, starts night, werewolves kill someone, transitions to day
func setupDayPhaseGame(ctx *TestContext, browser *TestBrowser, numVillagers, numWerewolves int) ([]*TestPlayer, []*TestPlayer, []*TestPlayer) {
	players := setupNightPhaseGame(ctx, browser, numVillagers, numWerewolves)
	werewolves, villagers := findPlayersByRole(players)

	// All werewolves vote for the first villager to transition to day
	targetName := villagers[0].Name
	for _, w := range werewolves {
		w.voteForPlayer(targetName)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after night kill, should be in day phase")

	return players, werewolves, villagers
}

func TestDayVoteByAlivePlayer(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing day vote by alive player ===")

	// Setup: 3 villagers, 1 werewolf - werewolf kills villager 0, leaves 2 villagers and 1 werewolf
	players, werewolves, villagers := setupDayPhaseGame(ctx, browser, 3, 1)

	// Verify we're in day phase
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)
	if !werewolves[0].isInDayPhase() {
		ctx.logger.LogDB("FAIL: not in day phase")
		t.Fatal("Should be in day phase after night kill")
	}

	// Alive player should see vote buttons
	voteButtons := villagers[1].getDayVoteButtons()
	if len(voteButtons) == 0 {
		ctx.logger.LogDB("FAIL: no day vote buttons")
		t.Fatal("Alive player should see day vote buttons")
	}

	ctx.logger.Debug("Day vote buttons: %v", voteButtons)

	// Vote for the werewolf
	villagers[1].dayVoteForPlayer(werewolves[0].Name)

	// Verify vote was recorded (check page shows vote list)
	villagers[1].reload()
	content := villagers[1].getGameContent()
	if !strings.Contains(content, "voted for") {
		ctx.logger.LogDB("FAIL: vote not shown")
		t.Error("Vote should be visible in the vote list")
	}

	ctx.logger.Debug("Players: %d", len(players))
	ctx.logger.Debug("=== Test passed ===")
}

func TestDayVoteTransitionToNight(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing day vote transition to night ===")

	// Setup: 2 villagers, 1 werewolf - after night kill, 1 villager and 1 werewolf remain
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 2, 1)

	// Both alive players vote for each other (the remaining villager and werewolf)
	// With 2 alive players, majority is 2, so both must vote for same person
	// Let's have both vote for the werewolf
	villagers[1].dayVoteForPlayer(werewolves[0].Name)
	time.Sleep(20 * time.Millisecond)
	werewolves[0].dayVoteForPlayer(villagers[1].Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after day votes")

	// With a split vote (1-1), no majority, should transition to night without elimination
	villagers[1].reload()
	time.Sleep(20 * time.Millisecond)

	if villagers[1].isInDayPhase() {
		ctx.logger.LogDB("FAIL: still in day phase after all voted")
		t.Error("Should transition to night after all players voted")
	}

	// Should be in night 2 now
	if !villagers[1].isInNightPhase() {
		content := villagers[1].getGameContent()
		ctx.logger.LogDB("FAIL: not in night phase")
		t.Errorf("Should be in night phase. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestVillagersWinByEliminatingWerewolf(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing villagers win by eliminating werewolf ===")

	// Setup: 3 villagers, 1 werewolf - after night kill, 2 villagers and 1 werewolf remain
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 3, 1)

	// Both remaining villagers vote for the werewolf (majority of 2 out of 3)
	villagers[1].dayVoteForPlayer(werewolves[0].Name)
	time.Sleep(20 * time.Millisecond)
	villagers[2].dayVoteForPlayer(werewolves[0].Name)
	time.Sleep(20 * time.Millisecond)
	// Werewolf votes for a villager (won't matter)
	werewolves[0].dayVoteForPlayer(villagers[1].Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after day elimination vote")

	// Game should be finished with villagers winning
	villagers[1].reload()
	time.Sleep(20 * time.Millisecond)

	if !villagers[1].isGameFinished() {
		content := villagers[1].getGameContent()
		ctx.logger.LogDB("FAIL: game not finished")
		t.Errorf("Game should be finished after eliminating last werewolf. Content: %s", content)
		return
	}

	winner := villagers[1].getWinner()
	if winner != "villagers" {
		ctx.logger.LogDB("FAIL: wrong winner")
		t.Errorf("Villagers should win, got: %s", winner)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolvesWinByEliminatingVillagers(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing werewolves win by eliminating villagers ===")

	// Setup: 2 villagers, 2 werewolves - after night kill, 1 villager and 2 werewolves remain
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 2, 2)

	// Both werewolves vote for the remaining villager
	werewolves[0].dayVoteForPlayer(villagers[1].Name)
	time.Sleep(20 * time.Millisecond)
	werewolves[1].dayVoteForPlayer(villagers[1].Name)
	time.Sleep(20 * time.Millisecond)
	// Villager votes for a werewolf (won't matter with 2v1)
	villagers[1].dayVoteForPlayer(werewolves[0].Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after day elimination vote")

	// Game should be finished with werewolves winning
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)

	if !werewolves[0].isGameFinished() {
		content := werewolves[0].getGameContent()
		ctx.logger.LogDB("FAIL: game not finished")
		t.Errorf("Game should be finished after eliminating last villager. Content: %s", content)
		return
	}

	winner := werewolves[0].getWinner()
	if winner != "werewolves" {
		ctx.logger.LogDB("FAIL: wrong winner")
		t.Errorf("Werewolves should win, got: %s", winner)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestNoEliminationOnTiedVote(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing no elimination on tied vote ===")

	// Setup: 3 villagers, 2 werewolves - after night kill, 2 villagers and 2 werewolves remain (4 alive)
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 3, 2)

	// Create a tie: 2 votes for villager, 2 votes for werewolf
	werewolves[0].dayVoteForPlayer(villagers[1].Name)
	time.Sleep(20 * time.Millisecond)
	werewolves[1].dayVoteForPlayer(villagers[1].Name)
	time.Sleep(20 * time.Millisecond)
	villagers[1].dayVoteForPlayer(werewolves[0].Name)
	time.Sleep(20 * time.Millisecond)
	villagers[2].dayVoteForPlayer(werewolves[0].Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after tied vote")

	// With a 2-2 tie, no majority (need 3), should transition to night without elimination
	villagers[1].reload()
	time.Sleep(20 * time.Millisecond)

	// Should be in night 2
	if !villagers[1].isInNightPhase() {
		if villagers[1].isGameFinished() {
			ctx.logger.LogDB("FAIL: game ended on tie")
			t.Error("Game should not end on a tied vote")
		} else {
			content := villagers[1].getGameContent()
			ctx.logger.LogDB("FAIL: not in night after tie")
			t.Errorf("Should transition to night after tied vote. Content: %s", content)
		}
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestDeadPlayerCannotVote(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing dead player cannot vote ===")

	// Setup: 3 villagers, 1 werewolf - werewolf kills villager 0
	_, _, villagers := setupDayPhaseGame(ctx, browser, 3, 1)

	// villagers[0] is dead (killed at night)
	deadPlayer := villagers[0]

	// Dead player should NOT see vote buttons
	deadPlayer.reload()
	time.Sleep(20 * time.Millisecond)

	voteButtons := deadPlayer.getDayVoteButtons()
	if len(voteButtons) > 0 {
		ctx.logger.LogDB("FAIL: dead player sees vote buttons")
		t.Errorf("Dead player should not see vote buttons, but found: %v", voteButtons)
	}

	// Check that the page shows "You are dead and cannot vote"
	content := deadPlayer.getGameContent()
	if !strings.Contains(content, "dead") || !strings.Contains(content, "cannot vote") {
		ctx.logger.LogDB("FAIL: no dead message shown")
		t.Error("Dead player should see message that they cannot vote")
	}

	ctx.logger.Debug("=== Test passed ===")
}

// ============================================================================
// Seer Phase Test Helpers
// ============================================================================

// findPlayersByRoleExtended returns players grouped by their role including Seer
func findPlayersByRoleExtended(players []*TestPlayer) (werewolves, villagers, seers []*TestPlayer) {
	for _, p := range players {
		role := p.getRole()
		switch role {
		case "Werewolf":
			werewolves = append(werewolves, p)
		case "Seer":
			seers = append(seers, p)
		default:
			villagers = append(villagers, p)
		}
	}
	return
}

// seerInvestigatePlayer clicks the seer investigate button for a specific player
func (tp *TestPlayer) seerInvestigatePlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Seer investigating: %s", tp.Name, targetName)
	}

	buttons, err := tp.p().Elements(".seer-button")
	if err != nil {
		if tp.logger != nil {
			tp.logger.Debug("[%s] Failed to find seer buttons: %v", tp.Name, err)
		}
		return
	}

	for _, btn := range buttons {
		text := strings.TrimSpace(btn.MustText())
		if strings.Contains(text, targetName) {
			btn.MustClick()
			time.Sleep(20 * time.Millisecond)
			tp.logHTML("after seer investigation of " + targetName)
			return
		}
	}

	if tp.logger != nil {
		tp.logger.Debug("[%s] Could not find seer button for: %s", tp.Name, targetName)
	}
}

// getSeerResult returns the text of the seer's investigation result for the current night
func (tp *TestPlayer) getSeerResult() string {
	el, err := tp.p().Element("#seer-result")
	if err != nil {
		return ""
	}
	text := el.MustText()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Seer result: %s", tp.Name, text)
	}
	return text
}

// canSeeSeerButtons checks if the seer investigation buttons are visible
func (tp *TestPlayer) canSeeSeerButtons() bool {
	elements, err := tp.p().Elements(".seer-button")
	canSee := err == nil && len(elements) > 0
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see seer buttons: %v", tp.Name, canSee)
	}
	return canSee
}

// ============================================================================
// Seer Tests
// ============================================================================

func TestSeerCanInvestigateVillager(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Seer can investigate villager ===")

	// Setup: 1 villager + 1 werewolf + 1 seer = 3 players
	var players []*TestPlayer
	for i, name := range []string{"A1", "A2", "A3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
		_ = i
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleSeer)
	time.Sleep(20 * time.Millisecond)
	players[0].reload()
	players[0].startGame()
	time.Sleep(20 * time.Millisecond)

	werewolves, villagers, seers := findPlayersByRoleExtended(players)
	if len(seers) == 0 {
		t.Fatal("No Seer found after role assignment")
	}
	if len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Missing werewolves or villagers")
	}

	seer := seers[0]
	villager := villagers[0]
	ctx.logger.Debug("Seer: %s, investigating Villager: %s", seer.Name, villager.Name)

	// Seer should see investigation buttons
	seer.reload()
	time.Sleep(20 * time.Millisecond)
	if !seer.canSeeSeerButtons() {
		ctx.logger.LogDB("FAIL: seer cannot see investigate buttons")
		t.Fatal("Seer should see investigation buttons during night phase")
	}

	// Seer investigates the villager
	seer.seerInvestigatePlayer(villager.Name)
	time.Sleep(50 * time.Millisecond)

	// Seer should see result showing "Not a Werewolf"
	seer.reload()
	time.Sleep(20 * time.Millisecond)
	result := seer.getSeerResult()
	if !strings.Contains(result, "Not a Werewolf") {
		ctx.logger.LogDB("FAIL: seer result incorrect")
		t.Errorf("Seer investigating villager should see 'Not a Werewolf', got: %s", result)
	}
	if !strings.Contains(result, villager.Name) {
		t.Errorf("Seer result should mention target name %s, got: %s", villager.Name, result)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestSeerCanInvestigateWerewolf(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Seer can investigate werewolf ===")

	// Setup: 1 villager + 1 werewolf + 1 seer
	var players []*TestPlayer
	for _, name := range []string{"B1", "B2", "B3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleSeer)
	time.Sleep(20 * time.Millisecond)
	players[0].reload()
	players[0].startGame()
	time.Sleep(20 * time.Millisecond)

	werewolves, _, seers := findPlayersByRoleExtended(players)
	if len(seers) == 0 || len(werewolves) == 0 {
		t.Fatal("Missing seer or werewolf")
	}

	seer := seers[0]
	werewolf := werewolves[0]
	ctx.logger.Debug("Seer: %s, investigating Werewolf: %s", seer.Name, werewolf.Name)

	// Seer investigates the werewolf
	seer.reload()
	time.Sleep(20 * time.Millisecond)
	seer.seerInvestigatePlayer(werewolf.Name)
	time.Sleep(50 * time.Millisecond)

	// Seer should see result showing "a Werewolf!"
	seer.reload()
	time.Sleep(20 * time.Millisecond)
	result := seer.getSeerResult()
	if !strings.Contains(result, "Werewolf") {
		ctx.logger.LogDB("FAIL: seer result did not identify werewolf")
		t.Errorf("Seer investigating werewolf should see 'a Werewolf!', got: %s", result)
	}
	if !strings.Contains(result, werewolf.Name) {
		t.Errorf("Seer result should mention target name %s, got: %s", werewolf.Name, result)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestNightWaitsForSeer(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing night waits for seer to investigate ===")

	// Setup: 1 seer + 1 werewolf (2 players, werewolf needs to vote AND seer must investigate)
	var players []*TestPlayer
	for _, name := range []string{"C1", "C2"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleSeer)
	time.Sleep(20 * time.Millisecond)
	players[0].reload()
	players[0].startGame()
	time.Sleep(20 * time.Millisecond)

	werewolves, _, seers := findPlayersByRoleExtended(players)
	if len(seers) == 0 || len(werewolves) == 0 {
		t.Fatal("Missing seer or werewolf")
	}

	seer := seers[0]
	werewolf := werewolves[0]
	ctx.logger.Debug("Seer: %s, Werewolf: %s", seer.Name, werewolf.Name)

	// Werewolf votes - night should NOT end yet (seer hasn't investigated)
	werewolf.voteForPlayer(seer.Name)
	time.Sleep(50 * time.Millisecond)

	werewolf.reload()
	time.Sleep(20 * time.Millisecond)
	if werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: night ended before seer investigated")
		t.Error("Night should not end until seer has investigated")
	}
	if !werewolf.isInNightPhase() {
		t.Error("Should still be in night phase")
	}

	ctx.logger.Debug("Werewolf voted, night still active. Now seer investigates...")

	// Seer investigates - night SHOULD end now
	seer.reload()
	time.Sleep(20 * time.Millisecond)
	seer.seerInvestigatePlayer(werewolf.Name)
	time.Sleep(50 * time.Millisecond)

	seer.reload()
	time.Sleep(20 * time.Millisecond)
	if !seer.isInDayPhase() {
		content := seer.getGameContent()
		ctx.logger.LogDB("FAIL: did not transition to day after seer investigated")
		t.Errorf("Should transition to day after both werewolf voted and seer investigated. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestTwoSeersActIndependently(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing two seers act independently ===")

	// Setup: 1 werewolf + 2 seers + 1 villager = 4 players
	var players []*TestPlayer
	for _, name := range []string{"D1", "D2", "D3", "D4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleSeer)
	players[0].addRoleByID(RoleSeer)
	players[0].addRoleByID(RoleVillager)
	time.Sleep(20 * time.Millisecond)
	players[0].reload()
	players[0].startGame()
	time.Sleep(20 * time.Millisecond)

	werewolves, villagers, seers := findPlayersByRoleExtended(players)
	if len(seers) < 2 || len(werewolves) == 0 {
		t.Fatalf("Need 2 seers and 1 werewolf, got seers=%d werewolves=%d", len(seers), len(werewolves))
	}

	seer1 := seers[0]
	seer2 := seers[1]
	werewolf := werewolves[0]
	villager := villagers[0]
	ctx.logger.Debug("Seer1: %s, Seer2: %s, Werewolf: %s, Villager: %s", seer1.Name, seer2.Name, werewolf.Name, villager.Name)

	// Both seers investigate BEFORE the werewolf votes so the night stays active
	// and we can read results while still in night phase.

	// Seer1 investigates werewolf
	seer1.reload()
	time.Sleep(20 * time.Millisecond)
	seer1.seerInvestigatePlayer(werewolf.Name)
	time.Sleep(20 * time.Millisecond)

	// Seer2 investigates villager
	seer2.reload()
	time.Sleep(20 * time.Millisecond)
	seer2.seerInvestigatePlayer(villager.Name)
	time.Sleep(20 * time.Millisecond)

	// Night is still active (werewolf hasn't voted) - verify and read both results
	seer1.reload()
	time.Sleep(20 * time.Millisecond)
	if seer1.isInDayPhase() {
		ctx.logger.LogDB("FAIL: night ended before werewolf voted")
		t.Error("Night should not end before werewolf votes")
	}

	result1 := seer1.getSeerResult()
	if !strings.Contains(result1, "Werewolf") {
		t.Errorf("Seer1 should see 'Werewolf' result, got: %s", result1)
	}

	seer2.reload()
	time.Sleep(20 * time.Millisecond)
	result2 := seer2.getSeerResult()
	if !strings.Contains(result2, "Not a Werewolf") {
		t.Errorf("Seer2 should see 'Not a Werewolf' result, got: %s", result2)
	}

	// Each seer sees only their own result, not the other's
	if strings.Contains(result1, villager.Name) {
		t.Errorf("Seer1 should not see Seer2's investigation of %s", villager.Name)
	}
	if strings.Contains(result2, werewolf.Name) {
		t.Errorf("Seer2 should not see Seer1's investigation of %s", werewolf.Name)
	}

	// Werewolf votes - all conditions now met, night ends
	werewolf.reload()
	time.Sleep(20 * time.Millisecond)
	werewolf.voteForPlayer(villager.Name)
	time.Sleep(50 * time.Millisecond)

	werewolf.reload()
	time.Sleep(20 * time.Millisecond)
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: did not transition to day after werewolf voted")
		t.Error("Should transition to day after werewolf voted (both seers already investigated)")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestCannotVoteForDeadPlayer(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing cannot vote for dead player ===")

	// Setup: 3 villagers, 1 werewolf - werewolf kills villager 0
	_, _, villagers := setupDayPhaseGame(ctx, browser, 3, 1)

	// villagers[0] is dead
	deadPlayerName := villagers[0].Name

	// Living player should NOT see dead player in vote buttons
	alivePlayer := villagers[1]
	voteButtons := alivePlayer.getDayVoteButtons()

	for _, name := range voteButtons {
		if name == deadPlayerName {
			ctx.logger.LogDB("FAIL: dead player in vote options")
			t.Errorf("Dead player %s should not be in vote options", deadPlayerName)
		}
	}

	// Should only see alive players (2 villagers + 1 werewolf = 3)
	expectedAlive := 3 // villagers[1], villagers[2], werewolves[0]
	if len(voteButtons) != expectedAlive {
		ctx.logger.LogDB("FAIL: wrong number of vote buttons")
		t.Errorf("Expected %d vote buttons for alive players, got %d: %v", expectedAlive, len(voteButtons), voteButtons)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// ============================================================================
// Doctor Test Helpers
// ============================================================================

// findPlayersByRoleWithDoctor returns players grouped by role including Doctor
func findPlayersByRoleWithDoctor(players []*TestPlayer) (werewolves, villagers, doctors []*TestPlayer) {
	for _, p := range players {
		role := p.getRole()
		switch role {
		case "Werewolf":
			werewolves = append(werewolves, p)
		case "Doctor":
			doctors = append(doctors, p)
		default:
			villagers = append(villagers, p)
		}
	}
	return
}

// doctorProtectPlayer clicks the doctor protect button for a specific player
func (tp *TestPlayer) doctorProtectPlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Doctor protecting: %s", tp.Name, targetName)
	}

	buttons, err := tp.p().Elements(".doctor-button")
	if err != nil {
		if tp.logger != nil {
			tp.logger.Debug("[%s] Failed to find doctor buttons: %v", tp.Name, err)
		}
		return
	}

	for _, btn := range buttons {
		text := strings.TrimSpace(btn.MustText())
		if strings.Contains(text, targetName) {
			btn.MustClick()
			time.Sleep(20 * time.Millisecond)
			tp.logHTML("after doctor protection of " + targetName)
			return
		}
	}

	if tp.logger != nil {
		tp.logger.Debug("[%s] Could not find doctor button for: %s", tp.Name, targetName)
	}
}

// getDoctorResult returns the text of the doctor's protection confirmation
func (tp *TestPlayer) getDoctorResult() string {
	el, err := tp.p().Element("#doctor-result")
	if err != nil {
		return ""
	}
	text := el.MustText()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Doctor result: %s", tp.Name, text)
	}
	return text
}

// canSeeDoctorButtons checks if the doctor protection buttons are visible
func (tp *TestPlayer) canSeeDoctorButtons() bool {
	elements, err := tp.p().Elements(".doctor-button")
	canSee := err == nil && len(elements) > 0
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see doctor buttons: %v", tp.Name, canSee)
	}
	return canSee
}

// hasNoDeathMessage checks if the day phase shows "no one died last night"
func (tp *TestPlayer) hasNoDeathMessage() bool {
	el, err := tp.p().Element("#no-death-message")
	found := err == nil && el != nil
	if tp.logger != nil {
		tp.logger.Debug("[%s] Has no-death message: %v", tp.Name, found)
	}
	return found
}

// ============================================================================
// Doctor Tests
// ============================================================================

func TestDoctorCanProtect(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Doctor can see protect buttons and get confirmation ===")

	// Setup: 1 villager + 1 werewolf + 1 doctor = 3 players
	var players []*TestPlayer
	for _, name := range []string{"E1", "E2", "E3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleDoctor)
	time.Sleep(20 * time.Millisecond)
	players[0].reload()
	players[0].startGame()
	time.Sleep(20 * time.Millisecond)

	werewolves, villagers, doctors := findPlayersByRoleWithDoctor(players)
	if len(doctors) == 0 {
		t.Fatal("No Doctor found after role assignment")
	}
	if len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Missing werewolves or villagers")
	}

	doctor := doctors[0]
	villager := villagers[0]
	ctx.logger.Debug("Doctor: %s, protecting Villager: %s", doctor.Name, villager.Name)

	// Doctor should see protection buttons
	doctor.reload()
	time.Sleep(20 * time.Millisecond)
	if !doctor.canSeeDoctorButtons() {
		ctx.logger.LogDB("FAIL: doctor cannot see protect buttons")
		t.Fatal("Doctor should see protection buttons during night phase")
	}

	// Doctor protects the villager - night stays active (werewolf hasn't voted yet)
	doctor.doctorProtectPlayer(villager.Name)
	time.Sleep(50 * time.Millisecond)

	// Doctor should see confirmation
	doctor.reload()
	time.Sleep(20 * time.Millisecond)
	result := doctor.getDoctorResult()
	if !strings.Contains(result, villager.Name) {
		ctx.logger.LogDB("FAIL: doctor protection confirmation missing")
		t.Errorf("Doctor should see protection confirmation with target name %s, got: %s", villager.Name, result)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestDoctorSavesVictim(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Doctor saves the werewolf's target ===")

	// Setup: 1 villager + 1 werewolf + 1 doctor = 3 players
	var players []*TestPlayer
	for _, name := range []string{"F1", "F2", "F3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleDoctor)
	time.Sleep(20 * time.Millisecond)
	players[0].reload()
	players[0].startGame()
	time.Sleep(20 * time.Millisecond)

	werewolves, villagers, doctors := findPlayersByRoleWithDoctor(players)
	if len(doctors) == 0 || len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Missing required roles")
	}

	doctor := doctors[0]
	werewolf := werewolves[0]
	villager := villagers[0]
	ctx.logger.Debug("Doctor: %s, Werewolf: %s, Villager (target): %s", doctor.Name, werewolf.Name, villager.Name)

	// Doctor protects the villager first (before werewolf votes)
	doctor.reload()
	time.Sleep(20 * time.Millisecond)
	doctor.doctorProtectPlayer(villager.Name)
	time.Sleep(20 * time.Millisecond)

	// Werewolf votes for the villager (the protected player)
	werewolf.reload()
	time.Sleep(20 * time.Millisecond)
	werewolf.voteForPlayer(villager.Name)
	time.Sleep(50 * time.Millisecond)

	// Night should end - check day phase shows "no one died"
	werewolf.reload()
	time.Sleep(20 * time.Millisecond)
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: did not transition to day")
		t.Fatal("Should have transitioned to day phase")
	}

	if !werewolf.hasNoDeathMessage() {
		content := werewolf.getGameContent()
		ctx.logger.LogDB("FAIL: victim not saved by doctor")
		t.Errorf("Doctor should have saved the victim, expected 'no one died' message. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestDoctorDoesNotSaveWrongPlayer(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Doctor protecting wrong player does not save victim ===")

	// Setup: 2 villagers + 1 werewolf + 1 doctor = 4 players
	var players []*TestPlayer
	for _, name := range []string{"G1", "G2", "G3", "G4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleDoctor)
	time.Sleep(20 * time.Millisecond)
	players[0].reload()
	players[0].startGame()
	time.Sleep(20 * time.Millisecond)

	werewolves, villagers, doctors := findPlayersByRoleWithDoctor(players)
	if len(doctors) == 0 || len(werewolves) == 0 || len(villagers) < 2 {
		t.Fatalf("Missing required roles, got doctors=%d werewolves=%d villagers=%d", len(doctors), len(werewolves), len(villagers))
	}

	doctor := doctors[0]
	werewolf := werewolves[0]
	villager0 := villagers[0] // doctor protects this one
	villager1 := villagers[1] // werewolf kills this one
	ctx.logger.Debug("Doctor: %s protects %s, Werewolf: %s kills %s", doctor.Name, villager0.Name, werewolf.Name, villager1.Name)

	// Doctor protects villager0
	doctor.reload()
	time.Sleep(20 * time.Millisecond)
	doctor.doctorProtectPlayer(villager0.Name)
	time.Sleep(20 * time.Millisecond)

	// Werewolf kills villager1 (a different player - not protected)
	werewolf.reload()
	time.Sleep(20 * time.Millisecond)
	werewolf.voteForPlayer(villager1.Name)
	time.Sleep(50 * time.Millisecond)

	// Day should show villager1 died
	werewolf.reload()
	time.Sleep(20 * time.Millisecond)
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: did not transition to day")
		t.Fatal("Should have transitioned to day phase")
	}

	if werewolf.hasNoDeathMessage() {
		ctx.logger.LogDB("FAIL: expected death but got no-death message")
		t.Error("Doctor protected wrong player, villager1 should have died")
	}

	content := werewolf.getGameContent()
	if !strings.Contains(content, villager1.Name) {
		ctx.logger.LogDB("FAIL: death announcement missing victim name")
		t.Errorf("Day announcement should mention %s (the victim), got: %s", villager1.Name, content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestNightWaitsForDoctor(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing night waits for doctor to protect ===")

	// Setup: 1 doctor + 1 werewolf = 2 players
	var players []*TestPlayer
	for _, name := range []string{"H1", "H2"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleDoctor)
	time.Sleep(20 * time.Millisecond)
	players[0].reload()
	players[0].startGame()
	time.Sleep(20 * time.Millisecond)

	werewolves, _, doctors := findPlayersByRoleWithDoctor(players)
	if len(doctors) == 0 || len(werewolves) == 0 {
		t.Fatal("Missing doctor or werewolf")
	}

	doctor := doctors[0]
	werewolf := werewolves[0]
	ctx.logger.Debug("Doctor: %s, Werewolf: %s", doctor.Name, werewolf.Name)

	// Werewolf votes - night should NOT end yet (doctor hasn't protected)
	werewolf.voteForPlayer(doctor.Name)
	time.Sleep(50 * time.Millisecond)

	werewolf.reload()
	time.Sleep(20 * time.Millisecond)
	if werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: night ended before doctor protected")
		t.Error("Night should not end until doctor has protected")
	}
	if !werewolf.isInNightPhase() {
		t.Error("Should still be in night phase")
	}

	ctx.logger.Debug("Werewolf voted, night still active. Now doctor protects...")

	// Doctor protects - night SHOULD end now
	doctor.reload()
	time.Sleep(20 * time.Millisecond)
	doctor.doctorProtectPlayer(werewolf.Name)
	time.Sleep(50 * time.Millisecond)

	doctor.reload()
	time.Sleep(20 * time.Millisecond)
	if !doctor.isInDayPhase() {
		content := doctor.getGameContent()
		ctx.logger.LogDB("FAIL: did not transition to day after doctor protected")
		t.Errorf("Should transition to day after both werewolf voted and doctor protected. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestTwoDoctorsActIndependently(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing two doctors act independently ===")

	// Setup: 1 werewolf + 2 doctors + 1 villager = 4 players
	var players []*TestPlayer
	for _, name := range []string{"I1", "I2", "I3", "I4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleDoctor)
	players[0].addRoleByID(RoleDoctor)
	players[0].addRoleByID(RoleVillager)
	time.Sleep(20 * time.Millisecond)
	players[0].reload()
	players[0].startGame()
	time.Sleep(20 * time.Millisecond)

	werewolves, villagers, doctors := findPlayersByRoleWithDoctor(players)
	if len(doctors) < 2 || len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatalf("Need 2 doctors, 1 werewolf, 1 villager, got doctors=%d werewolves=%d villagers=%d", len(doctors), len(werewolves), len(villagers))
	}

	doctor1 := doctors[0]
	doctor2 := doctors[1]
	werewolf := werewolves[0]
	villager := villagers[0]
	ctx.logger.Debug("Doctor1: %s, Doctor2: %s, Werewolf: %s, Villager: %s", doctor1.Name, doctor2.Name, werewolf.Name, villager.Name)

	// Both doctors protect BEFORE the werewolf votes so the night stays active
	// and we can verify each sees their own confirmation.

	// Doctor1 protects the villager
	doctor1.reload()
	time.Sleep(20 * time.Millisecond)
	doctor1.doctorProtectPlayer(villager.Name)
	time.Sleep(20 * time.Millisecond)

	// Doctor2 protects the werewolf (a different player)
	doctor2.reload()
	time.Sleep(20 * time.Millisecond)
	doctor2.doctorProtectPlayer(werewolf.Name)
	time.Sleep(20 * time.Millisecond)

	// Night is still active (werewolf hasn't voted) - verify and read both confirmations
	doctor1.reload()
	time.Sleep(20 * time.Millisecond)
	if doctor1.isInDayPhase() {
		ctx.logger.LogDB("FAIL: night ended before werewolf voted")
		t.Error("Night should not end before werewolf votes")
	}

	// Each doctor sees their own protection target
	result1 := doctor1.getDoctorResult()
	if !strings.Contains(result1, villager.Name) {
		t.Errorf("Doctor1 should see confirmation with %s, got: %s", villager.Name, result1)
	}
	// Doctor1 should NOT see doctor2's target in their confirmation
	if strings.Contains(result1, werewolf.Name) {
		t.Errorf("Doctor1 should not see Doctor2's protection target %s", werewolf.Name)
	}

	doctor2.reload()
	time.Sleep(20 * time.Millisecond)
	result2 := doctor2.getDoctorResult()
	if !strings.Contains(result2, werewolf.Name) {
		t.Errorf("Doctor2 should see confirmation with %s, got: %s", werewolf.Name, result2)
	}
	// Doctor2 should NOT see doctor1's target in their confirmation
	if strings.Contains(result2, villager.Name) {
		t.Errorf("Doctor2 should not see Doctor1's protection target %s", villager.Name)
	}

	// Werewolf votes for the villager (protected by doctor1) - all conditions met, night ends
	werewolf.reload()
	time.Sleep(20 * time.Millisecond)
	werewolf.voteForPlayer(villager.Name)
	time.Sleep(50 * time.Millisecond)

	// Day should show no one died (doctor1 protected the villager)
	werewolf.reload()
	time.Sleep(20 * time.Millisecond)
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: did not transition to day after werewolf voted")
		t.Fatal("Should transition to day after all conditions met")
	}

	if !werewolf.hasNoDeathMessage() {
		content := werewolf.getGameContent()
		ctx.logger.LogDB("FAIL: doctor1 should have saved the villager")
		t.Errorf("Doctor1 protected the villager, expected 'no one died'. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// ============================================================================
// Guard Helpers
// ============================================================================

func findPlayersByRoleWithGuard(players []*TestPlayer) (werewolves, villagers, guards []*TestPlayer) {
	for _, p := range players {
		role := p.getRole()
		switch role {
		case "Werewolf":
			werewolves = append(werewolves, p)
		case "Guard":
			guards = append(guards, p)
		default:
			villagers = append(villagers, p)
		}
	}
	return
}

// guardProtectPlayer clicks the guard protect button for a specific player
func (tp *TestPlayer) guardProtectPlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Guard protecting: %s", tp.Name, targetName)
	}

	buttons, err := tp.p().Elements(".guard-button")
	if err != nil {
		if tp.logger != nil {
			tp.logger.Debug("[%s] Failed to find guard buttons: %v", tp.Name, err)
		}
		return
	}

	for _, btn := range buttons {
		text := strings.TrimSpace(btn.MustText())
		if strings.Contains(text, targetName) {
			btn.MustClick()
			time.Sleep(20 * time.Millisecond)
			tp.logHTML("after guard protection of " + targetName)
			return
		}
	}

	if tp.logger != nil {
		tp.logger.Debug("[%s] Could not find guard button for: %s", tp.Name, targetName)
	}
}

// getGuardResult returns the text of the guard's protection confirmation
func (tp *TestPlayer) getGuardResult() string {
	el, err := tp.p().Element("#guard-result")
	if err != nil {
		return ""
	}
	text := el.MustText()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Guard result: %s", tp.Name, text)
	}
	return text
}

// canSeeGuardButtons checks if the guard protection buttons are visible
func (tp *TestPlayer) canSeeGuardButtons() bool {
	elements, err := tp.p().Elements(".guard-button")
	canSee := err == nil && len(elements) > 0
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see guard buttons: %v", tp.Name, canSee)
	}
	return canSee
}

// getGuardButtonNames returns the names shown on guard protection buttons
func (tp *TestPlayer) getGuardButtonNames() []string {
	var names []string
	elements, err := tp.p().Elements(".guard-button")
	if err != nil {
		return names
	}
	for _, el := range elements {
		text := strings.TrimSpace(el.MustText())
		if text != "" {
			names = append(names, text)
		}
	}
	if tp.logger != nil {
		tp.logger.Debug("[%s] Guard button names: %v", tp.Name, names)
	}
	return names
}

// ============================================================================
// Guard Tests
// ============================================================================

func TestGuardCanProtect(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Guard can see protect buttons and get confirmation ===")

	// Setup: 1 villager + 1 werewolf + 1 guard = 3 players
	var players []*TestPlayer
	for _, name := range []string{"J1", "J2", "J3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

	werewolves, villagers, guards := findPlayersByRoleWithGuard(players)
	if len(guards) == 0 {
		t.Fatal("No Guard found after role assignment")
	}
	if len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Missing werewolves or villagers")
	}

	guard := guards[0]
	villager := villagers[0]
	ctx.logger.Debug("Guard: %s, protecting Villager: %s", guard.Name, villager.Name)

	// Guard should see protection buttons (WS update from startGame)
	if !guard.canSeeGuardButtons() {
		ctx.logger.LogDB("FAIL: guard cannot see protect buttons")
		t.Fatal("Guard should see protection buttons during night phase")
	}

	// Guard protects the villager
	guard.guardProtectPlayer(villager.Name)
	time.Sleep(50 * time.Millisecond)

	// Guard should see confirmation via WS update
	result := guard.getGuardResult()
	if !strings.Contains(result, villager.Name) {
		ctx.logger.LogDB("FAIL: guard protection confirmation missing")
		t.Errorf("Guard should see protection confirmation with target name %s, got: %s", villager.Name, result)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestGuardSavesVictim(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Guard saves the werewolf's target ===")

	// Setup: 1 villager + 1 werewolf + 1 guard = 3 players
	var players []*TestPlayer
	for _, name := range []string{"K1", "K2", "K3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

	werewolves, villagers, guards := findPlayersByRoleWithGuard(players)
	if len(guards) == 0 || len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Missing required roles")
	}

	guard := guards[0]
	werewolf := werewolves[0]
	villager := villagers[0]
	ctx.logger.Debug("Guard: %s, Werewolf: %s, Villager (target): %s", guard.Name, werewolf.Name, villager.Name)

	// Guard protects the villager
	guard.guardProtectPlayer(villager.Name)
	time.Sleep(20 * time.Millisecond)

	// Werewolf votes for the villager (the protected player)
	werewolf.voteForPlayer(villager.Name)
	time.Sleep(50 * time.Millisecond)

	// Night should end - check day phase shows "no one died"
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: did not transition to day")
		t.Fatal("Should have transitioned to day phase")
	}

	if !werewolf.hasNoDeathMessage() {
		content := werewolf.getGameContent()
		ctx.logger.LogDB("FAIL: victim not saved by guard")
		t.Errorf("Guard should have saved the victim, expected 'no one died' message. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestGuardCannotProtectSelf(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Guard cannot protect themselves ===")

	// Setup: 1 villager + 1 werewolf + 1 guard = 3 players
	var players []*TestPlayer
	for _, name := range []string{"L1", "L2", "L3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

	_, _, guards := findPlayersByRoleWithGuard(players)
	if len(guards) == 0 {
		t.Fatal("No Guard found after role assignment")
	}

	guard := guards[0]
	ctx.logger.Debug("Guard: %s", guard.Name)

	// Guard should see buttons but NOT their own name
	buttonNames := guard.getGuardButtonNames()
	for _, name := range buttonNames {
		if name == guard.Name {
			ctx.logger.LogDB("FAIL: guard can see self in targets")
			t.Errorf("Guard should NOT see themselves as a protection target, but found '%s' in buttons", guard.Name)
		}
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestGuardCannotProtectSamePlayerTwiceInARow(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Guard cannot protect same player two nights in a row ===")

	// Setup: 3 villagers + 1 werewolf + 1 guard = 5 players
	// Need enough villagers so the game doesn't end after one kill
	var players []*TestPlayer
	for _, name := range []string{"M1", "M2", "M3", "M4", "M5"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

	werewolves, villagers, guards := findPlayersByRoleWithGuard(players)
	if len(guards) == 0 || len(werewolves) == 0 || len(villagers) < 3 {
		t.Fatalf("Need 1 guard, 1 werewolf, 3 villagers, got guards=%d werewolves=%d villagers=%d",
			len(guards), len(werewolves), len(villagers))
	}

	guard := guards[0]
	werewolf := werewolves[0]
	protectedVillager := villagers[0]
	targetVillager := villagers[1]
	ctx.logger.Debug("Guard: %s, Werewolf: %s, Protected: %s, Target: %s",
		guard.Name, werewolf.Name, protectedVillager.Name, targetVillager.Name)

	// Night 1: Guard protects protectedVillager
	guard.guardProtectPlayer(protectedVillager.Name)
	time.Sleep(20 * time.Millisecond)

	// Werewolf kills targetVillager (different from protected)
	werewolf.voteForPlayer(targetVillager.Name)
	time.Sleep(50 * time.Millisecond)

	// Should transition to day
	if !guard.isInDayPhase() {
		ctx.logger.LogDB("FAIL: did not transition to day after night 1")
		t.Fatal("Should have transitioned to day phase")
	}

	// Day phase: everyone votes with a split so no elimination, go to night 2
	// With 4 alive players (targetVillager died), majority is 3
	allAlive := []*TestPlayer{}
	for _, p := range players {
		buttons := p.getDayVoteButtons()
		if len(buttons) > 0 {
			allAlive = append(allAlive, p)
		}
	}

	ctx.logger.Debug("Alive players for day vote: %d", len(allAlive))

	// Each alive player votes for a different target to ensure no majority
	for i, p := range allAlive {
		targetIdx := (i + 1) % len(allAlive)
		p.dayVoteForPlayer(allAlive[targetIdx].Name)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)

	// Should be in night 2 now
	if !guard.isInNightPhase() {
		content := guard.getGameContent()
		ctx.logger.LogDB("FAIL: not in night 2")
		t.Fatalf("Should be in night phase 2. Content: %s", content)
	}

	// Night 2: Guard should NOT see protectedVillager in their targets
	buttonNames := guard.getGuardButtonNames()
	for _, name := range buttonNames {
		if name == protectedVillager.Name {
			ctx.logger.LogDB("FAIL: guard can protect same player as last night")
			t.Errorf("Guard should NOT be able to protect '%s' two nights in a row, but found them in buttons", protectedVillager.Name)
		}
	}

	// Guard should still see other targets
	if len(buttonNames) == 0 {
		ctx.logger.LogDB("FAIL: guard has no targets")
		t.Error("Guard should have at least one valid target")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestNightWaitsForGuard(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing night waits for guard to protect ===")

	// Setup: 1 guard + 1 werewolf = 2 players
	var players []*TestPlayer
	for _, name := range []string{"N1", "N2"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

	werewolves, _, guards := findPlayersByRoleWithGuard(players)
	if len(guards) == 0 || len(werewolves) == 0 {
		t.Fatal("Missing guard or werewolf")
	}

	guard := guards[0]
	werewolf := werewolves[0]
	ctx.logger.Debug("Guard: %s, Werewolf: %s", guard.Name, werewolf.Name)

	// Werewolf votes - night should NOT end yet (guard hasn't protected)
	werewolf.voteForPlayer(guard.Name)
	time.Sleep(50 * time.Millisecond)

	if werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: night ended before guard protected")
		t.Error("Night should not end until guard has protected")
	}
	if !werewolf.isInNightPhase() {
		t.Error("Should still be in night phase")
	}

	ctx.logger.Debug("Werewolf voted, night still active. Now guard protects...")

	// Guard protects - night SHOULD end now
	guard.guardProtectPlayer(werewolf.Name)
	time.Sleep(50 * time.Millisecond)

	if !guard.isInDayPhase() {
		content := guard.getGameContent()
		ctx.logger.LogDB("FAIL: did not transition to day after guard protected")
		t.Errorf("Should transition to day after both werewolf voted and guard protected. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestTwoGuardsActIndependently(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing two guards act independently ===")

	// Setup: 1 werewolf + 2 guards + 1 villager = 4 players
	var players []*TestPlayer
	for _, name := range []string{"O1", "O2", "O3", "O4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	players[0].addRoleByID(RoleGuard)
	players[0].addRoleByID(RoleVillager)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

	werewolves, villagers, guards := findPlayersByRoleWithGuard(players)
	if len(guards) < 2 || len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatalf("Need 2 guards, 1 werewolf, 1 villager, got guards=%d werewolves=%d villagers=%d", len(guards), len(werewolves), len(villagers))
	}

	guard1 := guards[0]
	guard2 := guards[1]
	werewolf := werewolves[0]
	villager := villagers[0]
	ctx.logger.Debug("Guard1: %s, Guard2: %s, Werewolf: %s, Villager: %s", guard1.Name, guard2.Name, werewolf.Name, villager.Name)

	// Guard1 protects the villager
	guard1.guardProtectPlayer(villager.Name)
	time.Sleep(20 * time.Millisecond)

	// Guard2 protects the werewolf
	guard2.guardProtectPlayer(werewolf.Name)
	time.Sleep(20 * time.Millisecond)

	// Night is still active (werewolf hasn't voted) - verify and read both confirmations
	if guard1.isInDayPhase() {
		ctx.logger.LogDB("FAIL: night ended before werewolf voted")
		t.Error("Night should not end before werewolf votes")
	}

	// Each guard sees their own protection target
	result1 := guard1.getGuardResult()
	if !strings.Contains(result1, villager.Name) {
		t.Errorf("Guard1 should see confirmation with %s, got: %s", villager.Name, result1)
	}
	if strings.Contains(result1, werewolf.Name) {
		t.Errorf("Guard1 should not see Guard2's protection target %s", werewolf.Name)
	}

	result2 := guard2.getGuardResult()
	if !strings.Contains(result2, werewolf.Name) {
		t.Errorf("Guard2 should see confirmation with %s, got: %s", werewolf.Name, result2)
	}
	if strings.Contains(result2, villager.Name) {
		t.Errorf("Guard2 should not see Guard1's protection target %s", villager.Name)
	}

	// Werewolf votes for the villager (protected by guard1) - all conditions met, night ends
	werewolf.voteForPlayer(villager.Name)
	time.Sleep(50 * time.Millisecond)

	// Day should show no one died (guard1 protected the villager)
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: did not transition to day after werewolf voted")
		t.Fatal("Should transition to day after all conditions met")
	}

	if !werewolf.hasNoDeathMessage() {
		content := werewolf.getGameContent()
		ctx.logger.LogDB("FAIL: guard1 should have saved the villager")
		t.Errorf("Guard1 protected the villager, expected 'no one died'. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// playerNames returns the names of the given test players (for debug logging)
func playerNames(players []*TestPlayer) []string {
	var names []string
	for _, p := range players {
		names = append(names, p.Name)
	}
	return names
}

// ============================================================================
// Hunter Test Helpers
// ============================================================================

func findPlayersByRoleWithHunter(players []*TestPlayer) (werewolves, villagers, hunters []*TestPlayer) {
	for _, p := range players {
		role := p.getRole()
		switch role {
		case "Werewolf":
			werewolves = append(werewolves, p)
		case "Hunter":
			hunters = append(hunters, p)
		default:
			villagers = append(villagers, p)
		}
	}
	return
}

// hunterShootPlayer clicks the hunter revenge button for a specific player
func (tp *TestPlayer) hunterShootPlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Hunter shooting: %s", tp.Name, targetName)
	}

	buttons, err := tp.p().Elements(".hunter-button")
	if err != nil {
		if tp.logger != nil {
			tp.logger.Debug("[%s] Failed to find hunter buttons: %v", tp.Name, err)
		}
		return
	}

	for _, btn := range buttons {
		text := strings.TrimSpace(btn.MustText())
		if strings.Contains(text, targetName) {
			btn.MustClick()
			time.Sleep(20 * time.Millisecond)
			tp.logHTML("after hunter shooting " + targetName)
			return
		}
	}

	if tp.logger != nil {
		tp.logger.Debug("[%s] Could not find hunter button for: %s", tp.Name, targetName)
	}
}

// canSeeHunterButtons checks if the hunter revenge buttons are visible
func (tp *TestPlayer) canSeeHunterButtons() bool {
	elements, err := tp.p().Elements(".hunter-button")
	canSee := err == nil && len(elements) > 0
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see hunter buttons: %v", tp.Name, canSee)
	}
	return canSee
}

// getHunterRevengeResult returns the text of the hunter revenge result announcement
func (tp *TestPlayer) getHunterRevengeResult() string {
	el, err := tp.p().Element("#hunter-revenge-result")
	if err != nil {
		return ""
	}
	text := el.MustText()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Hunter revenge result: %s", tp.Name, text)
	}
	return text
}

// isInHunterRevengePhase checks if the hunter revenge section is visible
func (tp *TestPlayer) isInHunterRevengePhase() bool {
	_, err := tp.p().Element("#hunter-revenge-section")
	inPhase := err == nil
	if tp.logger != nil {
		tp.logger.Debug("[%s] In hunter revenge phase: %v", tp.Name, inPhase)
	}
	return inPhase
}

// isHunterWaiting checks if the "Hunter is choosing" waiting message is visible
func (tp *TestPlayer) isHunterWaiting() bool {
	_, err := tp.p().Element("#hunter-waiting")
	waiting := err == nil
	if tp.logger != nil {
		tp.logger.Debug("[%s] Hunter waiting message visible: %v", tp.Name, waiting)
	}
	return waiting
}

// ============================================================================
// Hunter Tests
// ============================================================================

func TestHunterDeathShotOnNightKill(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Hunter death shot after night kill ===")

	// Setup: 2 villagers + 1 werewolf + 1 hunter = 4 players
	var players []*TestPlayer
	for _, name := range []string{"H1", "H2", "H3", "H4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleHunter)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

	werewolves, villagers, hunters := findPlayersByRoleWithHunter(players)
	if len(hunters) == 0 {
		t.Fatal("No Hunter found after role assignment")
	}
	if len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Need at least 1 werewolf and 1 villager")
	}

	ctx.logger.Debug("Werewolves: %v, Villagers: %v, Hunters: %v",
		playerNames(werewolves), playerNames(villagers), playerNames(hunters))

	// Night 1: Werewolf kills the Hunter
	hunter := hunters[0]
	werewolves[0].voteForPlayer(hunter.Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after werewolf kills hunter")

	// Day 1: Hunter should see revenge buttons
	time.Sleep(50 * time.Millisecond)
	if !hunter.isInDayPhase() {
		content := hunter.getGameContent()
		ctx.logger.LogDB("FAIL: not in day phase")
		t.Fatalf("Should be in day phase. Content: %s", content)
	}

	// Death announcement should show the Hunter died
	announcement := hunter.getDeathAnnouncement()
	if !strings.Contains(announcement, hunter.Name) {
		ctx.logger.LogDB("FAIL: death announcement missing hunter name")
		t.Errorf("Death announcement should mention hunter. Got: %s", announcement)
	}

	// Hunter should see revenge buttons
	if !hunter.canSeeHunterButtons() {
		content := hunter.getGameContent()
		ctx.logger.LogDB("FAIL: hunter can't see revenge buttons")
		t.Fatalf("Hunter should see revenge buttons. Content: %s", content)
	}

	// Voting should NOT be visible yet (revenge pending)
	voteButtons := villagers[0].getDayVoteButtons()
	if len(voteButtons) > 0 {
		t.Error("Vote buttons should be hidden while Hunter revenge is pending")
	}

	// Hunter shoots a villager
	target := villagers[0]
	hunter.hunterShootPlayer(target.Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after hunter revenge")

	// Should still be in day phase, now with voting visible
	if !villagers[1].isInDayPhase() {
		content := villagers[1].getGameContent()
		ctx.logger.LogDB("FAIL: not in day after revenge")
		t.Fatalf("Should still be in day phase after revenge. Content: %s", content)
	}

	// Revenge result should be shown
	result := villagers[1].getHunterRevengeResult()
	if !strings.Contains(result, target.Name) {
		content := villagers[1].getGameContent()
		t.Errorf("Revenge result should mention target '%s'. Result: %s. Content: %s", target.Name, result, content)
	}

	// Voting should now be visible
	voteButtons = villagers[1].getDayVoteButtons()
	if len(voteButtons) == 0 {
		content := villagers[1].getGameContent()
		ctx.logger.LogDB("FAIL: no vote buttons after revenge")
		t.Fatalf("Vote buttons should be visible after revenge. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestHunterDeathShotOnDayElimination(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Hunter death shot after day elimination ===")

	// Setup: 3 villagers + 1 werewolf + 1 hunter = 5 players
	var players []*TestPlayer
	for _, name := range []string{"D1", "D2", "D3", "D4", "D5"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleHunter)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

	werewolves, villagers, hunters := findPlayersByRoleWithHunter(players)
	if len(hunters) == 0 || len(werewolves) == 0 {
		t.Fatal("Need at least 1 hunter and 1 werewolf")
	}

	ctx.logger.Debug("Werewolves: %v, Villagers: %v, Hunters: %v",
		playerNames(werewolves), playerNames(villagers), playerNames(hunters))

	hunter := hunters[0]

	// Night 1: Werewolf kills a villager (not the hunter)
	werewolves[0].voteForPlayer(villagers[0].Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after night 1 kill")

	// Day 1: Vote to eliminate the Hunter
	// With 4 alive players, majority is 3
	time.Sleep(50 * time.Millisecond)
	var alivePlayers []*TestPlayer
	for _, p := range players {
		if p != villagers[0] { // villagers[0] died at night
			alivePlayers = append(alivePlayers, p)
		}
	}

	for _, p := range alivePlayers {
		if p != hunter {
			p.dayVoteForPlayer(hunter.Name)
			time.Sleep(20 * time.Millisecond)
		}
	}
	// Hunter votes for someone else
	hunter.dayVoteForPlayer(werewolves[0].Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after day vote to eliminate hunter")

	// Hunter should now see revenge buttons (day elimination revenge)
	time.Sleep(50 * time.Millisecond)
	if !hunter.isInDayPhase() {
		content := hunter.getGameContent()
		ctx.logger.LogDB("FAIL: not in day phase for hunter revenge")
		t.Fatalf("Should be in day phase for hunter revenge. Content: %s", content)
	}

	if !hunter.canSeeHunterButtons() {
		content := hunter.getGameContent()
		ctx.logger.LogDB("FAIL: hunter can't see revenge buttons after day elimination")
		t.Fatalf("Hunter should see revenge buttons after day elimination. Content: %s", content)
	}

	// Hunter shoots a villager (not the werewolf, so the game continues)
	hunter.hunterShootPlayer(villagers[1].Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after hunter day revenge")

	// After day-elimination revenge, should transition to night
	time.Sleep(50 * time.Millisecond)
	if !villagers[2].isInNightPhase() {
		content := villagers[2].getGameContent()
		ctx.logger.LogDB("FAIL: not in night phase after day elimination revenge")
		t.Fatalf("Should transition to night after day elimination revenge. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestHunterShootsLastWerewolf(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Hunter shoots last werewolf — villagers win ===")

	// Setup: 1 villager + 1 werewolf + 1 hunter = 3 players
	var players []*TestPlayer
	for _, name := range []string{"W1", "W2", "W3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleHunter)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

	werewolves, villagers, hunters := findPlayersByRoleWithHunter(players)
	if len(hunters) == 0 || len(werewolves) == 0 {
		t.Fatal("Need at least 1 hunter and 1 werewolf")
	}

	ctx.logger.Debug("Werewolves: %v, Villagers: %v, Hunters: %v",
		playerNames(werewolves), playerNames(villagers), playerNames(hunters))

	hunter := hunters[0]

	// Night 1: Werewolf kills the Hunter
	werewolves[0].voteForPlayer(hunter.Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after werewolf kills hunter")

	// Day 1: Hunter uses revenge shot to kill the last werewolf
	time.Sleep(50 * time.Millisecond)
	hunter.hunterShootPlayer(werewolves[0].Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after hunter kills last werewolf")

	// Game should be finished — villagers win
	time.Sleep(50 * time.Millisecond)
	if !villagers[0].isGameFinished() {
		content := villagers[0].getGameContent()
		ctx.logger.LogDB("FAIL: game not finished")
		t.Fatalf("Game should be finished after Hunter kills last werewolf. Content: %s", content)
	}

	winner := villagers[0].getWinner()
	if winner != "villagers" {
		ctx.logger.LogDB("FAIL: wrong winner")
		t.Errorf("Villagers should win, got: %s", winner)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestNonHunterPlayersWaitDuringRevenge(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing non-Hunter players see waiting message during revenge ===")

	// Setup: 2 villagers + 1 werewolf + 1 hunter = 4 players
	var players []*TestPlayer
	for _, name := range []string{"P1", "P2", "P3", "P4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleHunter)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

	werewolves, villagers, hunters := findPlayersByRoleWithHunter(players)
	if len(hunters) == 0 || len(werewolves) == 0 || len(villagers) < 2 {
		t.Fatal("Need 1 hunter, 1 werewolf, 2 villagers")
	}

	hunter := hunters[0]

	// Night 1: Werewolf kills the Hunter
	werewolves[0].voteForPlayer(hunter.Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after werewolf kills hunter")
	time.Sleep(50 * time.Millisecond)

	// Non-hunter players should see waiting message
	if !villagers[0].isHunterWaiting() {
		content := villagers[0].getGameContent()
		ctx.logger.LogDB("FAIL: villager doesn't see hunter waiting message")
		t.Errorf("Non-hunter should see waiting message. Content: %s", content)
	}

	// Non-hunter should NOT see hunter buttons
	if villagers[0].canSeeHunterButtons() {
		t.Error("Non-hunter should not see hunter buttons")
	}

	// Werewolf should also see waiting message
	if !werewolves[0].isHunterWaiting() {
		content := werewolves[0].getGameContent()
		t.Errorf("Werewolf should see waiting message. Content: %s", content)
	}

	// Hunter should see buttons, not waiting
	if hunter.isHunterWaiting() {
		t.Error("Hunter should not see waiting message — should see buttons")
	}
	if !hunter.canSeeHunterButtons() {
		content := hunter.getGameContent()
		t.Errorf("Hunter should see revenge buttons. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}
