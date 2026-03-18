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
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("image content-type: want image/png, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, pngBytes) {
		t.Errorf("image body: got %d bytes, want %d bytes", len(body), len(pngBytes))
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
	if got := fetchURL(firstURL); !bytes.Equal(got, redPNG) {
		t.Fatalf("after first upload: got %d bytes, want red PNG (%d bytes)", len(got), len(redPNG))
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
	if got := fetchURL(secondURL); !bytes.Equal(got, bluePNG) {
		t.Fatalf("after second upload: got %d bytes, want blue PNG (%d bytes)", len(got), len(bluePNG))
	}

	ctx.logger.Debug("=== TestProfileImageCanBeChanged passed ===")
}
