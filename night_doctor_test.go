package main

import (
	"strings"
	"testing"
)

// ============================================================================
// Doctor Test Helpers
// ============================================================================

// findPlayersByRoleWithDoctor returns players grouped by role including Doctor
func findPlayersByRoleWithDoctor(players []*TestPlayer) (werewolves, villagers, doctors []*TestPlayer) {
	for _, p := range players {
		role := p.getRole()
		switch role {
		case "Werewolf":
			werewolves = append(werewolves, p)
		case "Doctor":
			doctors = append(doctors, p)
		default:
			villagers = append(villagers, p)
		}
	}
	return
}

// doctorProtectPlayer selects a target for the doctor and clicks the Protect button.
func (tp *TestPlayer) doctorProtectPlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Doctor selecting target: %s", tp.Name, targetName)
	}
	// Select the player — use JS click to avoid scroll-triggered CSS transition layout shifts
	tp.clickAndWait("[id^='doctor-select-form-'] .player-card[player-name='" + targetName + "']")
	tp.logHTML("after doctor select of " + targetName)
	// Click Protect button to commit
	tp.clickAndWait("#doctor-protect-button")
	tp.logHTML("after doctor protection of " + targetName)
}

// getDoctorResult returns the text of the doctor's protection confirmation
func (tp *TestPlayer) getDoctorResult() string {
	el, err := tp.p().Element("#doctor-result")
	if err != nil {
		return ""
	}
	text, _ := el.Text()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Doctor result: %s", tp.Name, text)
	}
	return text
}

// canSeeDoctorButtons checks if the doctor protection cards are visible
func (tp *TestPlayer) canSeeDoctorButtons() bool {
	found, _, err := tp.p().Has("[id^='doctor-select-form-'] .player-card")
	canSee := err == nil && found
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see doctor buttons: %v", tp.Name, canSee)
	}
	return canSee
}

// hasNoDeathMessage checks if the day phase shows "no one died last night"
func (tp *TestPlayer) hasNoDeathMessage() bool {
	found, _, err := tp.p().Has("#no-death-message")

	if tp.logger != nil {
		tp.logger.Debug("[%s] Has no-death message: %v", tp.Name, found)
	}
	return err == nil && found
}

// ============================================================================
// Doctor Tests
// ============================================================================

