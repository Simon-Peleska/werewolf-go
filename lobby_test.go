package main

import (
	"fmt"
	"strings"
	"testing"
	"testing/quick"
	"time"
)

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
