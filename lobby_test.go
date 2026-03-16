package main

import (
	"fmt"
	"strings"
	"testing"
)

// ============================================================================
// Lobby Player Count Tests
// ============================================================================

func TestLobbyPlayerCount(t *testing.T) {
	t.Parallel()
	count := 6 // max: int(playerCount%5) + 2

	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing lobby with %d players ===", count)

	var players []*TestPlayer
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("P%d", i+1)
		player := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, player)
	}

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
	}
}

func TestLobbyPlayersLeave(t *testing.T) {
	t.Parallel()
	totalPlayers := 6   // max: int(seed%4) + 3
	leavingPlayers := 2 // max: int(seed%2) + 1

	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing: %d players, %d leaving ===", totalPlayers, leavingPlayers)

	var players []*TestPlayer
	for i := 0; i < totalPlayers; i++ {
		name := fmt.Sprintf("L%d", i+1)
		player := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, player)
	}

	ctx.logger.LogDB("all players joined")

	// Some players leave
	for i := 0; i < leavingPlayers; i++ {
		players[i].disconnect()
	}

	ctx.logger.LogDB("after players left")

	// Check remaining count - wait for disconnect to propagate
	remainingPlayer := players[leavingPlayers]

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
	}
}

func TestLobbyPlayersLeaveAndRejoin(t *testing.T) {
	t.Parallel()
	totalPlayers := 4   // max: int(seed%3) + 2
	leavingPlayers := 2 // max: int(seed%2) + 1

	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing: %d players, %d leave and rejoin ===", totalPlayers, leavingPlayers)

	var players []*TestPlayer
	for i := 0; i < totalPlayers; i++ {
		name := fmt.Sprintf("R%d", i+1)
		player := browser.signupPlayer(ctx.baseURL, name)
		// Get secret code for rejoining
		player.SecretCode = player.getSecretCode()
		players = append(players, player)
	}

	ctx.logger.LogDB("all players joined with secret codes")

	// Some players leave
	for i := 0; i < leavingPlayers; i++ {
		players[i].disconnect()
	}

	ctx.logger.LogDB("after players left")

	// Players rejoin via login
	for i := 0; i < leavingPlayers; i++ {
		name := fmt.Sprintf("R%d", i+1)
		rejoined := browser.loginPlayer(ctx.baseURL, name, players[i].SecretCode)
		players[i] = rejoined
	}

	ctx.logger.LogDB("after players rejoined")

	// Check that all players are back - wait for rejoin to propagate
	playerList := players[leavingPlayers].getPlayerList()

	for i := 0; i < totalPlayers; i++ {
		name := fmt.Sprintf("R%d", i+1)
		if !strings.Contains(playerList, name) {
			ctx.logger.LogDB("FAIL: player not found after rejoin")
			t.Errorf("Player %s not found after rejoin. List: %s", name, playerList)
		}
	}
}

// ============================================================================
// Game Start Tests
// ============================================================================

func TestLobbyCanStartWithMatchingRoles(t *testing.T) {
	t.Parallel()
	v := 3 // max: int(villagers%3) + 1
	w := 2 // max: int(werewolves%2) + 1
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
		players = append(players, player)
	}

	// Add roles (first player adds them)
	for i := 0; i < v; i++ {
		players[0].addRoleByID(RoleVillager)
	}
	for i := 0; i < w; i++ {
		players[0].addRoleByID(RoleWerewolf)
	}

	ctx.logger.LogDB("after adding roles")

	// Check if can start
	canStart := players[0].canStartGame()
	status := players[0].getStatusMessage()

	if !canStart {
		ctx.logger.LogDB("FAIL: cannot start with matching roles")
		t.Errorf("Should be able to start with matching roles. Status: %s", status)
	}

	if !strings.Contains(status, "Ready to start") {
		ctx.logger.LogDB("FAIL: status not ready")
		t.Errorf("Status should indicate ready to start, got: %s", status)
	}
}

func TestLobbyCannotStartWithMismatchedRoles(t *testing.T) {
	t.Parallel()
	v := 3 // max: int(villagers%2) + 2
	w := 3 // max: int(werewolves%2) + 2
	totalPlayers := v + w
	// Add one fewer role than players (mismatch: roles < players)
	roleCount := totalPlayers - 1

	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing mismatch: %d players, %d roles ===", totalPlayers, roleCount)

	// Create players
	var players []*TestPlayer
	for i := 0; i < totalPlayers; i++ {
		name := fmt.Sprintf("M%d", i+1)
		player := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, player)
	}

	// Add fewer roles than players (leave one slot unfilled)
	added := 0
	for i := 0; i < v && added < roleCount; i++ {
		players[0].addRoleByID(RoleVillager)
		added++
	}
	for i := 0; i < w && added < roleCount; i++ {
		players[0].addRoleByID(RoleWerewolf)
		added++
	}

	ctx.logger.LogDB("after adding mismatched roles")

	// Check that start is disabled (roles != players)
	canStart := players[0].canStartGame()

	if canStart {
		ctx.logger.LogDB("FAIL: can start with mismatched roles")
		t.Errorf("Should NOT be able to start with %d roles and %d players",
			roleCount, totalPlayers)
	}
}

