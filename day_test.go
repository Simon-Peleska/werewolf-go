package main

import (
	"strings"
	"testing"
)

// ============================================================================
// Day Phase Test Helpers
// ============================================================================

// dayVoteForPlayer clicks the day vote button for a specific player
func (tp *TestPlayer) dayVoteForPlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Day voting for: %s", tp.Name, targetName)
	}
	tp.clickAndWait("[id^='day-vote-form-'] player-card[player-name='" + targetName + "']")
	tp.logHTML("after day voting for " + targetName)
	// Auto-press End Vote if the button is present and enabled (all players have voted)
	if has, endVoteBtn, _ := tp.p().Has("#day-end-vote-btn:not([disabled])"); has {
		tp.clickElementAndWait(endVoteBtn)
	}
}

// getDayVoteButtons returns the names of players that can be voted for during day
func (tp *TestPlayer) getDayVoteButtons() []string {
	result, err := tp.p().Eval(`() => {
		const cards = document.querySelectorAll("[id^='day-vote-form-'] player-card");
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
		tp.logger.Debug("[%s] Day vote buttons: %v", tp.Name, names)
	}
	return names
}

// getCurrentDayVoteTarget returns the player name currently selected in the day vote UI
func (tp *TestPlayer) getCurrentDayVoteTarget() string {
	found, el, err := tp.p().Has("[id^='day-vote-form-'] player-card[selected]")
	if err != nil || !found {
		return ""
	}
	name, err := el.Attribute("player-name")
	if err != nil || name == nil {
		return ""
	}
	return *name
}

// getDayVoteCount returns the vote count shown on a day vote card for a given player
func (tp *TestPlayer) getDayVoteCount(targetName string) string {
	result, err := tp.p().Eval(`() => {
		const card = document.querySelector("[id^='day-vote-form-'] player-card[player-name='` + targetName + `']");
		return card ? (card.getAttribute('count') || '0') : '0';
	}`)
	if err != nil {
		return "0"
	}
	v := result.Value.String()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Day vote count for %s: %s", tp.Name, targetName, v)
	}
	return v
}

// isGameFinished checks if the game has ended
func (tp *TestPlayer) isGameFinished() bool {
	found, _, err := tp.p().Has(".win-hero")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Is game finished: %v", tp.Name, found)
	}
	return found && err == nil
}

// getWinner returns the winner if game is finished
func (tp *TestPlayer) getWinner() string {
	found, _, _ := tp.p().Has(".win-seal-villagers")
	if found {
		return "villagers"
	}

	found, _, _ = tp.p().Has(".win-seal-werewolves")
	if found {
		return "werewolves"
	}

	return ""
}

// setupDayPhaseGame creates a game, starts night, werewolves kill someone, transitions to day
func setupDayPhaseGame(ctx *TestContext, browser *TestBrowser, numVillagers, numWerewolves int) ([]*TestPlayer, []*TestPlayer, []*TestPlayer) {
	players := setupNightPhaseGame(ctx, browser, numVillagers, numWerewolves)
	werewolves, villagers := findPlayersByRole(players)

	// All werewolves vote for the first villager to transition to day
	targetName := villagers[0].Name
	for _, w := range werewolves {
		w.voteForPlayer(targetName)
	}

	submitNightSurveysForAllPlayers(players)

	// Wait for phase transition to day on all players
	for _, p := range players {
		err := p.waitForDayPhase()
		if err != nil {
			ctx.logger.Debug("Warning: timeout waiting for day phase on %s: %v", p.Name, err)
		}
	}

	ctx.logger.LogDB("after night kill, should be in day phase")

	return players, werewolves, villagers
}

// ============================================================================
// Day Phase Tests
// ============================================================================

func TestDayVoteByAlivePlayer(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing day vote by alive player ===")

	// Setup: 3 villagers, 1 werewolf - werewolf kills villager 0, leaves 2 villagers and 1 werewolf
	players, werewolves, villagers := setupDayPhaseGame(ctx, browser, 3, 1)

	// Verify we're in day phase
	if !werewolves[0].isInDayPhase() {
		ctx.logger.LogDB("FAIL: not in day phase")
		t.Fatal("Should be in day phase after night kill")
	}

	// Alive player should see vote buttons
	voteButtons := villagers[1].getDayVoteButtons()
	if len(voteButtons) == 0 {
		ctx.logger.LogDB("FAIL: no day vote buttons")
		t.Fatal("Alive player should see day vote buttons")
	}

	ctx.logger.Debug("Day vote buttons: %v", voteButtons)

	// Vote for the werewolf
	villagers[1].dayVoteForPlayer(werewolves[0].Name)

	// Verify vote was recorded (check page shows vote list with arrow format "VoterName → TargetName")
	content := villagers[1].getGameContent()
	if !strings.Contains(content, "→") {
		ctx.logger.LogDB("FAIL: vote not shown")
		t.Error("Vote should be visible in the vote list")
	}

	// History: day votes are public — all alive players see them
	voteEntry := "voted to eliminate " + werewolves[0].Name
	if !villagers[1].historyContains(voteEntry) {
		ctx.logger.LogDB("FAIL: voter cannot see own vote in history")
		t.Errorf("Voter should see their own vote in history, got: %s", villagers[1].getHistoryText())
	}
	if !werewolves[0].historyContains(voteEntry) {
		ctx.logger.LogDB("FAIL: vote target cannot see vote in history")
		t.Errorf("Vote target should see the vote in history (public action)")
	}
	if !villagers[2].historyContains(voteEntry) {
		ctx.logger.LogDB("FAIL: bystander cannot see vote in history")
		t.Errorf("Other alive player should see the vote in history (public action)")
	}

	ctx.logger.Debug("Players: %d", len(players))
	ctx.logger.Debug("=== Test passed ===")
}

func TestDayVoteCountsShownOnCards(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing day vote counts on cards ===")

	// 3 villagers, 1 werewolf — werewolf kills villager[0], leaving 3 alive:
	// villagers[1], villagers[2], werewolves[0]
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 3, 1)

	targetName := werewolves[0].Name
	ctx.logger.Debug("Target: %s, voters: %s, %s", targetName, villagers[1].Name, villagers[2].Name)

	// Before any vote: count should be 0
	if got := villagers[1].getDayVoteCount(targetName); got != "0" {
		t.Errorf("Expected count 0 before any vote, got %s", got)
	}

	// Villager 1 votes for werewolf (AllActed=false, no auto-end)
	villagers[1].dayVoteForPlayer(targetName)

	// Both villager 1 and villager 2 should see count=1 on the werewolf's card
	if got := villagers[1].getDayVoteCount(targetName); got != "1" {
		ctx.logger.LogDB("FAIL: wrong count after villager 1 votes")
		t.Errorf("Villager 1 page: expected count 1 after first vote, got %s", got)
	}
	if got := villagers[2].getDayVoteCount(targetName); got != "1" {
		ctx.logger.LogDB("FAIL: wrong count on villager 2 page")
		t.Errorf("Villager 2 page: expected count 1 after villager 1 voted, got %s", got)
	}

	// Villager 2 also votes for werewolf (2/3 voted, AllActed still false)
	villagers[2].dayVoteForPlayer(targetName)

	// All three alive players should see count=2 on the werewolf's card
	if got := villagers[1].getDayVoteCount(targetName); got != "2" {
		ctx.logger.LogDB("FAIL: wrong count after villager 2 votes (villager 1 view)")
		t.Errorf("Villager 1 page: expected count 2 after second vote, got %s", got)
	}
	if got := villagers[2].getDayVoteCount(targetName); got != "2" {
		ctx.logger.LogDB("FAIL: wrong count after villager 2 votes (villager 2 view)")
		t.Errorf("Villager 2 page: expected count 2 after second vote, got %s", got)
	}
	if got := werewolves[0].getDayVoteCount(targetName); got != "2" {
		ctx.logger.LogDB("FAIL: wrong count on target's own page")
		t.Errorf("Werewolf page: expected count 2 after two votes against them, got %s", got)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestDayVoteCanUnselect(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing day vote can unselect ===")

	// 3 villagers, 1 werewolf — werewolf kills villager[0], leaving 3 alive
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 3, 1)

	targetName := werewolves[0].Name
	ctx.logger.Debug("Voter: %s, Target: %s", villagers[1].Name, targetName)

	// Vote for werewolf
	villagers[1].dayVoteForPlayer(targetName)

	if got := villagers[1].getCurrentDayVoteTarget(); got != targetName {
		ctx.logger.LogDB("FAIL: vote not recorded")
		t.Fatalf("Expected vote for %s, got %q", targetName, got)
	}
	if got := villagers[1].getDayVoteCount(targetName); got != "1" {
		t.Errorf("Expected count 1 after voting, got %s", got)
	}

	// Click same card again to unselect
	villagers[1].dayVoteForPlayer(targetName)

	if got := villagers[1].getCurrentDayVoteTarget(); got != "" {
		ctx.logger.LogDB("FAIL: vote not cleared after unselect")
		t.Errorf("Expected no vote after unselect, got %q", got)
	}
	if got := villagers[1].getDayVoteCount(targetName); got != "0" {
		ctx.logger.LogDB("FAIL: vote count not decremented after unselect")
		t.Errorf("Expected count 0 after unselect, got %s", got)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestPlayerCanPassDayVote(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing majority pass skips elimination ===")

	// Setup: 4 villagers + 1 werewolf. Night kills villagers[0].
	// Day starts with 4 alive: villagers[1-3] + werewolf
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 4, 1)

	if len(werewolves) == 0 || len(villagers) < 4 {
		t.Fatal("Failed to find enough players")
	}

	// villagers[0] was killed at night; alive players are villagers[1:] + werewolves
	alivePlayers := append(villagers[1:], werewolves...)

	// All alive players press Pass
	for _, p := range alivePlayers {
		p.clickAndWait("#day-pass-btn")
	}

	ctx.logger.LogDB("after all players passed")

	// End Vote button should now be visible (all alive players have acted)
	has, endVoteBtn, _ := alivePlayers[0].p().Has("#day-end-vote-btn")
	if !has {
		ctx.logger.LogDB("FAIL: End Vote button not visible after all passed")
		t.Fatal("End Vote button should appear after all players pass")
	}
	alivePlayers[0].clickElementAndWait(endVoteBtn)

	ctx.logger.LogDB("after End Vote press")

	// Majority passed → no kill → transition to night (round 2)
	if !alivePlayers[0].isInNightPhase() {
		ctx.logger.LogDB("FAIL: not in night phase after majority pass")
		t.Fatal("Should transition to night after majority pass (no elimination)")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestDayVoteTransitionToNight(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing day vote transition to night ===")

	// Setup: 2 villagers, 1 werewolf - after night kill, 1 villager and 1 werewolf remain
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 2, 1)

	// Both alive players vote for each other (the remaining villager and werewolf)
	// With 2 alive players, majority is 2, so both must vote for same person
	// Let's have both vote for the werewolf
	villagers[1].dayVoteForPlayer(werewolves[0].Name)
	werewolves[0].dayVoteForPlayer(villagers[1].Name)

	ctx.logger.LogDB("after day votes")

	// With a split vote (1-1), no majority, should transition to night without elimination
	if villagers[1].isInDayPhase() {
		ctx.logger.LogDB("FAIL: still in day phase after all voted")
		t.Error("Should transition to night after all players voted")
	}

	// Should be in night 2 now
	if !villagers[1].isInNightPhase() {
		content := villagers[1].getGameContent()
		ctx.logger.LogDB("FAIL: not in night phase")
		t.Errorf("Should be in night phase. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestVillagersWinByEliminatingWerewolf(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing villagers win by eliminating werewolf ===")

	// Setup: 3 villagers, 1 werewolf - after night kill, 2 villagers and 1 werewolf remain
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 3, 1)

	// Both remaining villagers vote for the werewolf (majority of 2 out of 3)
	villagers[1].dayVoteForPlayer(werewolves[0].Name)
	villagers[2].dayVoteForPlayer(werewolves[0].Name)
	// Werewolf votes for a villager (won't matter)
	werewolves[0].dayVoteForPlayer(villagers[1].Name)

	ctx.logger.LogDB("after day elimination vote")

	// Game should be finished with villagers winning
	if !villagers[1].isGameFinished() {
		content := villagers[1].getGameContent()
		ctx.logger.LogDB("FAIL: game not finished")
		t.Errorf("Game should be finished after eliminating last werewolf. Content: %s", content)
		return
	}

	winner := villagers[1].getWinner()
	if winner != "villagers" {
		ctx.logger.LogDB("FAIL: wrong winner")
		t.Errorf("Villagers should win, got: %s", winner)
	}

	// History: elimination is public — everyone sees it
	eliminationEntry := werewolves[0].Name + " (Werewolf) was eliminated by the village"
	if !villagers[1].historyContains(eliminationEntry) {
		ctx.logger.LogDB("FAIL: villager cannot see elimination in history")
		t.Errorf("Villager should see elimination in history, got: %s", villagers[1].getHistoryText())
	}
	if !werewolves[0].historyContains(eliminationEntry) {
		ctx.logger.LogDB("FAIL: eliminated werewolf cannot see their own elimination in history")
		t.Errorf("Eliminated player should see their elimination in history")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolvesWinByEliminatingVillagers(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing werewolves win by eliminating villagers ===")

	// Setup: 2 villagers, 2 werewolves - after night kill, 1 villager and 2 werewolves remain
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 2, 2)

	// Both werewolves vote for the remaining villager
	werewolves[0].dayVoteForPlayer(villagers[1].Name)
	werewolves[1].dayVoteForPlayer(villagers[1].Name)
	// Villager votes for a werewolf (won't matter with 2v1)
	villagers[1].dayVoteForPlayer(werewolves[0].Name)

	ctx.logger.LogDB("after day elimination vote")

	// Game should be finished with werewolves winning
	if !werewolves[0].isGameFinished() {
		content := werewolves[0].getGameContent()
		ctx.logger.LogDB("FAIL: game not finished")
		t.Errorf("Game should be finished after eliminating last villager. Content: %s", content)
		return
	}

	winner := werewolves[0].getWinner()
	if winner != "werewolves" {
		ctx.logger.LogDB("FAIL: wrong winner")
		t.Errorf("Werewolves should win, got: %s", winner)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestNewGameReturnsToLobbyWithSameRoles(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing new game returns to lobby with same role counts ===")

	// Setup: 3 villagers, 1 werewolf (role config: Villager×3, Werewolf×1)
	// After night kill: villagers[0] is dead; alive = villagers[1], villagers[2], werewolves[0]
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 3, 1)

	// Vote out the werewolf → villagers win
	villagers[1].dayVoteForPlayer(werewolves[0].Name)
	villagers[2].dayVoteForPlayer(werewolves[0].Name)
	werewolves[0].dayVoteForPlayer(villagers[1].Name)

	ctx.logger.LogDB("after winning vote")

	if !villagers[1].isGameFinished() {
		t.Fatal("Game should be finished after eliminating the last werewolf")
	}

	// "Play Again" button should be visible
	if has, _, _ := villagers[1].p().Has("#btn-new-game"); !has {
		t.Fatal("Play Again button should be visible on the finished screen")
	}

	// Click "Play Again" — sends new_game action, server broadcasts lobby update to all players
	villagers[1].clickAndWait("#btn-new-game")
	ctx.logger.LogDB("after play again click")

	// All connected players should now see the lobby
	allPlayers := []*TestPlayer{villagers[0], villagers[1], villagers[2], werewolves[0]}
	for _, p := range allPlayers {
		err := p.waitUntilCondition(`() => document.querySelector('#btn-start') !== null`, "lobby loaded")
		if err != nil {
			ctx.logger.LogDB("FAIL: player not in lobby")
			t.Errorf("Player %s should be in lobby after new game: %v", p.Name, err)
		}
	}

	// Role counts must be preserved: Villager=3, Werewolf=1
	if count := villagers[1].getRoleCountByID(RoleVillager); count != "3" {
		ctx.logger.LogDB("FAIL: wrong villager count")
		t.Errorf("Expected Villager count 3 in new lobby, got %q", count)
	}
	if count := villagers[1].getRoleCountByID(RoleWerewolf); count != "1" {
		ctx.logger.LogDB("FAIL: wrong werewolf count")
		t.Errorf("Expected Werewolf count 1 in new lobby, got %q", count)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestNoEliminationOnTiedVote(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing no elimination on tied vote ===")

	// Setup: 3 villagers, 2 werewolves - after night kill, 2 villagers and 2 werewolves remain (4 alive)
	_, werewolves, villagers := setupDayPhaseGame(ctx, browser, 3, 2)

	// Create a tie: 2 votes for villager, 2 votes for werewolf
	werewolves[0].dayVoteForPlayer(villagers[1].Name)
	werewolves[1].dayVoteForPlayer(villagers[1].Name)
	villagers[1].dayVoteForPlayer(werewolves[0].Name)
	villagers[2].dayVoteForPlayer(werewolves[0].Name)

	ctx.logger.LogDB("after tied vote")

	// With a 2-2 tie, no majority (need 3), should transition to night without elimination
	// Should be in night 2
	if !villagers[1].isInNightPhase() {
		if villagers[1].isGameFinished() {
			ctx.logger.LogDB("FAIL: game ended on tie")
			t.Error("Game should not end on a tied vote")
		} else {
			content := villagers[1].getGameContent()
			ctx.logger.LogDB("FAIL: not in night after tie")
			t.Errorf("Should transition to night after tied vote. Content: %s", content)
		}
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestDeadPlayerCannotVote(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing dead player cannot vote ===")

	// Setup: 3 villagers, 1 werewolf - werewolf kills villager 0
	_, _, villagers := setupDayPhaseGame(ctx, browser, 3, 1)

	// villagers[0] is dead (killed at night)
	deadPlayer := villagers[0]

	// Dead player should NOT see vote buttons
	voteButtons := deadPlayer.getDayVoteButtons()
	if len(voteButtons) > 0 {
		ctx.logger.LogDB("FAIL: dead player sees vote buttons")
		t.Errorf("Dead player should not see vote buttons, but found: %v", voteButtons)
	}

	// Check that the page shows "You are dead and cannot vote"
	content := deadPlayer.getGameContent()
	if !strings.Contains(content, "dead") || !strings.Contains(content, "cannot vote") {
		ctx.logger.LogDB("FAIL: no dead message shown")
		t.Error("Dead player should see message that they cannot vote")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestCannotVoteForDeadPlayer(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing cannot vote for dead player ===")

	// Setup: 3 villagers, 1 werewolf - werewolf kills villager 0
	_, _, villagers := setupDayPhaseGame(ctx, browser, 3, 1)

	// villagers[0] is dead
	deadPlayerName := villagers[0].Name

	// Living player should NOT see dead player in vote buttons
	alivePlayer := villagers[1]
	voteButtons := alivePlayer.getDayVoteButtons()

	for _, name := range voteButtons {
		if name == deadPlayerName {
			ctx.logger.LogDB("FAIL: dead player in vote options")
			t.Errorf("Dead player %s should not be in vote options", deadPlayerName)
		}
	}

	// Should only see alive players (2 villagers + 1 werewolf = 3)
	expectedAlive := 3 // villagers[1], villagers[2], werewolves[0]
	if len(voteButtons) != expectedAlive {
		ctx.logger.LogDB("FAIL: wrong number of vote buttons")
		t.Errorf("Expected %d vote buttons for alive players, got %d: %v", expectedAlive, len(voteButtons), voteButtons)
	}

	ctx.logger.Debug("=== Test passed ===")
}
