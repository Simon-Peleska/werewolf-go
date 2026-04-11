package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

func makePNG(r, g, b uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: r, G: g, B: b, A: 255})
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func TestSignupWithName(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	name := "SignupUser"
	ctx.logger.Debug("=== Testing signup with name: %s ===", name)

	player := browser.signupPlayer(ctx.baseURL, name)

	if !player.isOnGamePage() {
		ctx.logger.LogDB("FAIL: player not on game page")
		t.Fatalf("Player should be on game page after signup")
	}

	playerList := player.getPlayerList()
	if !strings.Contains(playerList, name) {
		ctx.logger.LogDB("FAIL: player not in list")
		t.Fatalf("Player %s not found in player list: %s", name, playerList)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestSignupDuplicateNameFails(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	name := "DupName"
	ctx.logger.Debug("=== Testing duplicate signup with name: %s ===", name)

	// First signup should succeed
	player1 := browser.signupPlayer(ctx.baseURL, name)

	if !player1.isOnGamePage() {
		ctx.logger.LogDB("FAIL: first player not on game page")
		t.Fatalf("First player should be on game page")
	}

	// Second signup with same name should fail
	player2 := browser.signupPlayerNoRedirect(ctx.baseURL, name)

	if player2.isOnGamePage() {
		ctx.logger.LogDB("FAIL: duplicate signup succeeded")
		t.Fatalf("Duplicate signup should not redirect to game")
	}

	if !player2.hasErrorToast() {
		ctx.logger.LogDB("FAIL: no error toast shown")
		t.Fatalf("Expected error toast for duplicate name")
	}

	ctx.logger.Debug("=== Test passed ===")
}

// ============================================================================
// Unknown / bare-game path redirect tests
// ============================================================================

// TestUnknownPathsRedirectToIndex verifies that bare /game paths and other
// unknown URLs redirect to the login page rather than returning 404.
func TestUnknownPathsRedirectToIndex(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	cases := []struct {
		path        string
		wantURLPath string // expected path after redirect (no query string)
		wantName    string // expected ?name= value, empty if none
	}{
		{"/game", "/", ""},
		{"/game/", "/", ""},
		{"/game?name=Alice", "/", "Alice"},
		{"/game/?name=Alice", "/", "Alice"},
		{"/some/unknown/path", "/", ""},
		{"/doesnotexist", "/", ""},
		{"/test?name=Bob", "/", "Bob"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			page := browser.newIncognitoPage(ctx.baseURL + tc.path)
			info, err := page.Info()
			if err != nil {
				t.Fatalf("Could not get page info for path %q: %v", tc.path, err)
			}

			// Check the path portion ends at "/".
			gotPath := strings.Split(strings.TrimPrefix(info.URL, ctx.baseURL), "?")[0]
			if gotPath != tc.wantURLPath {
				t.Errorf("path %q: want URL path %q, got %q (full URL: %s)", tc.path, tc.wantURLPath, gotPath, info.URL)
			}

			// Check ?name= is forwarded when expected.
			if tc.wantName != "" {
				if !strings.Contains(info.URL, "name="+tc.wantName) {
					t.Errorf("path %q: expected ?name=%s in URL, got: %s", tc.path, tc.wantName, info.URL)
				}
				// Also verify the login name input is pre-filled.
				p := page.Timeout(browserTimeout)
				el, err := p.Element("#login-name")
				if err != nil {
					t.Fatalf("path %q: could not find #login-name: %v", tc.path, err)
				}
				val, _ := el.Attribute("value")
				if val == nil || *val != tc.wantName {
					got := ""
					if val != nil {
						got = *val
					}
					t.Errorf("path %q: expected #login-name value=%q, got=%q", tc.path, tc.wantName, got)
				}
			}
		})
	}
}

// TestUnknownPathWithNameLogsOutDifferentUser verifies that a logged-in user
// visiting an unknown path with ?name=<other> is logged out before the redirect.
func TestUnknownPathWithNameLogsOutDifferentUser(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Sign up a player first.
	player := browser.signupPlayer(ctx.baseURL, "LoggedInUser")

	// Visit an unknown path with a different ?name= while logged in.
	wait := player.Page.WaitNavigation(proto.PageLifecycleEventNameLoad)
	player.Page.Navigate(ctx.baseURL + "/test?name=OtherUser")
	wait()

	// Should land on the index page.
	if !player.isOnIndexPage() {
		info, _ := player.Page.Info()
		t.Fatalf("Expected redirect to index, got: %s", info.URL)
	}

	// Session must be cleared — navigating to the game page should redirect back to login.
	wait = player.Page.WaitNavigation(proto.PageLifecycleEventNameLoad)
	player.Page.Navigate(ctx.baseURL + "/game/test-game")
	wait()

	if !player.isOnIndexPage() {
		t.Fatal("Expected session to be cleared: game page should redirect to login")
	}
}

// TestUnknownPathWithSameNameKeepsSession verifies that a logged-in user
// visiting an unknown path with their own ?name= does NOT get logged out.
func TestUnknownPathWithSameNameKeepsSession(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	playerName := "SameNameUser"
	player := browser.signupPlayer(ctx.baseURL, playerName)
	secretCode := player.getSecretCode()

	// Visit an unknown path with the same name as the logged-in user.
	wait := player.Page.WaitNavigation(proto.PageLifecycleEventNameLoad)
	player.Page.Navigate(ctx.baseURL + "/test?name=" + playerName)
	wait()

	// Should land on the index page.
	if !player.isOnIndexPage() {
		info, _ := player.Page.Info()
		t.Fatalf("Expected redirect to index, got: %s", info.URL)
	}

	// Session must still be valid — navigating to the game page should succeed.
	wait = player.Page.WaitNavigation(proto.PageLifecycleEventNameLoad)
	player.Page.Navigate(ctx.baseURL + "/game/test-game")
	wait()

	if !player.isOnGamePage() {
		t.Fatal("Expected session to be intact: game page should load without redirect")
	}

	// Secret code still visible confirms it's the same session.
	if code := player.getSecretCode(); code != secretCode {
		t.Fatalf("Secret code changed — session was incorrectly replaced: before=%q after=%q", secretCode, code)
	}
}

// ============================================================================
// Game redirect tests
// ============================================================================

// TestUnauthenticatedGameRedirectsToLogin verifies that visiting /game/<name>
// without a session redirects to /?game=<name>.
func TestUnauthenticatedGameRedirectsToLogin(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	gameName := "redirect-test-game"
	ctx.logger.Debug("=== Testing unauthenticated redirect for game: %s ===", gameName)

	// Navigate directly to the game page without being logged in.
	page := browser.newIncognitoPage(ctx.baseURL + "/game/" + gameName)
	player := &TestPlayer{Name: "anon", Page: page, logger: ctx.logger, t: t}

	// Should have landed on the index page (redirected away from /game).
	if !player.isOnIndexPage() {
		t.Fatalf("Expected redirect to index page, but URL is: %v", func() string {
			info, _ := page.Info()
			return info.URL
		}())
	}

	// The URL should contain the game parameter.
	info, err := page.Info()
	if err != nil {
		t.Fatalf("Could not get page info: %v", err)
	}
	if !strings.Contains(info.URL, "game="+gameName) {
		t.Fatalf("Expected URL to contain game=%s, got: %s", gameName, info.URL)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestGameParamPrefillsSignupForm verifies that the game name from the URL
// parameter is pre-filled in the signup form on the index page.
func TestGameParamPrefillsSignupForm(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	gameName := "my-prefill-game"
	ctx.logger.Debug("=== Testing signup form prefill for game: %s ===", gameName)

	page := browser.newIncognitoPage(ctx.baseURL + "/?game=" + gameName)
	p := page.Timeout(browserTimeout)

	el, err := p.Element("#signup-game-name")
	if err != nil {
		t.Fatalf("Could not find signup game name input: %v", err)
	}
	val, err := el.Attribute("value")
	if err != nil || val == nil || *val != gameName {
		got := ""
		if val != nil {
			got = *val
		}
		t.Fatalf("Expected signup game name input value=%q, got=%q", gameName, got)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestGameParamPrefillsLoginForm verifies that the game name from the URL
// parameter is pre-filled in the login (returning player) form on the index page.
func TestGameParamPrefillsLoginForm(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	gameName := "login-prefill-game"
	ctx.logger.Debug("=== Testing login form prefill for game: %s ===", gameName)

	page := browser.newIncognitoPage(ctx.baseURL + "/?game=" + gameName)
	p := page.Timeout(browserTimeout)

	el, err := p.Element("#login-game-name")
	if err != nil {
		t.Fatalf("Could not find login game name input: %v", err)
	}
	val, err := el.Attribute("value")
	if err != nil || val == nil || *val != gameName {
		got := ""
		if val != nil {
			got = *val
		}
		t.Fatalf("Expected login game name input value=%q, got=%q", gameName, got)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestUnauthenticatedGameRedirectAndSignup is an end-to-end test: a user visits
// a game URL without being logged in, gets redirected with the game name
// pre-filled, fills their name, and successfully joins the game.
func TestUnauthenticatedGameRedirectAndSignup(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	gameName := "redirect-signup-game"
	playerName := "RedirectUser"
	ctx.logger.Debug("=== Testing redirect-then-signup flow for game: %s ===", gameName)

	// Navigate directly to a game page without a session.
	page := browser.newIncognitoPage(ctx.baseURL + "/game/" + gameName)
	player := &TestPlayer{Name: playerName, Page: page, logger: ctx.logger, t: t}

	// Confirm we're on the login page.
	if !player.isOnIndexPage() {
		t.Fatal("Expected redirect to index page")
	}

	// The game name input should already be filled from the ?game= parameter.
	p := page.Timeout(browserTimeout)
	gameNameEl, err := p.Element("#signup-game-name")
	if err != nil {
		t.Fatalf("Could not find signup game name input: %v", err)
	}
	val, _ := gameNameEl.Attribute("value")
	if val == nil || *val != gameName {
		got := ""
		if val != nil {
			got = *val
		}
		t.Fatalf("Expected signup game name pre-filled with %q, got %q", gameName, got)
	}

	// Fill in the player name and submit.
	nameEl, err := p.Element("#signup-name")
	if err != nil {
		t.Fatalf("Could not find signup name input: %v", err)
	}
	nameEl.Input(playerName)

	wait := page.WaitNavigation(proto.PageLifecycleEventNameLoad)
	p.MustElement("#btn-signup").Click(proto.InputMouseButtonLeft, 1)
	wait()

	// Should now be on the game page.
	if !player.isOnGamePage() {
		t.Fatal("Expected to be on game page after signup")
	}

	// Player should appear in the lobby list.
	if err := player.waitUntilCondition(`() => {
		const cards = document.querySelectorAll('#player-list player-card');
		return Array.from(cards).some(c => c.getAttribute('player-name') === '`+playerName+`');
	}`, "player appears in lobby list"); err != nil {
		t.Fatalf("Player not found in lobby list: %v", err)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// ============================================================================
// Login Tests
// ============================================================================

func TestLoginWithCorrectSecret(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	name := "LoginUser"
	ctx.logger.Debug("=== Testing login with name: %s ===", name)

	// Signup first
	player1 := browser.signupPlayer(ctx.baseURL, name)

	// Get secret code
	secretCode := player1.getSecretCode()
	if secretCode == "" {
		t.Fatal("Could not find secret code")
	}

	// Login on a new page
	player2 := browser.loginPlayer(ctx.baseURL, name, secretCode)

	if !player2.isOnGamePage() {
		ctx.logger.LogDB("FAIL: login did not redirect to game")
		t.Fatalf("Login should redirect to game page")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestLoginWithWrongSecret(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	name := "WrongSecret"
	ctx.logger.Debug("=== Testing login with wrong secret for: %s ===", name)

	// Signup first
	_ = browser.signupPlayer(ctx.baseURL, name)

	// Try to login with wrong secret
	player2 := browser.loginPlayerNoRedirect(ctx.baseURL, name, "wrongsecret")

	if player2.isOnGamePage() {
		ctx.logger.LogDB("FAIL: login with wrong secret succeeded")
		t.Fatalf("Login with wrong secret should not redirect to game")
	}

	if !player2.hasErrorToast() {
		ctx.logger.LogDB("FAIL: no error toast for wrong secret")
		t.Fatalf("Expected error toast for wrong secret")
	}

	ctx.logger.Debug("=== Test passed ===")
}

// ============================================================================
// Auto-join link tests (?name= parameter)
// ============================================================================

// TestAutoJoinNewPlayerCreatesAccountAndJoins verifies that visiting
// /game/<name>?name=<player> when the player doesn't exist auto-creates them
// and lands directly on the game page.
func TestAutoJoinNewPlayerCreatesAccountAndJoins(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	gameName := "autojoin-new-game"
	playerName := "AutoNewPlayer"
	ctx.logger.Debug("=== Testing auto-join for new player: %s ===", playerName)

	page := browser.newIncognitoPage(ctx.baseURL + "/game/" + gameName + "?name=" + playerName)
	player := &TestPlayer{Name: playerName, Page: page, logger: ctx.logger, t: t}

	// Should be on the game page, not the index.
	if !player.isOnGamePage() {
		info, _ := page.Info()
		t.Fatalf("Expected auto-join to land on game page, got URL: %s", info.URL)
	}

	// The player should exist in the database.
	var count int
	if err := ctx.app.db.Get(&count, "SELECT COUNT(*) FROM player WHERE name = ?", playerName); err != nil || count == 0 {
		t.Fatalf("Expected player '%s' to be created in DB", playerName)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestAutoJoinExistingPlayerRedirectsToLoginWithNameFilled verifies that
// visiting /game/<name>?name=<player> when the player already exists redirects
// to the login page with both game and name pre-filled.
func TestAutoJoinExistingPlayerRedirectsToLoginWithNameFilled(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	gameName := "autojoin-existing-game"
	playerName := "ExistingPlayer"
	ctx.logger.Debug("=== Testing auto-join for existing player: %s ===", playerName)

	// Create the player first via normal signup.
	_ = browser.signupPlayerInGame(ctx.baseURL, playerName, gameName)

	// Now visit the auto-join link in a fresh incognito window.
	page := browser.newIncognitoPage(ctx.baseURL + "/game/" + gameName + "?name=" + playerName)
	player := &TestPlayer{Name: playerName, Page: page, logger: ctx.logger, t: t}

	// Should be on the index page (login required — player exists).
	if !player.isOnIndexPage() {
		info, _ := page.Info()
		t.Fatalf("Expected redirect to login page, got URL: %s", info.URL)
	}

	p := page.Timeout(browserTimeout)

	// Login game name field should be pre-filled.
	gameNameEl, err := p.Element("#login-game-name")
	if err != nil {
		t.Fatalf("Could not find login game name input: %v", err)
	}
	gameVal, _ := gameNameEl.Attribute("value")
	if gameVal == nil || *gameVal != gameName {
		got := ""
		if gameVal != nil {
			got = *gameVal
		}
		t.Fatalf("Expected login game name pre-filled with %q, got %q", gameName, got)
	}

	// Login name field should be pre-filled.
	nameEl, err := p.Element("#login-name")
	if err != nil {
		t.Fatalf("Could not find login name input: %v", err)
	}
	nameVal, _ := nameEl.Attribute("value")
	if nameVal == nil || *nameVal != playerName {
		got := ""
		if nameVal != nil {
			got = *nameVal
		}
		t.Fatalf("Expected login name pre-filled with %q, got %q", playerName, got)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestAutoJoinLogsOutExistingSession verifies that a currently-logged-in user
// is logged out when they visit a /game/<name>?name= link for a new player,
// and the new player gets created and logged in instead.
func TestAutoJoinLogsOutExistingSession(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	gameName := "autojoin-logout-game"
	originalName := "OriginalPlayer"
	newName := "SwitchPlayer"
	ctx.logger.Debug("=== Testing auto-join clears existing session: %s -> %s ===", originalName, newName)

	// Sign up the original player.
	original := browser.signupPlayerInGame(ctx.baseURL, originalName, gameName)

	// Navigate the same page (same session) to the auto-join link for a new player.
	wait := original.Page.WaitNavigation(proto.PageLifecycleEventNameLoad)
	original.Page.Navigate(ctx.baseURL + "/game/" + gameName + "?name=" + newName)
	wait()

	updated := &TestPlayer{Name: newName, Page: original.Page, logger: ctx.logger, t: t}

	// Should be on the game page as the new player.
	if !updated.isOnGamePage() {
		info, _ := original.Page.Info()
		t.Fatalf("Expected game page after auto-join, got: %s", info.URL)
	}

	// The new player should exist in the database.
	var count int
	if err := ctx.app.db.Get(&count, "SELECT COUNT(*) FROM player WHERE name = ?", newName); err != nil || count == 0 {
		t.Fatalf("Expected new player '%s' to be created in DB", newName)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestAutoJoinSameUserKeepsSession verifies that following a ?name= link as the
// player with that exact name does NOT log them out — it just drops the param
// and continues to the game page with the existing session intact.
func TestAutoJoinSameUserKeepsSession(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	gameName := "autojoin-same-game"
	playerName := "SamePlayer"
	ctx.logger.Debug("=== Testing auto-join same user keeps session: %s ===", playerName)

	// Sign up the player normally.
	player := browser.signupPlayerInGame(ctx.baseURL, playerName, gameName)

	secretCode := player.getSecretCode()
	if secretCode == "" {
		t.Fatal("Could not read secret code before auto-join")
	}

	// Navigate to the auto-join link for the same name they're already logged in as.
	wait := player.Page.WaitNavigation(proto.PageLifecycleEventNameLoad)
	player.Page.Navigate(ctx.baseURL + "/game/" + gameName + "?name=" + playerName)
	wait()

	// Should still be on the game page.
	if !player.isOnGamePage() {
		info, _ := player.Page.Info()
		t.Fatalf("Expected to stay on game page, got: %s", info.URL)
	}

	// Session must still be valid — secret code is still visible.
	codeAfter := player.getSecretCode()
	if codeAfter == "" {
		t.Fatal("Secret code missing after auto-join — session was incorrectly cleared")
	}
	if codeAfter != secretCode {
		t.Fatalf("Secret code changed after auto-join: before=%q after=%q", secretCode, codeAfter)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestProfileImageUploadAndDisplay(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	uploader := browser.signupPlayer(ctx.baseURL, "ImgUploader")
	watcher := browser.signupPlayer(ctx.baseURL, "ImgWatcher")

	var uploaderID int64
	if err := ctx.app.db.Get(&uploaderID, "SELECT rowid FROM player WHERE name = 'ImgUploader'"); err != nil {
		t.Fatalf("could not get uploader player ID: %v", err)
	}

	pngBytes := makePNG(255, 0, 0) // 1×1 red PNG

	uploader.uploadProfileViaUI(pngBytes)

	// Wait for the uploader's own card to receive the updated profile-image attribute.
	if err := uploader.waitUntilCondition(`() => {
		const card = document.querySelector('#sidebar-role-card');
		return card && card.getAttribute('profile-image') !== '';
	}`, "uploader's own card has profile-image attribute"); err != nil {
		t.Fatalf("uploader's card did not get profile-image: %v\n%s", err, uploader.dumpElement("#player-list"))
	}

	// DB: profile_image_id is set on the player.
	var profileImageID *int64
	if err := ctx.app.db.Get(&profileImageID, "SELECT profile_image_id FROM player WHERE rowid = ?", uploaderID); err != nil || profileImageID == nil {
		t.Fatalf("expected profile_image_id to be set on player, err: %v", err)
	}

	imgURL := fmt.Sprintf("/player-image/%d", *profileImageID)

	// Correct URL on the uploader's own card.
	attrResult, err := uploader.p().Eval(`() => document.querySelector('#sidebar-role-card')?.getAttribute('profile-image') ?? ''`)
	if err != nil {
		t.Fatalf("eval profile-image attr: %v", err)
	}
	if got := attrResult.Value.String(); got != imgURL {
		t.Errorf("profile-image attr: want %q, got %q", imgURL, got)
	}

	// Watcher's sidebar also shows the profile-image on the uploader's card.
	if err := watcher.waitUntilCondition(`() => {
		const cards = document.querySelectorAll('#player-list player-card');
		return Array.from(cards).some(c =>
			c.getAttribute('player-name') === 'ImgUploader' &&
			c.getAttribute('profile-image') !== ''
		);
	}`, "watcher sees uploader's profile-image"); err != nil {
		t.Fatalf("watcher did not see uploader's profile-image: %v\n%s", err, watcher.dumpElement("#player-list"))
	}

	// Image endpoint serves the correct bytes with the right content-type.
	resp, err := http.Get(ctx.baseURL + imgURL)
	if err != nil {
		t.Fatalf("GET %s: %v", imgURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("image endpoint: want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("image content-type: want image/jpeg, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("image body is empty")
	}

	// Shadow DOM: the seal img src inside the uploader's own card points to the profile image.
	sealSrcResult, err := uploader.p().Eval(`() => {
		const card = document.querySelector('#sidebar-role-card');
		if (!card || !card.shadowRoot) return '';
		return card.shadowRoot.querySelector('.pc-seal')?.src ?? '';
	}`)
	if err != nil {
		t.Fatalf("eval seal src: %v", err)
	}
	if src := sealSrcResult.Value.String(); !strings.Contains(src, "/player-image/") {
		t.Errorf("seal should show profile image, got src: %q", src)
	}

	ctx.logger.Debug("=== TestProfileImageUploadAndDisplay passed ===")
}

func TestProfileImageCanBeChanged(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	player := browser.signupPlayer(ctx.baseURL, "ImgChanger")

	redPNG := makePNG(255, 0, 0)
	bluePNG := makePNG(0, 0, 255)

	getProfileImageURL := func() string {
		t.Helper()
		res, err := player.p().Eval(`() => document.querySelector('#sidebar-role-card')?.getAttribute('profile-image') ?? ''`)
		if err != nil {
			t.Fatalf("reading profile-image attr: %v", err)
		}
		return res.Value.String()
	}

	fetchURL := func(path string) []byte {
		t.Helper()
		resp, err := http.Get(ctx.baseURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("image endpoint: want 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		return body
	}

	// First upload: red PNG.
	player.uploadProfileViaUI(redPNG)
	if err := player.waitUntilCondition(`() => {
		const card = document.querySelector('#sidebar-role-card');
		return card && card.getAttribute('profile-image') !== '';
	}`, "card has profile-image after first upload"); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	firstURL := getProfileImageURL()
	if got := fetchURL(firstURL); len(got) == 0 {
		t.Fatalf("after first upload: empty response body")
	}

	// Second upload: blue PNG. The URL must change (new image rowid) so all clients re-fetch.
	player.uploadProfileViaUI(bluePNG)
	if err := player.waitUntilCondition(`() => {
		const url = document.querySelector('#sidebar-role-card')?.getAttribute('profile-image') ?? '';
		return url !== '' && url !== '`+firstURL+`';
	}`, "card profile-image URL changed after second upload"); err != nil {
		t.Fatalf("second upload: %v", err)
	}
	secondURL := getProfileImageURL()
	if got := fetchURL(secondURL); len(got) == 0 {
		t.Fatalf("after second upload: empty response body")
	}

	ctx.logger.Debug("=== TestProfileImageCanBeChanged passed ===")
}
