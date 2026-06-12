package main

import (
	"strings"
	"testing"
)

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

// guardProtectPlayer selects a target for the guard and clicks the Protect button.
func (tp *TestPlayer) guardProtectPlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Guard selecting target: %s", tp.Name, targetName)
	}
	// Select the player — use JS click to avoid scroll-triggered CSS transition layout shifts
	tp.clickAndWait("[id^='guard-select-form-'] .player-card[player-name='" + targetName + "']")
	tp.logHTML("after guard select of " + targetName)
	// Click Protect button to commit
	tp.clickAndWait("#guard-protect-button")
	tp.logHTML("after guard protection of " + targetName)
}

// getGuardResult returns the text of the guard's protection confirmation
func (tp *TestPlayer) getGuardResult() string {
	el, err := tp.p().Element("#guard-result")
	if err != nil {
		return ""
	}
	text, _ := el.Text()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Guard result: %s", tp.Name, text)
	}
	return text
}

// canSeeGuardButtons checks if the guard protection cards are visible
func (tp *TestPlayer) canSeeGuardButtons() bool {
	found, _, err := tp.p().Has("[id^='guard-select-form-'] .player-card")
	canSee := err == nil && found
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see guard buttons: %v", tp.Name, canSee)
	}
	return canSee
}

// getGuardButtonNames returns the names shown on guard protection cards
func (tp *TestPlayer) getGuardButtonNames() []string {
	result, err := tp.p().Eval(`() => {
		const cards = document.querySelectorAll("[id^='guard-select-form-'] .player-card");
		return Array.from(cards).map(c => c.getAttribute('player-name') || '').filter(Boolean).join('\n');
	}`)
	if err != nil {
		return nil
	}
	raw := result.Value.String()
	if raw == "" {
		return nil
	}
	names := strings.Split(raw, "\n")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Guard button names: %v", tp.Name, names)
	}
	return names
}

// ============================================================================
// Guard Tests
// ============================================================================

func TestGuardCanProtect(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Guard can see protect buttons and get confirmation ===")

	// Setup: 1 villager + 1 werewolf + 1 guard = 3 players
	var players []*TestPlayer
	for _, name := range []string{"J1", "J2", "J3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	players[0].startGame()

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

	// Guard should see confirmation via WS update
	result := guard.getGuardResult()
	if !strings.Contains(result, villager.Name) {
		ctx.logger.LogDB("FAIL: guard protection confirmation missing")
		t.Errorf("Guard should see protection confirmation with target name %s, got: %s", villager.Name, result)
	}

	// History: guard protection is actor-only — only the guard sees it
	protectEntry := "You protected " + villager.Name
	if !guard.historyContains(protectEntry) {
		ctx.logger.LogDB("FAIL: guard cannot see own protection in history")
		t.Errorf("Guard should see their protection in history, got: %s", guard.getHistoryText())
	}
	if werewolves[0].historyContains(protectEntry) {
		ctx.logger.LogDB("FAIL: werewolf can see guard protection in history")
		t.Errorf("Werewolf should not see guard protection in history")
	}
	if villagers[0].historyContains(protectEntry) {
		ctx.logger.LogDB("FAIL: protected player can see guard protection in history")
		t.Errorf("Protected player should not see guard protection in history")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestGuardSavesVictim(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Guard saves the werewolf's target ===")

	// Setup: 1 villager + 1 werewolf + 1 guard = 3 players
	var players []*TestPlayer
	for _, name := range []string{"K1", "K2", "K3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	players[0].startGame()

	// Wait for all players to reach night phase
	for _, p := range players {
		err := p.waitForNightPhase()
		if err != nil {
			ctx.logger.Debug("Warning: timeout waiting for night phase on %s: %v", p.Name, err)
		}
	}

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

	// Werewolf votes for the villager (the protected player)
	werewolf.voteForPlayer(villager.Name)

	submitNightSurveysForAllPlayers(players)

	// Wait for phase transition to day
	err := werewolf.waitForDayPhase()
	if err != nil {
		ctx.logger.Debug("Warning: timeout waiting for day phase: %v", err)
	}

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
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Guard cannot protect themselves ===")

	// Setup: 1 villager + 1 werewolf + 1 guard = 3 players
	var players []*TestPlayer
	for _, name := range []string{"L1", "L2", "L3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	players[0].startGame()

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
	t.Parallel()
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
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	players[0].startGame()

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

	// Werewolf kills targetVillager (different from protected)
	werewolf.voteForPlayer(targetVillager.Name)

	submitNightSurveysForAllPlayers(players)

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
	}

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
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing night waits for guard to protect ===")

	// Setup: 1 guard + 1 werewolf + 1 villager = 3 players (so game doesn't end immediately)
	var players []*TestPlayer
	for _, name := range []string{"N1", "N2", "N3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	players[0].addRoleByID(RoleVillager)
	players[0].startGame()

	// Wait for all players to reach night phase
	for _, p := range players {
		err := p.waitForNightPhase()
		if err != nil {
			ctx.logger.Debug("Warning: timeout waiting for night phase on %s: %v", p.Name, err)
		}
	}

	werewolves, villagers, guards := findPlayersByRoleWithGuard(players)
	if len(guards) == 0 || len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Missing guard, werewolf, or villager")
	}

	guard := guards[0]
	werewolf := werewolves[0]
	villager := villagers[0]
	ctx.logger.Debug("Guard: %s, Werewolf: %s, Villager: %s", guard.Name, werewolf.Name, villager.Name)

	// Werewolf votes to kill villager - night should NOT end yet (guard hasn't protected)
	werewolf.voteForPlayer(villager.Name)

	if werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: night ended before guard protected")
		t.Error("Night should not end until guard has protected")
	}
	if !werewolf.isInNightPhase() {
		t.Error("Should still be in night phase")
	}

	ctx.logger.Debug("Werewolf voted, night still active. Now guard protects...")

	// Guard protects the villager (saving them) - night SHOULD end now
	guard.guardProtectPlayer(villager.Name)

	submitNightSurveysForAllPlayers(players)

	// Wait for phase transition to day
	err := guard.waitForDayPhase()
	if err != nil {
		ctx.logger.Debug("Warning: timeout waiting for day phase: %v", err)
	}

	if !guard.isInDayPhase() {
		content := guard.getGameContent()
		ctx.logger.LogDB("FAIL: did not transition to day after guard protected")
		t.Errorf("Should transition to day after both werewolf voted and guard protected. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestTwoGuardsActIndependently(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing two guards act independently ===")

	// Setup: 1 werewolf + 2 guards + 1 villager = 4 players
	var players []*TestPlayer
	for _, name := range []string{"O1", "O2", "O3", "O4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleGuard)
	players[0].addRoleByID(RoleGuard)
	players[0].addRoleByID(RoleVillager)
	players[0].startGame()

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

	// Guard2 protects the werewolf
	guard2.guardProtectPlayer(werewolf.Name)

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

	submitNightSurveysForAllPlayers(players)

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