func TestDoctorCanProtect(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Doctor can see protect buttons and get confirmation ===")

	// Setup: 1 villager + 1 werewolf + 1 doctor = 3 players
	var players []*TestPlayer
	for _, name := range []string{"E1", "E2", "E3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleDoctor)
	players[0].startGame()

	werewolves, villagers, doctors := findPlayersByRoleWithDoctor(players)
	if len(doctors) == 0 {
		t.Fatal("No Doctor found after role assignment")
	}
	if len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Missing werewolves or villagers")
	}

	doctor := doctors[0]
	villager := villagers[0]
	ctx.logger.Debug("Doctor: %s, protecting Villager: %s", doctor.Name, villager.Name)

	// Doctor should see protection buttons
	if !doctor.canSeeDoctorButtons() {
		ctx.logger.LogDB("FAIL: doctor cannot see protect buttons")
		t.Fatal("Doctor should see protection buttons during night phase")
	}

	// Doctor protects the villager - night stays active (werewolf hasn't voted yet)
	doctor.doctorProtectPlayer(villager.Name)

	// Doctor should see confirmation
	result := doctor.getDoctorResult()
	if !strings.Contains(result, villager.Name) {
		ctx.logger.LogDB("FAIL: doctor protection confirmation missing")
		t.Errorf("Doctor should see protection confirmation with target name %s, got: %s", villager.Name, result)
	}

	// History: doctor protection is actor-only — only the doctor sees it
	protectEntry := "You protected " + villager.Name
	if !doctor.historyContains(protectEntry) {
		ctx.logger.LogDB("FAIL: doctor cannot see own protection in history")
		t.Errorf("Doctor should see their protection in history, got: %s", doctor.getHistoryText())
	}
	if werewolves[0].historyContains(protectEntry) {
		ctx.logger.LogDB("FAIL: werewolf can see doctor protection in history")
		t.Errorf("Werewolf should not see doctor protection in history")
	}
	if villagers[0].historyContains(protectEntry) {
		ctx.logger.LogDB("FAIL: protected player can see doctor protection in history")
		t.Errorf("Protected player should not see doctor protection in history")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestDoctorSavesVictim(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Doctor saves the werewolf's target ===")

	// Setup: 1 villager + 1 werewolf + 1 doctor = 3 players
	var players []*TestPlayer
	for _, name := range []string{"F1", "F2", "F3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleDoctor)
	players[0].startGame()

	// Wait for all players to reach night phase
	for _, p := range players {
		err := p.waitForNightPhase()
		if err != nil {
			ctx.logger.Debug("Warning: timeout waiting for night phase on %s: %v", p.Name, err)
		}
	}

	werewolves, villagers, doctors := findPlayersByRoleWithDoctor(players)
	if len(doctors) == 0 || len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Missing required roles")
	}

	doctor := doctors[0]
	werewolf := werewolves[0]
	villager := villagers[0]
	ctx.logger.Debug("Doctor: %s, Werewolf: %s, Villager (target): %s", doctor.Name, werewolf.Name, villager.Name)

	// Doctor protects the villager first (before werewolf votes)
	doctor.doctorProtectPlayer(villager.Name)

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
		ctx.logger.LogDB("FAIL: victim not saved by doctor")
		t.Errorf("Doctor should have saved the victim, expected 'no one died' message. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestDoctorDoesNotSaveWrongPlayer(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Doctor protecting wrong player does not save victim ===")

	// Setup: 2 villagers + 1 werewolf + 1 doctor = 4 players
	var players []*TestPlayer
	for _, name := range []string{"G1", "G2", "G3", "G4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleDoctor)
	players[0].startGame()

	werewolves, villagers, doctors := findPlayersByRoleWithDoctor(players)
	if len(doctors) == 0 || len(werewolves) == 0 || len(villagers) < 2 {
		t.Fatalf("Missing required roles, got doctors=%d werewolves=%d villagers=%d", len(doctors), len(werewolves), len(villagers))
	}

	doctor := doctors[0]
	werewolf := werewolves[0]
	villager0 := villagers[0] // doctor protects this one
	villager1 := villagers[1] // werewolf kills this one
	ctx.logger.Debug("Doctor: %s protects %s, Werewolf: %s kills %s", doctor.Name, villager0.Name, werewolf.Name, villager1.Name)

	// Doctor protects villager0
	doctor.doctorProtectPlayer(villager0.Name)

	// Werewolf kills villager1 (a different player - not protected)
	werewolf.voteForPlayer(villager1.Name)

	submitNightSurveysForAllPlayers(players)

	// Day should show villager1 died
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: did not transition to day")
		t.Fatal("Should have transitioned to day phase")
	}

	if werewolf.hasNoDeathMessage() {
		ctx.logger.LogDB("FAIL: expected death but got no-death message")
		t.Error("Doctor protected wrong player, villager1 should have died")
	}

	announcement := werewolf.getDeathAnnouncement()
	if !strings.Contains(announcement, villager1.Name) {
		ctx.logger.LogDB("FAIL: death announcement missing victim name")
		t.Errorf("Day announcement should mention %s (the victim), got: %s", villager1.Name, announcement)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestNightWaitsForDoctor(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing night waits for doctor to protect ===")

	// Setup: 1 doctor + 1 werewolf = 2 players
	var players []*TestPlayer
	for _, name := range []string{"H1", "H2", "H3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleDoctor)
	players[0].addRoleByID(RoleVillager)
	players[0].startGame()

	// Wait for all players to reach night phase
	for _, p := range players {
		err := p.waitForNightPhase()
		if err != nil {
			ctx.logger.Debug("Warning: timeout waiting for night phase on %s: %v", p.Name, err)
		}
	}

	werewolves, _, doctors := findPlayersByRoleWithDoctor(players)
	if len(doctors) == 0 || len(werewolves) == 0 {
		t.Fatal("Missing doctor or werewolf")
	}

	doctor := doctors[0]
	werewolf := werewolves[0]
	ctx.logger.Debug("Doctor: %s, Werewolf: %s", doctor.Name, werewolf.Name)

	// Werewolf votes - night should NOT end yet (doctor hasn't protected)
	werewolf.voteForPlayer(doctor.Name)

	if werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: night ended before doctor protected")
		t.Error("Night should not end until doctor has protected")
	}
	if !werewolf.isInNightPhase() {
		t.Error("Should still be in night phase")
	}

	ctx.logger.Debug("Werewolf voted, night still active. Now doctor protects...")

	// Doctor protects - night SHOULD end now
	doctor.doctorProtectPlayer(werewolf.Name)

	submitNightSurveysForAllPlayers(players)

	// Wait for phase transition to day
	err := doctor.waitForDayPhase()
	if err != nil {
		ctx.logger.Debug("Warning: timeout waiting for day phase: %v", err)
	}

	if !doctor.isInDayPhase() {
		content := doctor.getGameContent()
		ctx.logger.LogDB("FAIL: did not transition to day after doctor protected")
		t.Errorf("Should transition to day after both werewolf voted and doctor protected. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestTwoDoctorsActIndependently(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing two doctors act independently ===")

	// Setup: 1 werewolf + 2 doctors + 1 villager = 4 players
	var players []*TestPlayer
	for _, name := range []string{"I1", "I2", "I3", "I4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleDoctor)
	players[0].addRoleByID(RoleDoctor)
	players[0].addRoleByID(RoleVillager)
	players[0].startGame()

	werewolves, villagers, doctors := findPlayersByRoleWithDoctor(players)
	if len(doctors) < 2 || len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatalf("Need 2 doctors, 1 werewolf, 1 villager, got doctors=%d werewolves=%d villagers=%d", len(doctors), len(werewolves), len(villagers))
	}

	doctor1 := doctors[0]
	doctor2 := doctors[1]
	werewolf := werewolves[0]
	villager := villagers[0]
	ctx.logger.Debug("Doctor1: %s, Doctor2: %s, Werewolf: %s, Villager: %s", doctor1.Name, doctor2.Name, werewolf.Name, villager.Name)

	// Both doctors protect BEFORE the werewolf votes so the night stays active
	// and we can verify each sees their own confirmation.

	// Doctor1 protects the villager
	doctor1.doctorProtectPlayer(villager.Name)

	// Doctor2 protects the werewolf (a different player)
	doctor2.doctorProtectPlayer(werewolf.Name)

	// Night is still active (werewolf hasn't voted) - verify and read both confirmations
	if doctor1.isInDayPhase() {
		ctx.logger.LogDB("FAIL: night ended before werewolf voted")
		t.Error("Night should not end before werewolf votes")
	}

	// Each doctor sees their own protection target
	result1 := doctor1.getDoctorResult()
	if !strings.Contains(result1, villager.Name) {
		t.Errorf("Doctor1 should see confirmation with %s, got: %s", villager.Name, result1)
	}
	// Doctor1 should NOT see doctor2's target in their confirmation
	if strings.Contains(result1, werewolf.Name) {
		t.Errorf("Doctor1 should not see Doctor2's protection target %s", werewolf.Name)
	}

	result2 := doctor2.getDoctorResult()
	if !strings.Contains(result2, werewolf.Name) {
		t.Errorf("Doctor2 should see confirmation with %s, got: %s", werewolf.Name, result2)
	}
	// Doctor2 should NOT see doctor1's target in their confirmation
	if strings.Contains(result2, villager.Name) {
		t.Errorf("Doctor2 should not see Doctor1's protection target %s", villager.Name)
	}

	// Werewolf votes for the villager (protected by doctor1) - all conditions met, night ends
	werewolf.voteForPlayer(villager.Name)

	submitNightSurveysForAllPlayers(players)

	// Day should show no one died (doctor1 protected the villager)
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: did not transition to day after werewolf voted")
		t.Fatal("Should transition to day after all conditions met")
	}

	if !werewolf.hasNoDeathMessage() {
		content := werewolf.getGameContent()
		ctx.logger.LogDB("FAIL: doctor1 should have saved the villager")
		t.Errorf("Doctor1 protected the villager, expected 'no one died'. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}
