package main

import (
	"fmt"
	"strings"
	"testing"
)

// ============================================================================
// Seer Test Helpers
// ============================================================================

// seerSelectTarget clicks a target player in the Seer selection list.
func (tp *TestPlayer) seerSelectTarget(targetName string) {
	tp.clickAndWait("[id^='seer-select-form-'] .player-card[player-name='" + targetName + "']")
	tp.logHTML(fmt.Sprintf("after seer select: %s", targetName))
}

// seerInvestigate clicks the "Investigate" button to confirm the Seer's choice.
func (tp *TestPlayer) seerInvestigate() {
	tp.clickAndWait("#seer-investigate-button")
	tp.logHTML("after seer investigate")
}

// ============================================================================
// Doppelganger Test Helpers
// ============================================================================

func findPlayersByRoleWithDoppelganger(players []*TestPlayer) (werewolves, villagers, doppelgangers []*TestPlayer) {
	for _, p := range players {
		switch p.getRole() {
		case "Werewolf":
			werewolves = append(werewolves, p)
		case "Doppelganger":
			doppelgangers = append(doppelgangers, p)
		default:
			// Seer, Villager, Doctor, etc. all go in villagers for simplicity
			villagers = append(villagers, p)
		}
	}
	return
}

func findPlayersByRoleWithDoppelgangerAndSeer(players []*TestPlayer) (werewolves, villagers, seers, doppelgangers []*TestPlayer) {
	for _, p := range players {
		switch p.getRole() {
		case "Werewolf":
			werewolves = append(werewolves, p)
		case "Doppelganger":
			doppelgangers = append(doppelgangers, p)
		case "Seer":
			seers = append(seers, p)
		default:
			villagers = append(villagers, p)
		}
	}
	return
}

// canSeeDoppelgangerUI checks if the player sees the Doppelganger copy UI.
func (tp *TestPlayer) canSeeDoppelgangerUI() bool {
	html, _ := tp.Page.HTML()
	return strings.Contains(html, "Doppelganger: Choose Your Identity")
}

// isDoppelgangerCopyButtonEnabled returns true if the "Become" copy button is present and not disabled.
func (tp *TestPlayer) isDoppelgangerCopyButtonEnabled() bool {
	found, el, _ := tp.p().Has("#doppelganger-copy-button")
	if !found {
		return false
	}
	disabled, _ := el.Attribute("disabled")
	return disabled == nil
}

// doppelgangerSelectTarget clicks a target player in the Doppelganger selection list.
func (tp *TestPlayer) doppelgangerSelectTarget(targetName string) {
	tp.clickAndWait("[id^='doppelganger-select-form-'] .player-card[player-name='" + targetName + "']")
	tp.logHTML(fmt.Sprintf("after doppelganger select: %s", targetName))
}

// doppelgangerCopy clicks the "Become" button to finalise the copy.
func (tp *TestPlayer) doppelgangerCopy() {
	tp.clickAndWait("#doppelganger-copy-button")
	tp.logHTML("after doppelganger copy")
}

// ============================================================================
// Doppelganger Tests
// ============================================================================

