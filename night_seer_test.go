package main

import (
	"strings"
	"testing"
)

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

// seerInvestigatePlayer selects a target for the seer and clicks the Investigate button.
func (tp *TestPlayer) seerInvestigatePlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Seer selecting target: %s", tp.Name, targetName)
	}
	// Select the player — use JS click to avoid scroll-triggered CSS transition layout shifts
	tp.clickAndWait("[id^='seer-select-form-'] .player-card[player-name='" + targetName + "']")
	tp.logHTML("after seer select of " + targetName)
	// Click Investigate button to commit
	tp.clickAndWait("#seer-investigate-button")
	tp.logHTML("after seer investigation of " + targetName)
}

// getSeerResult returns a description of the seer's investigation result for the current night.
// Reads from .player-card attributes (shadow DOM text is not accessible via MustText).
// Returns a string like "Alice is a Werewolf" or "Bob is Not a Werewolf".
func (tp *TestPlayer) getSeerResult() string {
	found, el, err := tp.p().Has(".player-card#seer-result")
	if err != nil || !found {
		return ""
	}
	name, _ := el.Attribute("player-name")
	team, _ := el.Attribute("team")
	if name == nil {
		return ""
	}
	var result string
	if team != nil && *team == "werewolf" {
		result = *name + " is a Werewolf"
	} else {
		result = *name + " is Not a Werewolf"
	}
	if tp.logger != nil {
		tp.logger.Debug("[%s] Seer result: %s", tp.Name, result)
	}
	return result
}

// canSeeSeerButtons checks if the seer investigation cards are visible
func (tp *TestPlayer) canSeeSeerButtons() bool {
	found, _, err := tp.p().Has("[id^='seer-select-form-'] .player-card")
	canSee := err == nil && found
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see seer buttons: %v", tp.Name, canSee)
	}
	return canSee
}

// ============================================================================
// Seer Tests
// ============================================================================

