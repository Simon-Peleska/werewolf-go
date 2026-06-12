package main

import (
	"strings"
	"testing"
)

// ============================================================================
// Mason Test Helpers
// ============================================================================

func findPlayersByRoleWithMason(players []*TestPlayer) (werewolves, villagers, masons []*TestPlayer) {
	for _, p := range players {
		role := p.getRole()
		switch role {
		case "Werewolf":
			werewolves = append(werewolves, p)
		case "Mason":
			masons = append(masons, p)
		default:
			villagers = append(villagers, p)
		}
	}
	return
}

// canSeeMasonList checks if the player's page shows the mason card list
func (tp *TestPlayer) canSeeMasonList() bool {
	found, _, err := tp.p().Has("#mason-card-list")
	return err == nil && found
}

// canSeeMasonInList checks if a specific player appears as a card in the mason list
func (tp *TestPlayer) canSeeMasonInList(name string) bool {
	found, _, err := tp.p().Has("#mason-card-list .player-card[player-name='" + name + "']")
	return err == nil && found
}

// ============================================================================
// Mason Tests
// ============================================================================

func TestMasonsKnowEachOther(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Test: Masons know each other ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 2 masons + 2 villagers + 2 werewolves = 6 players
	var players []*TestPlayer
	for _, name := range []string{"M1", "M2", "M3", "M4", "M5", "M6"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleMason)
	players[0].addRoleByID(RoleMason)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	werewolves, villagers, masons := findPlayersByRoleWithMason(players)
	ctx.logger.Debug("Werewolves: %v, Villagers: %v, Masons: %v",
		playerNames(werewolves), playerNames(villagers), playerNames(masons))

	if len(masons) < 2 {
		t.Fatalf("Need at least 2 masons, got %d", len(masons))
	}

	mason1 := masons[0]
	mason2 := masons[1]

	// Mason1 should see mason2 in the mason card list
	if !mason1.canSeeMasonList() {
		ctx.logger.LogDB("FAIL: mason1 cannot see mason list")
		t.Errorf("Mason '%s' should see mason list", mason1.Name)
	}
	if !mason1.canSeeMasonInList(mason2.Name) {
		ctx.logger.LogDB("FAIL: mason1 cannot see mason2")
		t.Errorf("Mason '%s' should see fellow mason '%s' in the list", mason1.Name, mason2.Name)
	}

	// Mason2 should see mason1 in the mason card list
	if !mason2.canSeeMasonInList(mason1.Name) {
		ctx.logger.LogDB("FAIL: mason2 cannot see mason1")
		t.Errorf("Mason '%s' should see fellow mason '%s' in the list", mason2.Name, mason1.Name)
	}

	// A regular villager should NOT see mason list
	if len(villagers) > 0 {
		if villagers[0].canSeeMasonList() {
			t.Errorf("Villager '%s' should not see mason list", villagers[0].Name)
		}
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestSingleMasonSeesNoOthers(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Test: Single mason sees no others ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 1 mason + 3 villagers + 2 werewolves = 6 players
	var players []*TestPlayer
	for _, name := range []string{"M1", "M2", "M3", "M4", "M5", "M6"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleMason)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	_, _, masons := findPlayersByRoleWithMason(players)

	if len(masons) == 0 {
		t.Fatal("Mason not found")
	}
	mason := masons[0]

	// Mason should see the "only Mason" message
	content := mason.getGameContent()
	if !strings.Contains(content, "only Mason") {
		ctx.logger.LogDB("FAIL: single mason does not see 'only Mason' message")
		t.Errorf("Single mason should see 'only Mason' message. Content: %s", content)
	}

	// No mason-player list items should be shown
	if mason.canSeeMasonList() {
		t.Errorf("Single mason should not see mason-player list elements")
	}

	ctx.logger.Debug("=== Test passed ===")
}
