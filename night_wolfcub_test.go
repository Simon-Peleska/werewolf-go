package main

import (
	"strings"
	"testing"
)

// ============================================================================
// Wolf Cub Test Helpers
// ============================================================================

func findPlayersByRoleWithWolfCub(players []*TestPlayer) (werewolves, villagers, wolfCubs []*TestPlayer) {
	for _, p := range players {
		role := p.getRole()
		switch role {
		case "Werewolf":
			werewolves = append(werewolves, p)
		case "Wolf Cub":
			wolfCubs = append(wolfCubs, p)
		default:
			villagers = append(villagers, p)
		}
	}
	return
}

// canSeeDoubleKillSection checks if the werewolf sees the Wolf Cub double kill UI
func (tp *TestPlayer) canSeeDoubleKillSection() bool {
	html, _ := tp.Page.HTML()
	return strings.Contains(html, "Wolf Cub's Revenge")
}

// ============================================================================
// Wolf Cub Tests
// ============================================================================

func TestWolfCubNightKillTriggersDoubleKill(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing Wolf Cub night kill triggers double kill ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 1 Wolf Cub + 1 Werewolf + 4 Villagers = 6 players (C1-C6)
	var players []*TestPlayer
	for _, name := range []string{"C1", "C2", "C3", "C4", "C5", "C6"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleWolfCub)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].startGame()

	werewolves, villagers, wolfCubs := findPlayersByRoleWithWolfCub(players)
	ctx.logger.Debug("Werewolves: %v, Villagers: %v, WolfCubs: %v",
		playerNames(werewolves), playerNames(villagers), playerNames(wolfCubs))

	if len(wolfCubs) == 0 || len(werewolves) == 0 {
		t.Fatal("Need at least 1 Wolf Cub and 1 Werewolf")
	}

	wolfCub := wolfCubs[0]
	werewolf := werewolves[0]

	// Night 1: Both wolf-team members kill the Wolf Cub
	// Wolf Cub is also on the werewolf team, so it can vote too
	wolfCub.voteForPlayer(wolfCub.Name)
	werewolf.voteForPlayer(wolfCub.Name)

	submitNightSurveysForAllPlayers(players)

	// Should now be in day phase
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: not in day phase after wolf cub kill")
		t.Fatal("Should be in day phase after night 1")
	}

	// Death announcement should show Wolf Cub died
	announcement := werewolf.getDeathAnnouncement()
	if !strings.Contains(announcement, wolfCub.Name) {
		ctx.logger.LogDB("FAIL: death announcement missing wolf cub")
		t.Errorf("Death announcement should mention Wolf Cub '%s', got: %s", wolfCub.Name, announcement)
	}

	// Day 1: All alive players vote for a villager to advance to night 2
	// (avoid triggering game over — werewolves still outnumber checks)
	targetVillager := villagers[0]
	aliveWerewolfTeam := []*TestPlayer{werewolf}
	for _, v := range villagers {
		v.dayVoteForPlayer(targetVillager.Name)
	}
	for _, w := range aliveWerewolfTeam {
		w.dayVoteForPlayer(targetVillager.Name)
	}

	// Should be in night 2
	if !werewolf.isInNightPhase() {
		ctx.logger.LogDB("FAIL: not in night 2")
		t.Fatal("Should be in night 2 after day vote")
	}

	// Werewolf should see the double kill section
	if !werewolf.canSeeDoubleKillSection() {
		ctx.logger.LogDB("FAIL: no double kill section shown")
		t.Error("Werewolf should see Wolf Cub double kill section in night 2")
	}

	// Night 2: Werewolf votes for both victims
	victim1 := villagers[1]
	victim2 := villagers[2]

	// First kill vote
	werewolf.voteForPlayer(victim1.Name)

	// Second kill vote — click vote2 card
	found2, _, _ := werewolf.p().Has("[id^='vote2-form-'] player-card")
	if !found2 {
		ctx.logger.LogDB("FAIL: no vote2 cards found")
		t.Fatal("Should see vote2 buttons for second victim")
	}
	werewolf.clickAndWait("[id^='vote2-form-'] player-card[player-name='" + victim2.Name + "']")
	werewolf.waitUntilCondition(`() => !!document.querySelector('#werewolf-end-vote2-btn:not([disabled])')`, "end vote2 button enabled")
	if has, _, _ := werewolf.p().Has("#werewolf-end-vote2-btn:not([disabled])"); has {
		werewolf.clickAndWait("#werewolf-end-vote2-btn")
	}

	submitNightSurveysForAllPlayers(players)

	// Should transition to day 2 with 2 victims
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: not in day 2")
		t.Fatal("Should be in day 2 after both kills")
	}

	announcement2 := werewolf.getDeathAnnouncement()
	if !strings.Contains(announcement2, victim1.Name) {
		t.Errorf("Day 2 announcement should mention victim1 '%s', got: %s", victim1.Name, announcement2)
	}
	if !strings.Contains(announcement2, victim2.Name) {
		t.Errorf("Day 2 announcement should mention victim2 '%s', got: %s", victim2.Name, announcement2)
	}

	// History: Wolf Cub double kill (second victim) is team:werewolf — werewolf sees it, villagers don't
	kill2Entry := "voted to kill " + victim2.Name + " (Wolf Cub revenge)"
	if !werewolf.historyContains(kill2Entry) {
		ctx.logger.LogDB("FAIL: werewolf cannot see wolf cub double kill in history")
		t.Errorf("Werewolf should see wolf cub double kill in history, got: %s", werewolf.getHistoryText())
	}
	// villagers[3] is alive in day 2 (victims 0-2 are dead from prior rounds)
	if len(villagers) > 3 && villagers[3].historyContains(kill2Entry) {
		ctx.logger.LogDB("FAIL: villager can see wolf cub double kill in history")
		t.Errorf("Villager should not see wolf cub double kill in history")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWitchSavesSecondVictimInWolfCubDoubleKill(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing witch saves second victim in Wolf Cub double kill ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 1 Wolf Cub + 1 Werewolf + 1 Witch + 3 Villagers = 6 players (WK1-WK6)
	var players []*TestPlayer
	for _, name := range []string{"WK1", "WK2", "WK3", "WK4", "WK5", "WK6"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleWolfCub)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWitch)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].startGame()

	werewolves, villagers, wolfCubs := findPlayersByRoleWithWolfCub(players)
	var witch *TestPlayer
	var pureVillagers []*TestPlayer
	for _, v := range villagers {
		if v.getRole() == "Witch" {
			witch = v
		} else {
			pureVillagers = append(pureVillagers, v)
		}
	}

	ctx.logger.Debug("Werewolves: %v, WolfCubs: %v, Witch: %v, Villagers: %v",
		playerNames(werewolves), playerNames(wolfCubs), witch.Name, playerNames(pureVillagers))

	if len(wolfCubs) == 0 || len(werewolves) == 0 || witch == nil || len(pureVillagers) < 2 {
		t.Fatal("Need Wolf Cub, Werewolf, Witch and at least 2 villagers")
	}

	wolfCub := wolfCubs[0]
	werewolf := werewolves[0]

	// Night 1: Both wolf-team members kill the Wolf Cub (triggers double kill next night)
	wolfCub.voteForPlayer(wolfCub.Name)
	werewolf.voteForPlayer(wolfCub.Name)
	// Witch clicks Done without using any potions
	witch.waitUntilCondition(`() => !!document.querySelector('#witch-apply-button')`, "witch apply button visible")
	witch.clickAndWait("#witch-apply-button")

	submitNightSurveysForAllPlayers(players)

	// Day 1: all alive players vote out a villager to advance to night 2
	advanceTarget := pureVillagers[0]
	for _, p := range players {
		if p.isInDayPhase() {
			p.dayVoteForPlayer(advanceTarget.Name)
		}
	}

	// Should be in night 2
	if !werewolf.isInNightPhase() {
		ctx.logger.LogDB("FAIL: not in night 2")
		t.Fatal("Should be in night 2")
	}

	// Night 2: werewolf votes for victim1 and victim2
	victim1 := pureVillagers[1]
	victim2 := pureVillagers[2]

	werewolf.voteForPlayer(victim1.Name)

	found2, _, _ := werewolf.p().Has("[id^='vote2-form-'] player-card")
	if !found2 {
		ctx.logger.LogDB("FAIL: no vote2 cards found")
		t.Fatal("Should see vote2 buttons for second victim")
	}
	werewolf.clickAndWait("[id^='vote2-form-'] player-card[player-name='" + victim2.Name + "']")
	werewolf.waitUntilCondition(`() => !!document.querySelector('#werewolf-end-vote2-btn:not([disabled])')`, "end vote2 button enabled")
	if has, _, _ := werewolf.p().Has("#werewolf-end-vote2-btn:not([disabled])"); has {
		werewolf.clickAndWait("#werewolf-end-vote2-btn")
	}

	// Witch should see both victims listed
	if !witch.witchCanSeeVictim(victim1.Name) {
		ctx.logger.LogDB("FAIL: witch cannot see victim1")
		t.Errorf("Witch should see victim1 '%s'", victim1.Name)
	}
	if !witch.witchCanSeeVictim(victim2.Name) {
		ctx.logger.LogDB("FAIL: witch cannot see victim2")
		t.Errorf("Witch should see victim2 '%s'", victim2.Name)
	}

	// Witch selects victim2 to heal by clicking their card in the heal section
	if !witch.witchCanSeeVictim(victim2.Name) {
		ctx.logger.LogDB("FAIL: witch cannot see victim2 heal card")
		t.Fatalf("Witch should see heal card for victim2 '%s'", victim2.Name)
	}
	witch.clickAndWait("[id^='witch-select-heal-form-'] player-card[player-name='" + victim2.Name + "']")

	// Witch clicks Done to apply and end night
	witch.waitUntilCondition(`() => !!document.querySelector('#witch-apply-button')`, "witch apply button visible")
	witch.clickAndWait("#witch-apply-button")

	submitNightSurveysForAllPlayers(players)

	// Day 2: victim1 should be dead, victim2 should be alive
	if !witch.isInDayPhase() {
		ctx.logger.LogDB("FAIL: not in day 2")
		t.Fatal("Should be in day 2 after night resolves")
	}

	announcement := witch.getDeathAnnouncement()
	if !strings.Contains(announcement, victim1.Name) {
		t.Errorf("victim1 '%s' should be dead (not healed), announcement: %s", victim1.Name, announcement)
	}
	if strings.Contains(announcement, victim2.Name) {
		t.Errorf("victim2 '%s' should be alive (healed by witch), announcement: %s", victim2.Name, announcement)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWolfCubDayEliminationTriggersDoubleKill(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing Wolf Cub day elimination triggers double kill ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 1 Wolf Cub + 1 Werewolf + 4 Villagers = 6 players (C1-C6)
	var players []*TestPlayer
	for _, name := range []string{"E1", "E2", "E3", "E4", "E5", "E6"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleWolfCub)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].startGame()

	werewolves, villagers, wolfCubs := findPlayersByRoleWithWolfCub(players)
	ctx.logger.Debug("Werewolves: %v, Villagers: %v, WolfCubs: %v",
		playerNames(werewolves), playerNames(villagers), playerNames(wolfCubs))

	if len(wolfCubs) == 0 || len(werewolves) == 0 {
		t.Fatal("Need at least 1 Wolf Cub and 1 Werewolf")
	}

	wolfCub := wolfCubs[0]
	werewolf := werewolves[0]

	// Night 1: Werewolves kill a regular villager (not the wolf cub)
	killTarget := villagers[0]
	wolfCub.voteForPlayer(killTarget.Name)
	werewolf.voteForPlayer(killTarget.Name)

	submitNightSurveysForAllPlayers(players)

	// Day 1: All alive players vote to eliminate Wolf Cub
	for _, v := range villagers[1:] {
		v.dayVoteForPlayer(wolfCub.Name)
	}
	werewolf.dayVoteForPlayer(wolfCub.Name)
	wolfCub.dayVoteForPlayer(wolfCub.Name)

	// Should be in night 2
	if !werewolf.isInNightPhase() {
		ctx.logger.LogDB("FAIL: not in night 2 after wolf cub elimination")
		t.Fatal("Should be in night 2 after Wolf Cub is eliminated day 1")
	}

	// Werewolf should see the double kill section
	if !werewolf.canSeeDoubleKillSection() {
		ctx.logger.LogDB("FAIL: no double kill section shown")
		t.Error("Werewolf should see Wolf Cub double kill section in night 2")
	}

	// Night 2: Vote for both victims
	victim1 := villagers[1]
	victim2 := villagers[2]

	werewolf.voteForPlayer(victim1.Name)

	found2, _, _ := werewolf.p().Has("[id^='vote2-form-'] player-card")
	if !found2 {
		ctx.logger.LogDB("FAIL: no vote2 cards found")
		t.Fatal("Should see vote2 buttons for second victim")
	}
	werewolf.clickAndWait("[id^='vote2-form-'] player-card[player-name='" + victim2.Name + "']")
	werewolf.waitUntilCondition(`() => !!document.querySelector('#werewolf-end-vote2-btn:not([disabled])')`, "end vote2 button enabled")
	if has, _, _ := werewolf.p().Has("#werewolf-end-vote2-btn:not([disabled])"); has {
		werewolf.clickAndWait("#werewolf-end-vote2-btn")
	}

	submitNightSurveysForAllPlayers(players)

	// Day 2: two victims announced
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: not in day 2")
		t.Fatal("Should be in day 2 after both kills")
	}

	announcement := werewolf.getDeathAnnouncement()
	if !strings.Contains(announcement, victim1.Name) {
		t.Errorf("Day 2 announcement should mention victim1 '%s', got: %s", victim1.Name, announcement)
	}
	if !strings.Contains(announcement, victim2.Name) {
		t.Errorf("Day 2 announcement should mention victim2 '%s', got: %s", victim2.Name, announcement)
	}

	ctx.logger.Debug("=== Test passed ===")
}