// TestDoppelgangerCopiesVillager verifies the full happy path:
// Night only resolves after the Doppelganger copies, and the Doppelganger's
// role immediately changes to the copied role.
func TestDoppelgangerCopiesVillager(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// 1 Doppelganger + 2 Villagers + 1 Werewolf = 4 players
	var players []*TestPlayer
	for _, name := range []string{"D1", "D2", "D3", "D4"} {
		players = append(players, browser.signupPlayer(ctx.baseURL, name))
	}
	players[0].addRoleByID(RoleDoppelganger)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	werewolves, villagers, doppelgangers := findPlayersByRoleWithDoppelganger(players)
	if len(doppelgangers) == 0 || len(werewolves) == 0 || len(villagers) < 2 {
		t.Skip("Role assignment didn't produce expected roles")
	}

	dg := doppelgangers[0]
	wolf := werewolves[0]

	// Doppelganger must see the copy UI on Night 1.
	if !dg.canSeeDoppelgangerUI() {
		ctx.logger.LogDB("FAIL: doppelganger UI not visible")
		t.Fatal("Doppelganger should see the copy UI on Night 1")
	}

	// Copy button should be disabled until a target is selected.
	if dg.isDoppelgangerCopyButtonEnabled() {
		t.Fatal("Doppelganger copy button should be disabled before selecting a target")
	}

	// Werewolf votes — night should not resolve yet (Doppelganger hasn't copied).
	wolf.voteForPlayer(villagers[0].Name)
	if wolf.isInDayPhase() {
		t.Fatal("Night should not resolve before Doppelganger has copied")
	}

	// Doppelganger selects a villager.
	dg.doppelgangerSelectTarget(villagers[1].Name)
	if !dg.isDoppelgangerCopyButtonEnabled() {
		t.Fatal("Doppelganger copy button should be enabled after selecting a target")
	}

	// Night still should not resolve — copy not yet confirmed.
	if wolf.isInDayPhase() {
		t.Fatal("Night should not resolve before Doppelganger confirms copy")
	}

	// Doppelganger confirms copy.
	dg.doppelgangerCopy()

	submitNightSurveysForAllPlayers(players)

	// Night should now resolve.
	if err := wolf.waitForDayPhase(); err != nil {
		ctx.logger.LogDB("FAIL: day phase did not start")
		t.Fatalf("Night should resolve after Doppelganger copies: %v", err)
	}

	// Doppelganger's sidebar role should now show the copied role.
	newRole := dg.getRole()
	if newRole == "Doppelganger" {
		ctx.logger.LogDB("FAIL: doppelganger role not updated")
		t.Errorf("Doppelganger's role should have changed, still shows 'Doppelganger'")
	}

	// History should contain the copy notification (actor-only visibility).
	if !dg.historyContains("You secretly became a") {
		ctx.logger.LogDB("FAIL: copy history missing")
		t.Errorf("Doppelganger history should contain copy notification, got: %s", dg.getHistoryText())
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestDoppelgangerCopiesWerewolf verifies that when the Doppelganger copies a
// werewolf their role immediately changes to Werewolf.
func TestDoppelgangerCopiesWerewolf(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// 1 Doppelganger + 3 Villagers + 1 Werewolf = 5 players
	var players []*TestPlayer
	for _, name := range []string{"DW1", "DW2", "DW3", "DW4", "DW5"} {
		players = append(players, browser.signupPlayer(ctx.baseURL, name))
	}
	players[0].addRoleByID(RoleDoppelganger)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	werewolves, villagers, doppelgangers := findPlayersByRoleWithDoppelganger(players)
	if len(doppelgangers) == 0 || len(werewolves) == 0 || len(villagers) < 3 {
		t.Skip("Role assignment didn't produce expected roles")
	}

	dg := doppelgangers[0]
	wolf := werewolves[0]

	// Werewolf votes.
	wolf.voteForPlayer(villagers[0].Name)

	// Doppelganger copies the werewolf.
	dg.doppelgangerSelectTarget(wolf.Name)
	dg.doppelgangerCopy()

	// Doppelganger's role should immediately change to Werewolf.
	if err := dg.waitUntilCondition(`() => {
		const card = document.querySelector('#sidebar-role-card');
		return card && card.getAttribute('role-name') === 'Werewolf';
	}`, "doppelganger becomes werewolf"); err != nil {
		ctx.logger.LogDB("FAIL: doppelganger did not become werewolf")
		t.Fatalf("Doppelganger should show Werewolf role after copying werewolf: %v", err)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestDoppelgangerCanUnselectTarget verifies that clicking a selected player
// a second time deselects them and disables the copy button.
func TestDoppelgangerCanUnselectTarget(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// 1 Doppelganger + 1 Villager + 1 Werewolf = 3 players
	var players []*TestPlayer
	for _, name := range []string{"DU1", "DU2", "DU3"} {
		players = append(players, browser.signupPlayer(ctx.baseURL, name))
	}
	players[0].addRoleByID(RoleDoppelganger)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	_, villagers, doppelgangers := findPlayersByRoleWithDoppelganger(players)
	if len(doppelgangers) == 0 || len(villagers) == 0 {
		t.Skip("Role assignment didn't produce expected roles")
	}

	dg := doppelgangers[0]
	target := villagers[0]

	// Select a target — copy button should become enabled.
	dg.doppelgangerSelectTarget(target.Name)
	if !dg.isDoppelgangerCopyButtonEnabled() {
		t.Fatal("Copy button should be enabled after selecting a target")
	}

	// Click the same target again — should deselect, button disabled.
	dg.doppelgangerSelectTarget(target.Name)
	if dg.isDoppelgangerCopyButtonEnabled() {
		t.Fatal("Copy button should be disabled after deselecting the target")
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestDoppelgangerUIHiddenOnNight2 verifies that the Doppelganger copy UI
// is not shown on Night 2 (it is Night 1 only).
func TestDoppelgangerUIHiddenOnNight2(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// 1 Doppelganger + 3 Villagers + 1 Werewolf = 5 players
	// Doppelganger copies a Villager on Night 1.
	// Wolf kills a villager. Day vote eliminates another villager.
	// 3 players survive into Night 2.
	var players []*TestPlayer
	for _, name := range []string{"DN1", "DN2", "DN3", "DN4", "DN5"} {
		players = append(players, browser.signupPlayer(ctx.baseURL, name))
	}
	players[0].addRoleByID(RoleDoppelganger)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	werewolves, villagers, doppelgangers := findPlayersByRoleWithDoppelganger(players)
	if len(doppelgangers) == 0 || len(werewolves) == 0 || len(villagers) < 3 {
		t.Skip("Role assignment didn't produce expected roles")
	}

	dg := doppelgangers[0]
	wolf := werewolves[0]

	// Night 1: Doppelganger must see the UI.
	if !dg.canSeeDoppelgangerUI() {
		t.Fatal("Doppelganger should see the copy UI on Night 1")
	}

	// Doppelganger copies villagers[0]. Wolf kills villagers[1].
	dg.doppelgangerSelectTarget(villagers[0].Name)
	dg.doppelgangerCopy()

	wolf.voteForPlayer(villagers[1].Name)
	submitNightSurveysForAllPlayers(players)

	if err := wolf.waitForDayPhase(); err != nil {
		t.Fatalf("Day 1 did not start: %v", err)
	}

	// Day 1: vote out villagers[2] — 3 players survive into Night 2.
	// Alive after night 1: dg, wolf, villagers[0], villagers[2] (villagers[1] was killed).
	dg.dayVoteForPlayer(villagers[2].Name)
	wolf.dayVoteForPlayer(villagers[2].Name)
	villagers[0].dayVoteForPlayer(villagers[2].Name)
	villagers[2].dayVoteForPlayer(wolf.Name)

	if err := wolf.waitForNightPhase(); err != nil {
		t.Fatalf("Night 2 did not start: %v", err)
	}

	// Night 2: Doppelganger UI must NOT be shown.
	if dg.canSeeDoppelgangerUI() {
		t.Error("Doppelganger 'Choose Your Identity' UI should not appear on Night 2")
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestDoppelgangerSeerNotificationOnWerewolfCopy verifies that a Seer who
// investigated the Doppelganger before the copy receives a warning toast when
// the Doppelganger copies a werewolf-team player.
func TestDoppelgangerSeerNotificationOnWerewolfCopy(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// 1 Seer + 1 Doppelganger + 2 Villagers + 1 Werewolf = 5 players
	var players []*TestPlayer
	for _, name := range []string{"DS1", "DS2", "DS3", "DS4", "DS5"} {
		players = append(players, browser.signupPlayer(ctx.baseURL, name))
	}
	players[0].addRoleByID(RoleSeer)
	players[0].addRoleByID(RoleDoppelganger)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	werewolves, villagers, seers, doppelgangers := findPlayersByRoleWithDoppelgangerAndSeer(players)
	if len(seers) == 0 || len(doppelgangers) == 0 || len(werewolves) == 0 || len(villagers) < 2 {
		t.Skip("Role assignment didn't produce expected roles")
	}

	seer := seers[0]
	dg := doppelgangers[0]
	wolf := werewolves[0]

	// Werewolf votes for a villager.
	wolf.voteForPlayer(villagers[0].Name)

	// Seer investigates the Doppelganger — result should be "Not Werewolf".
	seer.seerSelectTarget(dg.Name)
	seer.seerInvestigate()

	// Confirm investigation result shows doppelganger as not-werewolf.
	seerResult, _ := seer.p().Element("#seer-result")
	if seerResult != nil {
		team, _ := seerResult.Attribute("team")
		if team != nil && *team == "werewolf" {
			t.Errorf("Seer result should show Doppelganger as non-werewolf before copy, got team=%s", *team)
		}
	}

	// Doppelganger copies the werewolf — this should trigger a warning toast to the Seer.
	dg.doppelgangerSelectTarget(wolf.Name)
	dg.doppelgangerCopy()

	// Seer should receive a warning toast about their stale reading.
	warningText := "has become a werewolf"
	if err := seer.waitUntilCondition(`() => {
		const container = document.querySelector('#toast-container');
		return container && container.textContent.includes('has become a werewolf');
	}`, "seer warning toast"); err != nil {
		ctx.logger.LogDB("FAIL: seer did not receive werewolf warning toast")
		t.Fatalf("Seer should receive a warning toast after Doppelganger becomes werewolf: %v", err)
	}

	if !seer.hasToastWithText(warningText) {
		ctx.logger.LogDB("FAIL: seer warning toast missing")
		t.Errorf("Seer should see warning toast containing %q", warningText)
	}

	ctx.logger.Debug("=== Test passed ===")
}