func TestGameStartAssignsCorrectRoles(t *testing.T) {
	t.Parallel()
	v := 2 // max: int(villagers%2) + 1
	w := 2 // max: int(werewolves%2) + 1
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
		players = append(players, player)
	}

	// Add roles
	for i := 0; i < v; i++ {
		players[0].addRoleByID(RoleVillager)
	}
	for i := 0; i < w; i++ {
		players[0].addRoleByID(RoleWerewolf)
	}

	// Start the game
	players[0].startGame()

	// Count assigned roles
	roleCounts := make(map[string]int)
	for _, player := range players {
		role := player.getRole()
		roleCounts[role]++
	}

	ctx.logger.Debug("Role counts: %v", roleCounts)

	// Verify counts
	if roleCounts["Villager"] != v {
		ctx.logger.LogDB("FAIL: villager count mismatch")
		t.Errorf("Expected %d villagers, got %d", v, roleCounts["Villager"])
	}
	if roleCounts["Werewolf"] != w {
		ctx.logger.LogDB("FAIL: werewolf count mismatch")
		t.Errorf("Expected %d werewolves, got %d", w, roleCounts["Werewolf"])
	}
}

func TestGameStartWithMixedRoles(t *testing.T) {
	t.Parallel()
	// Max values: Villager 2, Werewolf 2, Seer 1, Doctor 1
	v, w, s, d := 2, 2, 1, 1
	totalPlayers := v + w + s + d

	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing mixed roles: %d villagers, %d werewolves, %d seers, %d doctors ===", v, w, s, d)

	// Create players
	var players []*TestPlayer
	for i := 0; i < totalPlayers; i++ {
		name := fmt.Sprintf("X%d", i+1)
		player := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, player)
	}

	// Add all roles
	for i := 0; i < v; i++ {
		players[0].addRoleByID(RoleVillager)
	}
	for i := 0; i < w; i++ {
		players[0].addRoleByID(RoleWerewolf)
	}
	for i := 0; i < s; i++ {
		players[0].addRoleByID(RoleSeer)
	}
	for i := 0; i < d; i++ {
		players[0].addRoleByID(RoleDoctor)
	}

	ctx.logger.LogDB("after adding mixed roles")

	// Start the game
	players[0].startGame()

	// Count assigned roles
	roleCounts := make(map[string]int)
	for _, player := range players {
		role := player.getRole()
		roleCounts[role]++
	}

	ctx.logger.Debug("Role counts: %v", roleCounts)

	// Verify all role counts
	if roleCounts["Villager"] != v {
		ctx.logger.LogDB("FAIL: villager count mismatch")
		t.Errorf("Villager count mismatch: expected %d, got %d", v, roleCounts["Villager"])
	}
	if roleCounts["Werewolf"] != w {
		ctx.logger.LogDB("FAIL: werewolf count mismatch")
		t.Errorf("Werewolf count mismatch: expected %d, got %d", w, roleCounts["Werewolf"])
	}
	if roleCounts["Seer"] != s {
		ctx.logger.LogDB("FAIL: seer count mismatch")
		t.Errorf("Seer count mismatch: expected %d, got %d", s, roleCounts["Seer"])
	}
	if roleCounts["Doctor"] != d {
		ctx.logger.LogDB("FAIL: doctor count mismatch")
		t.Errorf("Doctor count mismatch: expected %d, got %d", d, roleCounts["Doctor"])
	}
}

// TestMultiGameIsolation verifies that multiple simultaneous games are fully isolated:
// - Players in different games see only their own game's lobby
// - A player can be in two games at the same time
// - Starting one game does not affect the other
func TestMultiGameIsolation(t *testing.T) {
	t.Parallel()

	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Alice joins both games; Bob only alpha; Carol only beta.
	alice1 := browser.signupPlayerInGame(ctx.baseURL, "Alice", "alpha")
	alice1.SecretCode = alice1.getSecretCode()
	alice2 := browser.loginPlayerInGame(ctx.baseURL, "Alice", alice1.SecretCode, "beta")
	bob := browser.signupPlayerInGame(ctx.baseURL, "Bob", "alpha")
	carol := browser.signupPlayerInGame(ctx.baseURL, "Carol", "beta")

	// alpha lobby: Alice + Bob
	alphaList := alice1.getPlayerList()
	if !strings.Contains(alphaList, "Alice") {
		t.Errorf("alpha: expected Alice in player list, got: %s", alphaList)
	}
	if !strings.Contains(alphaList, "Bob") {
		t.Errorf("alpha: expected Bob in player list, got: %s", alphaList)
	}
	if strings.Contains(alphaList, "Carol") {
		t.Errorf("alpha: Carol should not be in player list, got: %s", alphaList)
	}

	// beta lobby: Alice + Carol
	betaList := alice2.getPlayerList()
	if !strings.Contains(betaList, "Alice") {
		t.Errorf("beta: expected Alice in player list, got: %s", betaList)
	}
	if !strings.Contains(betaList, "Carol") {
		t.Errorf("beta: expected Carol in player list, got: %s", betaList)
	}
	if strings.Contains(betaList, "Bob") {
		t.Errorf("beta: Bob should not be in player list, got: %s", betaList)
	}

	// Start game alpha (2 players: 1 werewolf + 1 villager)
	alice1.addRoleByID(RoleWerewolf)
	alice1.addRoleByID(RoleVillager)
	alice1.startGame()

	// After alpha starts, alice1 sees her night/day role — she's no longer in lobby
	alphaRole := alice1.getRole()
	if alphaRole == "" {
		t.Errorf("alpha: Alice should have a role after game starts")
	}

	// Beta is still in lobby — Carol's view should still show the lobby
	betaListAfter := carol.getPlayerList()
	if !strings.Contains(betaListAfter, "Carol") {
		t.Errorf("beta: Carol still in lobby after alpha started, got: %s", betaListAfter)
	}
	if !strings.Contains(betaListAfter, "Alice") {
		t.Errorf("beta: Alice still in beta lobby after alpha started, got: %s", betaListAfter)
	}

	// Carol (beta) should still see the start button (lobby still active), not the game UI
	_, err := carol.p().Element("#btn-start")
	if err != nil {
		t.Errorf("beta: Carol should still see start button (beta still in lobby): %v", err)
	}

	_ = bob
}