func TestSeerCanInvestigateVillager(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Seer can investigate villager ===")

	// Setup: 1 villager + 1 werewolf + 1 seer = 3 players
	var players []*TestPlayer
	for i, name := range []string{"A1", "A2", "A3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
		_ = i
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleSeer)
	players[0].startGame()

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
	if !seer.canSeeSeerButtons() {
		ctx.logger.LogDB("FAIL: seer cannot see investigate buttons")
		t.Fatal("Seer should see investigation buttons during night phase")
	}

	// Seer investigates the villager
	seer.seerInvestigatePlayer(villager.Name)

	// Seer should see result showing "Not a Werewolf"
	result := seer.getSeerResult()
	if !strings.Contains(result, "Not a Werewolf") {
		ctx.logger.LogDB("FAIL: seer result incorrect")
		t.Errorf("Seer investigating villager should see 'Not a Werewolf', got: %s", result)
	}
	if !strings.Contains(result, villager.Name) {
		t.Errorf("Seer result should mention target name %s, got: %s", villager.Name, result)
	}

	// Seer should receive a toast confirming the target is not a werewolf
	if !seer.hasToastWithText(villager.Name + " is not a werewolf") {
		ctx.logger.LogDB("FAIL: seer did not receive villager investigation toast")
		t.Errorf("Seer should see a toast confirming %s is not a werewolf", villager.Name)
	}

	// Sidebar: investigated villager should NOT get the wolf indicator
	villagerID := villager.getPlayerID()
	if seer.isShownAsWerewolf(villagerID) {
		ctx.logger.LogDB("FAIL: seer sees wolf indicator for investigated villager")
		t.Errorf("Seer should not see 🐺 next to %s (villager)", villager.Name)
	}
	// Actual werewolf should also not be flagged (not yet investigated)
	werewolfID := werewolves[0].getPlayerID()
	if seer.isShownAsWerewolf(werewolfID) {
		ctx.logger.LogDB("FAIL: seer sees wolf indicator for uninvestigated werewolf")
		t.Errorf("Seer should not see 🐺 next to %s before investigating them", werewolves[0].Name)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestSeerCanInvestigateWerewolf(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Seer can investigate werewolf ===")

	// Setup: 1 villager + 1 werewolf + 1 seer
	var players []*TestPlayer
	for _, name := range []string{"B1", "B2", "B3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleSeer)
	players[0].startGame()

	werewolves, villagers, seers := findPlayersByRoleExtended(players)
	if len(seers) == 0 || len(werewolves) == 0 {
		t.Fatal("Missing seer or werewolf")
	}

	seer := seers[0]
	werewolf := werewolves[0]
	ctx.logger.Debug("Seer: %s, investigating Werewolf: %s", seer.Name, werewolf.Name)

	// Seer investigates the werewolf
	seer.seerInvestigatePlayer(werewolf.Name)

	// Seer should see result showing "a Werewolf!"
	result := seer.getSeerResult()
	if !strings.Contains(result, "Werewolf") {
		ctx.logger.LogDB("FAIL: seer result did not identify werewolf")
		t.Errorf("Seer investigating werewolf should see 'a Werewolf!', got: %s", result)
	}
	if !strings.Contains(result, werewolf.Name) {
		t.Errorf("Seer result should mention target name %s, got: %s", werewolf.Name, result)
	}

	// Seer should receive a toast with the investigation result
	if !seer.hasToastWithText(werewolf.Name + " is a werewolf") {
		ctx.logger.LogDB("FAIL: seer did not receive werewolf investigation toast")
		t.Errorf("Seer should see a toast identifying %s as a werewolf", werewolf.Name)
	}

	// Sidebar: seer should see the wolf indicator for the investigated werewolf
	werewolfID := werewolf.getPlayerID()
	if !seer.isShownAsWerewolf(werewolfID) {
		ctx.logger.LogDB("FAIL: seer does not see wolf indicator for investigated werewolf")
		t.Errorf("Seer should see 🐺 next to %s after investigation", werewolf.Name)
	}
	// Other players should not see the wolf indicator (no investigation, not a werewolf themselves)
	if len(villagers) > 0 && villagers[0].isShownAsWerewolf(werewolfID) {
		ctx.logger.LogDB("FAIL: villager sees wolf indicator without investigation")
		t.Errorf("Villager should not see 🐺 next to %s", werewolf.Name)
	}

	// History: seer investigation is actor-only — seer sees it, others do not
	investigateEntry := "You investigated " + werewolf.Name
	if !seer.historyContains(investigateEntry) {
		ctx.logger.LogDB("FAIL: seer cannot see own investigation in history")
		t.Errorf("Seer should see their investigation in history, got: %s", seer.getHistoryText())
	}
	if werewolf.historyContains(investigateEntry) {
		ctx.logger.LogDB("FAIL: werewolf can see seer investigation in history")
		t.Errorf("Werewolf should not see seer investigation in history")
	}
	if len(villagers) > 0 && villagers[0].historyContains(investigateEntry) {
		ctx.logger.LogDB("FAIL: villager can see seer investigation in history")
		t.Errorf("Villager should not see seer investigation in history")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestNightWaitsForSeer(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing night waits for seer to investigate ===")

	// Setup: 1 seer + 1 werewolf (2 players, werewolf needs to vote AND seer must investigate)
	var players []*TestPlayer
	for _, name := range []string{"C1", "C2", "C3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleSeer)
	players[0].addRoleByID(RoleVillager)
	players[0].startGame()

	// Wait for all players to reach night phase
	for _, p := range players {
		err := p.waitForNightPhase()
		if err != nil {
			ctx.logger.Debug("Warning: timeout waiting for night phase on %s: %v", p.Name, err)
		}
	}

	werewolves, _, seers := findPlayersByRoleExtended(players)
	if len(seers) == 0 || len(werewolves) == 0 {
		t.Fatal("Missing seer or werewolf")
	}

	seer := seers[0]
	werewolf := werewolves[0]
	ctx.logger.Debug("Seer: %s, Werewolf: %s", seer.Name, werewolf.Name)

	// Werewolf votes - night should NOT end yet (seer hasn't investigated)
	werewolf.voteForPlayer(seer.Name)

	if werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: night ended before seer investigated")
		t.Error("Night should not end until seer has investigated")
	}
	if !werewolf.isInNightPhase() {
		t.Error("Should still be in night phase")
	}

	ctx.logger.Debug("Werewolf voted, night still active. Now seer investigates...")

	// Seer investigates - night SHOULD end now
	seer.seerInvestigatePlayer(werewolf.Name)

	submitNightSurveysForAllPlayers(players)

	// Wait for phase transition to day
	err := seer.waitForDayPhase()
	if err != nil {
		ctx.logger.Debug("Warning: timeout waiting for day phase: %v", err)
	}

	if !seer.isInDayPhase() {
		content := seer.getGameContent()
		ctx.logger.LogDB("FAIL: did not transition to day after seer investigated")
		t.Errorf("Should transition to day after both werewolf voted and seer investigated. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestTwoSeersActIndependently(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing two seers act independently ===")

	// Setup: 1 werewolf + 2 seers + 1 villager = 4 players
	var players []*TestPlayer
	for _, name := range []string{"D1", "D2", "D3", "D4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleSeer)
	players[0].addRoleByID(RoleSeer)
	players[0].addRoleByID(RoleVillager)
	players[0].startGame()

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
	seer1.seerInvestigatePlayer(werewolf.Name)

	// Seer2 investigates villager
	seer2.seerInvestigatePlayer(villager.Name)

	// Night is still active (werewolf hasn't voted) - verify and read both results
	if seer1.isInDayPhase() {
		ctx.logger.LogDB("FAIL: night ended before werewolf voted")
		t.Error("Night should not end before werewolf votes")
	}

	result1 := seer1.getSeerResult()
	if !strings.Contains(result1, "Werewolf") {
		t.Errorf("Seer1 should see 'Werewolf' result, got: %s", result1)
	}

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
	werewolf.voteForPlayer(villager.Name)

	submitNightSurveysForAllPlayers(players)

	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: did not transition to day after werewolf voted")
		t.Error("Should transition to day after werewolf voted (both seers already investigated)")
	}

	ctx.logger.Debug("=== Test passed ===")
}
