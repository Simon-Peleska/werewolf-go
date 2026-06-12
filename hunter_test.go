package main

import (
	"strings"
	"testing"
)

func playerNames(players []*TestPlayer) []string {
	var names []string
	for _, p := range players {
		names = append(names, p.Name)
	}
	return names
}

// ============================================================================
// Hunter Test Helpers
// ============================================================================

func findPlayersByRoleWithHunter(players []*TestPlayer) (werewolves, villagers, hunters []*TestPlayer) {
	for _, p := range players {
		role := p.getRole()
		switch role {
		case "Werewolf":
			werewolves = append(werewolves, p)
		case "Hunter":
			hunters = append(hunters, p)
		default:
			villagers = append(villagers, p)
		}
	}
	return
}

// hunterShootPlayer selects a target for the hunter and clicks the Shoot button.
func (tp *TestPlayer) hunterShootPlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Hunter selecting target: %s", tp.Name, targetName)
	}
	// Select the player — use JS click to avoid scroll-triggered CSS transition layout shifts
	tp.clickAndWait("[id^='hunter-select-form-'] .player-card[player-name='" + targetName + "']")
	tp.logHTML("after hunter select of " + targetName)
	// Click Shoot button to commit
	tp.clickAndWait("#hunter-shoot-button")
	tp.logHTML("after hunter shooting " + targetName)
}

// canSeeHunterButtons checks if the hunter revenge cards are visible
func (tp *TestPlayer) canSeeHunterButtons() bool {
	found, _, err := tp.p().Has("[id^='hunter-select-form-'] .player-card")
	canSee := err == nil && found
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see hunter buttons: %v", tp.Name, canSee)
	}
	return canSee
}

// getHunterRevengeResult returns the text of the hunter revenge result announcement
func (tp *TestPlayer) getHunterRevengeResult() string {
	el, err := tp.p().Element("#hunter-revenge-result")
	if err != nil {
		return ""
	}
	text, _ := el.Text()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Hunter revenge result: %s", tp.Name, text)
	}
	return text
}

// isInHunterRevengePhase checks if the hunter revenge section is visible
func (tp *TestPlayer) isInHunterRevengePhase() bool {
	found, _, err := tp.p().Has("#hunter-revenge-section")
	inPhase := err == nil && found
	if tp.logger != nil {
		tp.logger.Debug("[%s] In hunter revenge phase: %v", tp.Name, inPhase)
	}
	return inPhase
}

// isHunterWaiting checks if the "Hunter is choosing" waiting message is visible
func (tp *TestPlayer) isHunterWaiting() bool {
	found, _, err := tp.p().Has("#hunter-waiting")
	waiting := err == nil && found
	if tp.logger != nil {
		tp.logger.Debug("[%s] Hunter waiting message visible: %v", tp.Name, waiting)
	}
	return waiting
}

// ============================================================================
// Hunter Tests
// ============================================================================

