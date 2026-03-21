package main

import (
	"os"
	"strings"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// TestMain launches a single shared Chromium browser for the entire test suite,
// then runs all tests, then closes the browser. This avoids per-test browser
// startup overhead and prevents resource exhaustion on long sequential runs.
func TestMain(m *testing.M) {
	path, found := launcher.LookPath()
	if found {
		u, err := launcher.New().
			Bin(path).
			Headless(true).
			// Wide viewport so the sidebar is always visible (breakpoint is 1280px).
			Set("window-size", "1920,1080").
			// Allow AudioContext to start without a user gesture so TTS audio tests work.
			Set("autoplay-policy", "no-user-gesture-required").
			// Prevent Chrome from throttling background tabs when many tests run in parallel.
			// Without these flags, JS event loops in background tabs can be suspended for
			// 30–60 s under heavy load, causing WS message handlers to never fire.
			Set("disable-background-timer-throttling", "").
			Set("disable-backgrounding-occluded-windows", "").
			Set("disable-renderer-backgrounding", "").
			Set("mute-audio", "").
			Launch()
		if err == nil {
			b := rod.New().ControlURL(u)
			if err := b.Connect(); err == nil {
				sharedBrowser = b
			}
		}
	}
	code := m.Run()
	if sharedBrowser != nil {
		sharedBrowser.MustClose()
	}
	os.Exit(code)
}

func TestWebSocketSync(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing WebSocket synchronization ===")

	// Player 1 signs up
	player1 := browser.signupPlayer(ctx.baseURL, "Alice")
	ctx.logger.Debug("Player 1 (Alice) in game")

	// Player 2 signs up
	player2 := browser.signupPlayer(ctx.baseURL, "Bob")
	ctx.logger.Debug("Player 2 (Bob) in game")

	// Wait for WebSocket connections
	ctx.logger.LogDB("both players connected")

	// Verify both players see each other
	playerList := player1.getPlayerList()
	if !strings.Contains(playerList, "Alice") || !strings.Contains(playerList, "Bob") {
		ctx.logger.LogDB("FAIL: players don't see each other")
		t.Errorf("Should see both players, got: %s", playerList)
	}

	// Player 1 adds a Werewolf role
	player1.addRoleByID(RoleWerewolf)

	// Check if player 2 sees the update
	count2 := player2.getRoleCountByID(RoleWerewolf)
	if count2 != "1" {
		ctx.logger.LogDB("FAIL: player 2 didn't see role update")
		t.Errorf("Player 2 werewolf count expected '1', got '%s'", count2)
	}

	// Add villager
	player1.addRoleByID(RoleVillager)

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
