package main

import (
	"strings"
	"testing"
	"testing/quick"
)

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