func TestHunterDeathShotOnNightKill(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Hunter death shot after night kill ===")

	// Setup: 2 villagers + 1 werewolf + 1 hunter = 4 players
	var players []*TestPlayer
	for _, name := range []string{"H1", "H2", "H3", "H4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleHunter)
	players[0].startGame()

	werewolves, villagers, hunters := findPlayersByRoleWithHunter(players)
	if len(hunters) == 0 {
		t.Fatal("No Hunter found after role assignment")
	}
	if len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Need at least 1 werewolf and 1 villager")
	}

	ctx.logger.Debug("Werewolves: %v, Villagers: %v, Hunters: %v",
		playerNames(werewolves), playerNames(villagers), playerNames(hunters))

	// Night 1: Werewolf kills the Hunter
	hunter := hunters[0]
	werewolves[0].voteForPlayer(hunter.Name)

	submitNightSurveysForAllPlayers(players)

	ctx.logger.LogDB("after werewolf kills hunter")

	// Day 1: Hunter should see revenge buttons
	if !hunter.isInDayPhase() {
		content := hunter.getGameContent()
		ctx.logger.LogDB("FAIL: not in day phase")
		t.Fatalf("Should be in day phase. Content: %s", content)
	}

	// Death announcement should show the Hunter died
	announcement := hunter.getDeathAnnouncement()
	if !strings.Contains(announcement, hunter.Name) {
		ctx.logger.LogDB("FAIL: death announcement missing hunter name")
		t.Errorf("Death announcement should mention hunter. Got: %s", announcement)
	}

	// Hunter should see revenge buttons
	if !hunter.canSeeHunterButtons() {
		content := hunter.getGameContent()
		ctx.logger.LogDB("FAIL: hunter can't see revenge buttons")
		t.Fatalf("Hunter should see revenge buttons. Content: %s", content)
	}

	// Voting should NOT be visible yet (revenge pending)
	voteButtons := villagers[0].getDayVoteButtons()
	if len(voteButtons) > 0 {
		t.Error("Vote buttons should be hidden while Hunter revenge is pending")
	}

	// Hunter shoots a villager
	target := villagers[0]
	hunter.hunterShootPlayer(target.Name)

	ctx.logger.LogDB("after hunter revenge")

	// Should still be in day phase, now with voting visible
	if !villagers[1].isInDayPhase() {
		content := villagers[1].getGameContent()
		ctx.logger.LogDB("FAIL: not in day after revenge")
		t.Fatalf("Should still be in day phase after revenge. Content: %s", content)
	}

	// Revenge result should be shown
	result := villagers[1].getHunterRevengeResult()
	if !strings.Contains(result, target.Name) {
		content := villagers[1].getGameContent()
		t.Errorf("Revenge result should mention target '%s'. Result: %s. Content: %s", target.Name, result, content)
	}

	// Voting should now be visible
	voteButtons = villagers[1].getDayVoteButtons()
	if len(voteButtons) == 0 {
		content := villagers[1].getGameContent()
		ctx.logger.LogDB("FAIL: no vote buttons after revenge")
		t.Fatalf("Vote buttons should be visible after revenge. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestHunterDeathShotOnDayElimination(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Hunter death shot after day elimination ===")

	// Setup: 3 villagers + 1 werewolf + 1 hunter = 5 players
	var players []*TestPlayer
	for _, name := range []string{"D1", "D2", "D3", "D4", "D5"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleHunter)
	players[0].startGame()

	werewolves, villagers, hunters := findPlayersByRoleWithHunter(players)
	if len(hunters) == 0 || len(werewolves) == 0 {
		t.Fatal("Need at least 1 hunter and 1 werewolf")
	}

	ctx.logger.Debug("Werewolves: %v, Villagers: %v, Hunters: %v",
		playerNames(werewolves), playerNames(villagers), playerNames(hunters))

	hunter := hunters[0]

	// Night 1: Werewolf kills a villager (not the hunter)
	werewolves[0].voteForPlayer(villagers[0].Name)

	submitNightSurveysForAllPlayers(players)

	ctx.logger.LogDB("after night 1 kill")

	// Day 1: Vote to eliminate the Hunter
	// With 4 alive players, majority is 3
	var alivePlayers []*TestPlayer
	for _, p := range players {
		if p != villagers[0] { // villagers[0] died at night
			alivePlayers = append(alivePlayers, p)
		}
	}

	for _, p := range alivePlayers {
		if p != hunter {
			p.dayVoteForPlayer(hunter.Name)
		}
	}
	// Hunter votes for someone else
	hunter.dayVoteForPlayer(werewolves[0].Name)

	ctx.logger.LogDB("after day vote to eliminate hunter")

	// Hunter should now see revenge buttons (day elimination revenge)
	if !hunter.isInDayPhase() {
		content := hunter.getGameContent()
		ctx.logger.LogDB("FAIL: not in day phase for hunter revenge")
		t.Fatalf("Should be in day phase for hunter revenge. Content: %s", content)
	}

	if !hunter.canSeeHunterButtons() {
		content := hunter.getGameContent()
		ctx.logger.LogDB("FAIL: hunter can't see revenge buttons after day elimination")
		t.Fatalf("Hunter should see revenge buttons after day elimination. Content: %s", content)
	}

	// Hunter shoots a villager (not the werewolf, so the game continues)
	hunter.hunterShootPlayer(villagers[1].Name)

	ctx.logger.LogDB("after hunter day revenge")

	// After day-elimination revenge, should transition to night
	if !villagers[2].isInNightPhase() {
		content := villagers[2].getGameContent()
		ctx.logger.LogDB("FAIL: not in night phase after day elimination revenge")
		t.Fatalf("Should transition to night after day elimination revenge. Content: %s", content)
	}

	// History: hunter revenge is public — surviving players see it
	hunterEntry := "Hunter " + hunter.Name + " shot " + villagers[1].Name
	if !villagers[2].historyContains(hunterEntry) {
		ctx.logger.LogDB("FAIL: survivor cannot see hunter revenge in history")
		t.Errorf("Surviving villager should see hunter revenge in history, got: %s", villagers[2].getHistoryText())
	}
	if !werewolves[0].historyContains(hunterEntry) {
		ctx.logger.LogDB("FAIL: werewolf cannot see hunter revenge in history")
		t.Errorf("Werewolf should see hunter revenge in history (public action)")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestHunterShootsLastWerewolf(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Hunter shoots last werewolf — villagers win ===")

	// Setup: 1 villager + 1 werewolf + 1 hunter = 3 players
	var players []*TestPlayer
	for _, name := range []string{"W1", "W2", "W3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleHunter)
	players[0].startGame()

	werewolves, villagers, hunters := findPlayersByRoleWithHunter(players)
	if len(hunters) == 0 || len(werewolves) == 0 {
		t.Fatal("Need at least 1 hunter and 1 werewolf")
	}

	ctx.logger.Debug("Werewolves: %v, Villagers: %v, Hunters: %v",
		playerNames(werewolves), playerNames(villagers), playerNames(hunters))

	hunter := hunters[0]

	// Night 1: Werewolf kills the Hunter
	werewolves[0].voteForPlayer(hunter.Name)

	submitNightSurveysForAllPlayers(players)

	ctx.logger.LogDB("after werewolf kills hunter")

	// Day 1: Hunter uses revenge shot to kill the last werewolf
	hunter.hunterShootPlayer(werewolves[0].Name)

	ctx.logger.LogDB("after hunter kills last werewolf")

	// Game should be finished — villagers win
	if !villagers[0].isGameFinished() {
		content := villagers[0].getGameContent()
		ctx.logger.LogDB("FAIL: game not finished")
		t.Fatalf("Game should be finished after Hunter kills last werewolf. Content: %s", content)
	}

	winner := villagers[0].getWinner()
	if winner != "villagers" {
		ctx.logger.LogDB("FAIL: wrong winner")
		t.Errorf("Villagers should win, got: %s", winner)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestNonHunterPlayersWaitDuringRevenge(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing non-Hunter players see waiting message during revenge ===")

	// Setup: 2 villagers + 1 werewolf + 1 hunter = 4 players
	var players []*TestPlayer
	for _, name := range []string{"P1", "P2", "P3", "P4"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleHunter)
	players[0].startGame()

	werewolves, villagers, hunters := findPlayersByRoleWithHunter(players)
	if len(hunters) == 0 || len(werewolves) == 0 || len(villagers) < 2 {
		t.Fatal("Need 1 hunter, 1 werewolf, 2 villagers")
	}

	hunter := hunters[0]

	// Night 1: Werewolf kills the Hunter
	werewolves[0].voteForPlayer(hunter.Name)

	submitNightSurveysForAllPlayers(players)

	ctx.logger.LogDB("after werewolf kills hunter")
	// Non-hunter players should see waiting message
	if !villagers[0].isHunterWaiting() {
		content := villagers[0].getGameContent()
		ctx.logger.LogDB("FAIL: villager doesn't see hunter waiting message")
		t.Errorf("Non-hunter should see waiting message. Content: %s", content)
	}

	// Non-hunter should NOT see hunter buttons
	if villagers[0].canSeeHunterButtons() {
		t.Error("Non-hunter should not see hunter buttons")
	}

	// Werewolf should also see waiting message
	if !werewolves[0].isHunterWaiting() {
		content := werewolves[0].getGameContent()
		t.Errorf("Werewolf should see waiting message. Content: %s", content)
	}

	// Hunter should see buttons, not waiting
	if hunter.isHunterWaiting() {
		t.Error("Hunter should not see waiting message — should see buttons")
	}
	if !hunter.canSeeHunterButtons() {
		content := hunter.getGameContent()
		t.Errorf("Hunter should see revenge buttons. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}
