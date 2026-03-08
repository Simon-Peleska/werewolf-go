package main

import (
	"strings"
	"testing"
)

func TestSignupWithName(t *testing.T) {
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
