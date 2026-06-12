package main

import (
	"fmt"
	"strings"
	"testing"
)

// witchCanSeeVictim waits for the witch's heal targets section to contain the named player.
// The victim only appears after werewolves press End Vote, so we poll until it shows up.
func (tp *TestPlayer) witchCanSeeVictim(name string) bool {
	checkJS := fmt.Sprintf(`(() => !!document.querySelector("#witch-heal-targets .player-card[player-name='%s']"))`, name)
	err := tp.waitUntilCondition(checkJS, "witch victim "+name)
	return err == nil
}

func TestWitchHealSavesVictim(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Test: Witch heals victim ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 4 villagers (including witch) + 2 werewolves = 6 players
	var players []*TestPlayer
	for _, name := range []string{"W1", "W2", "W3", "W4", "W5", "W6"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	// Add roles: 3 villagers, 1 witch, 2 werewolves = 6 roles for 6 players
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWitch)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	// Wait for all players to reach night phase
	for _, p := range players {
		err := p.waitForNightPhase()
		if err != nil {
			ctx.logger.Debug("Warning: timeout waiting for night phase on %s: %v", p.Name, err)
		}
	}

	var werewolves, villagers []*TestPlayer
	werewolves, villagers = findPlayersByRole(players)

	ctx.logger.Debug("Players: %d werewolves, %d villagers", len(werewolves), len(villagers))

	// Find witch
	var witch *TestPlayer
	for _, p := range villagers {
		if p.getRole() == "Witch" {
			witch = p
			break
		}
	}
	if witch == nil {
		t.Fatal("Witch not found in villagers")
	}

	// Werewolves kill first villager (not the witch)
	targetVillager := villagers[0]
	if targetVillager == witch {
		targetVillager = villagers[1]
	}
	werewolves[0].voteForPlayer(targetVillager.Name)
	werewolves[1].voteForPlayer(targetVillager.Name)

	// Witch should see the victim name
	if !witch.witchCanSeeVictim(targetVillager.Name) {
		t.Errorf("Witch should see werewolf target name: %s", targetVillager.Name)
	}

	// Select heal target by clicking the victim's card in the heal section
	witch.clickAndWait("[id^='witch-select-heal-form-'] .player-card[player-name='" + targetVillager.Name + "']")
	// Wait for server to confirm selection (card gains `selected` attribute)
	witch.waitUntilCondition(
		`() => !!document.querySelector('[id^="witch-select-heal-form-"] .player-card[selected]')`,
		"witch heal target selected",
	)

	// Witch clicks Done to apply and end night
	witch.waitUntilCondition(`() => !!document.querySelector('#witch-apply-button')`, "witch apply button visible")
	witch.clickAndWait("#witch-apply-button")
	// Wait for server to confirm apply (shows waiting or survey form)
	witch.waitUntilCondition(
		`() => !!document.querySelector('#night-survey-form') || document.body.textContent.includes('Waiting for the night to end')`,
		"witch apply done",
	)

	submitNightSurveysForAllPlayers(players)

	// Check day phase - victim should be alive
	if !targetVillager.isInDayPhase() {
		content := targetVillager.getGameContent()
		ctx.logger.LogDB("FAIL: not in day phase")
		t.Errorf("Should be in day phase after night with heal. Content: %s", content)
	}

	// Verify victim is still alive (no death announcement)
	announcement := targetVillager.getDeathAnnouncement()
	if strings.Contains(announcement, targetVillager.Name) {
		t.Errorf("Victim should not be in death announcement: %s", announcement)
	}

	// History: witch heal is actor-only — only the witch sees it
	healEntry := "You saved " + targetVillager.Name + " with your heal potion"
	if !witch.historyContains(healEntry) {
		ctx.logger.LogDB("FAIL: witch cannot see own heal in history")
		t.Errorf("Witch should see their heal in history, got: %s", witch.getHistoryText())
	}
	if werewolves[0].historyContains(healEntry) {
		ctx.logger.LogDB("FAIL: werewolf can see witch heal in history")
		t.Errorf("Werewolf should not see witch heal in history")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWitchPoisonKillsPlayer(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Test: Witch poison kills player ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 4 villagers (including witch) + 2 werewolves = 6 players
	var players []*TestPlayer
	for _, name := range []string{"W1", "W2", "W3", "W4", "W5", "W6"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	// Add roles: 3 villagers, 1 witch, 2 werewolves = 6 roles for 6 players
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWitch)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	// Wait for all players to reach night phase
	for _, p := range players {
		err := p.waitForNightPhase()
		if err != nil {
			ctx.logger.Debug("Warning: timeout waiting for night phase on %s: %v", p.Name, err)
		}
	}

	var werewolves, villagers []*TestPlayer
	werewolves, villagers = findPlayersByRole(players)

	// Find witch
	var witch *TestPlayer
	var otherVillager *TestPlayer
	for _, p := range villagers {
		if p.getRole() == "Witch" {
			witch = p
		} else if otherVillager == nil {
			otherVillager = p
		}
	}
	if witch == nil {
		t.Fatal("Witch not found")
	}

	// Werewolves kill first villager (not the witch or the one we'll poison)
	targetVillager := villagers[0]
	if targetVillager == witch {
		targetVillager = villagers[1]
	}
	werewolves[0].voteForPlayer(targetVillager.Name)
	werewolves[1].voteForPlayer(targetVillager.Name)

	// Wait for EndVote broadcast to reach witch's page (victim card appears in heal section)
	// This is a required sync point: ensures the WS message was processed and HTMX re-initialized ws-send forms.
	if !witch.witchCanSeeVictim(targetVillager.Name) {
		t.Fatalf("Witch did not see victim card for %s in time", targetVillager.Name)
	}

	// Select poison target by clicking the player's card in the poison section
	witch.clickAndWait("[id^='witch-select-poison-form-'] .player-card[player-name='" + otherVillager.Name + "']")
	// Wait for server to confirm selection (card gains `selected` attribute)
	witch.waitUntilCondition(
		`() => !!document.querySelector('[id^="witch-select-poison-form-"] .player-card[selected]')`,
		"witch poison target selected",
	)

	// Witch clicks Done to apply and end night
	witch.waitUntilCondition(`() => !!document.querySelector('#witch-apply-button')`, "witch apply button visible")
	witch.clickAndWait("#witch-apply-button")
	// Wait for server to confirm apply (shows waiting or survey form)
	witch.waitUntilCondition(
		`() => !!document.querySelector('#night-survey-form') || document.body.textContent.includes('Waiting for the night to end')`,
		"witch apply done",
	)

	submitNightSurveysForAllPlayers(players)

	// Wait for phase transition to day (use a living player, not the poisoned one)
	err := witch.waitForDayPhase()
	if err != nil {
		ctx.logger.Debug("Warning: timeout waiting for day phase: %v", err)
	}

	// Check results in day phase
	if !witch.isInDayPhase() {
		content := witch.getGameContent()
		t.Errorf("Should be in day phase after night. Content: %s", content)
	}

	// Verify poison target is dead (appears in death announcement with victim and poison target)
	announcement := witch.getDeathAnnouncement()
	if !strings.Contains(announcement, otherVillager.Name) {
		content := witch.getGameContent()
		t.Errorf("Poisoned player %s should be in death announcement. Announcement: %s, Content: %s",
			otherVillager.Name, announcement, content)
	}

	// History: witch poison action is actor-only — only the witch sees it
	poisonEntry := "You poisoned " + otherVillager.Name
	if !witch.historyContains(poisonEntry) {
		ctx.logger.LogDB("FAIL: witch cannot see own poison in history")
		t.Errorf("Witch should see their poison in history, got: %s", witch.getHistoryText())
	}
	if werewolves[0].historyContains(poisonEntry) {
		ctx.logger.LogDB("FAIL: werewolf can see witch poison in history")
		t.Errorf("Werewolf should not see witch poison in history")
	}

	// Night deaths (wolf kill + witch poison) should be public in history
	// The record format is "Night 1: NAME (ROLE) was found dead"
	wolfDeathEntry := "Night 1: " + targetVillager.Name + " (" + targetVillager.getRole() + ") was found dead"
	if !werewolves[0].historyContains(wolfDeathEntry) {
		ctx.logger.LogDB("FAIL: wolf kill death not in werewolf history")
		t.Errorf("Wolf kill death should be public in history, got: %s", werewolves[0].getHistoryText())
	}
	if !witch.historyContains(wolfDeathEntry) {
		ctx.logger.LogDB("FAIL: wolf kill death not in witch history")
		t.Errorf("Wolf kill death should be public in history, got: %s", witch.getHistoryText())
	}
	poisonDeathEntry := "Night 1: " + otherVillager.Name + " (" + otherVillager.getRole() + ") was found dead"
	if !werewolves[0].historyContains(poisonDeathEntry) {
		ctx.logger.LogDB("FAIL: poison death not in werewolf history")
		t.Errorf("Poison death should be public in history, got: %s", werewolves[0].getHistoryText())
	}
	if !witch.historyContains(poisonDeathEntry) {
		ctx.logger.LogDB("FAIL: poison death not in witch history")
		t.Errorf("Poison death should be public in history, got: %s", witch.getHistoryText())
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWitchPassEndNight(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Test: Witch pass ends night ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 4 villagers (including witch) + 2 werewolves = 6 players
	var players []*TestPlayer
	for _, name := range []string{"W1", "W2", "W3", "W4", "W5", "W6"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	// Add roles: 3 villagers, 1 witch, 2 werewolves = 6 roles for 6 players
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWitch)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	// Wait for all players to reach night phase
	for _, p := range players {
		err := p.waitForNightPhase()
		if err != nil {
			ctx.logger.Debug("Warning: timeout waiting for night phase on %s: %v", p.Name, err)
		}
	}

	var werewolves, villagers []*TestPlayer
	werewolves, villagers = findPlayersByRole(players)

	// Find witch
	var witch *TestPlayer
	for _, p := range villagers {
		if p.getRole() == "Witch" {
			witch = p
			break
		}
	}
	if witch == nil {
		t.Fatal("Witch not found")
	}

	// Werewolves kill
	targetVillager := villagers[0]
	if targetVillager == witch {
		targetVillager = villagers[1]
	}
	werewolves[0].voteForPlayer(targetVillager.Name)
	werewolves[1].voteForPlayer(targetVillager.Name)

	// Wait for EndVote broadcast to reach witch's page (victim card appears in heal section)
	// This is a required sync point: ensures the WS message was processed and HTMX re-initialized ws-send forms.
	if !witch.witchCanSeeVictim(targetVillager.Name) {
		t.Fatalf("Witch did not see victim card for %s in time", targetVillager.Name)
	}

	// Witch clicks Done without using any potions
	witch.waitUntilCondition(`() => !!document.querySelector('#witch-apply-button')`, "witch apply button visible")
	witch.clickAndWait("#witch-apply-button")

	submitNightSurveysForAllPlayers(players)

	// Wait for phase transition to day
	err := witch.waitForDayPhase()
	if err != nil {
		ctx.logger.Debug("Warning: timeout waiting for day phase: %v", err)
	}

	// Should transition to day
	if !witch.isInDayPhase() {
		ctx.logger.LogDB("FAIL: not in day phase after witch pass")
		t.Error("Should transition to day after witch passes")
	}

	// History: witch apply is actor-only — only the witch sees it
	applyEntry := "Witch " + witch.Name + " confirmed her actions"
	if !witch.historyContains(applyEntry) {
		ctx.logger.LogDB("FAIL: witch cannot see own apply in history")
		t.Errorf("Witch should see their apply in history, got: %s", witch.getHistoryText())
	}
	if werewolves[0].historyContains(applyEntry) {
		ctx.logger.LogDB("FAIL: werewolf can see witch apply in history")
		t.Errorf("Werewolf should not see witch apply in history")
	}

	ctx.logger.Debug("=== Test passed ===")
}
