package main

import (
	"strings"
	"testing"
)

func TestVillagerCannotSeeWerewolfVotes(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing villager cannot see werewolf votes ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 2 villagers, 1 werewolf
	players := setupNightPhaseGame(ctx, browser, 2, 1)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Failed to find werewolves and villagers")
	}

	ctx.logger.Debug("Werewolf: %s, Villagers: %s, %s",
		werewolves[0].Name, villagers[0].Name, villagers[1].Name)

	// Check villager cannot see voting UI

	if villagers[0].canSeeWerewolfVotes() {
		ctx.logger.LogDB("FAIL: villager can see werewolf votes")
		t.Error("Villager should not be able to see werewolf voting UI")
	}

	if villagers[0].canSeeWerewolfPack() {
		ctx.logger.LogDB("FAIL: villager can see werewolf pack")
		t.Error("Villager should not be able to see werewolf pack")
	}

	// Check werewolf CAN see voting UI

	if !werewolves[0].canSeeWerewolfVotes() {
		ctx.logger.LogDB("FAIL: werewolf cannot see votes")
		t.Error("Werewolf should be able to see voting UI")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolfCanVote(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing werewolf can vote ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 1 villager, 2 werewolves (need 2 so first vote doesn't resolve immediately)
	players := setupNightPhaseGame(ctx, browser, 1, 2)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) < 2 || len(villagers) == 0 {
		t.Fatal("Failed to find enough werewolves and villagers")
	}

	ctx.logger.Debug("Werewolves: %s, %s", werewolves[0].Name, werewolves[1].Name)

	// Werewolf should see vote buttons
	voteOptions := werewolves[0].getVoteButtons()
	if len(voteOptions) == 0 {
		ctx.logger.LogDB("FAIL: no vote buttons found")
		t.Fatal("Werewolf should see vote buttons")
	}

	ctx.logger.Debug("Vote options: %v", voteOptions)

	// First werewolf votes (doesn't resolve yet - need majority)
	targetName := villagers[0].Name
	werewolves[0].voteForPlayer(targetName)

	// Verify vote was recorded (check selected button)
	currentTarget := werewolves[0].getCurrentVoteTarget()
	if currentTarget != targetName {
		ctx.logger.LogDB("FAIL: vote not recorded")
		t.Errorf("Expected vote for %s, got %s", targetName, currentTarget)
	}

	// History: werewolf vote is team-visible — werewolves see it, villagers do not
	voteEntry := "voted to kill " + targetName
	if !werewolves[0].historyContains(voteEntry) {
		ctx.logger.LogDB("FAIL: voting werewolf cannot see own vote in history")
		t.Errorf("Voting werewolf should see their vote in history")
	}
	if !werewolves[1].historyContains(voteEntry) {
		ctx.logger.LogDB("FAIL: non-voting werewolf cannot see team vote in history")
		t.Errorf("Non-voting werewolf should see team vote in history")
	}
	if villagers[0].historyContains(voteEntry) {
		ctx.logger.LogDB("FAIL: villager can see werewolf vote in history")
		t.Errorf("Villager should not see werewolf vote in history")
	}

	// Toast should NOT fire yet — only one of two werewolves has voted
	if villagers[0].hasToast("werewolves have made their choice") {
		ctx.logger.LogDB("FAIL: toast fired before all werewolves voted")
		t.Errorf("Toast should not appear until all werewolves have voted")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolfVoteCountsShownOnCards(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing werewolf vote counts on cards ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// 1 villager, 2 werewolves — first wolf's vote won't resolve night (need End Vote)
	players := setupNightPhaseGame(ctx, browser, 1, 2)
	werewolves, villagers := findPlayersByRole(players)
	if len(werewolves) < 2 || len(villagers) == 0 {
		t.Fatal("Need at least 2 werewolves and 1 villager")
	}

	targetName := villagers[0].Name
	ctx.logger.Debug("Werewolves: %s, %s. Target: %s", werewolves[0].Name, werewolves[1].Name, targetName)

	// Before any vote: count should be 0
	if got := werewolves[0].getWerewolfVoteCount(targetName); got != "0" {
		t.Errorf("Expected count 0 before any vote, got %s", got)
	}

	// Wolf 1 votes for villager (won't auto-resolve: wolf 2 hasn't voted yet)
	werewolves[0].voteForPlayer(targetName)

	// Both werewolves should now see count=1 on the villager's card
	if got := werewolves[0].getWerewolfVoteCount(targetName); got != "1" {
		ctx.logger.LogDB("FAIL: wrong vote count after wolf 1 votes")
		t.Errorf("Wolf 1 page: expected count 1 after first vote, got %s", got)
	}

	if got := werewolves[1].getWerewolfVoteCount(targetName); got != "1" {
		ctx.logger.LogDB("FAIL: wrong vote count on wolf 2 page")
		t.Errorf("Wolf 2 page: expected count 1 after wolf 1 voted, got %s", got)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolfCanChangeVote(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing werewolf can change vote ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 2 villagers, 2 werewolves (need 2 werewolves so vote doesn't resolve, 2 villagers to switch between)
	players := setupNightPhaseGame(ctx, browser, 2, 2)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) < 2 || len(villagers) < 2 {
		t.Fatal("Failed to find enough players")
	}

	ctx.logger.Debug("Werewolves: %s, %s, Villagers: %s, %s",
		werewolves[0].Name, werewolves[1].Name, villagers[0].Name, villagers[1].Name)

	// First vote (only first werewolf votes, doesn't resolve)
	firstTarget := villagers[0].Name
	werewolves[0].voteForPlayer(firstTarget)

	currentTarget := werewolves[0].getCurrentVoteTarget()
	if currentTarget != firstTarget {
		ctx.logger.LogDB("FAIL: first vote not recorded")
		t.Errorf("Expected first vote for %s, got %s", firstTarget, currentTarget)
	}

	// Change vote
	secondTarget := villagers[1].Name
	werewolves[0].voteForPlayer(secondTarget)

	newTarget := werewolves[0].getCurrentVoteTarget()
	if newTarget != secondTarget {
		ctx.logger.LogDB("FAIL: vote change not recorded")
		t.Errorf("Expected changed vote for %s, got %s", secondTarget, newTarget)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolfCanUnselectVote(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing werewolf can unselect vote ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// 1 villager, 2 werewolves so first vote doesn't auto-resolve
	players := setupNightPhaseGame(ctx, browser, 1, 2)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) < 2 || len(villagers) == 0 {
		t.Fatal("Failed to find enough players")
	}

	targetName := villagers[0].Name
	ctx.logger.Debug("Werewolves: %s, %s. Target: %s", werewolves[0].Name, werewolves[1].Name, targetName)

	// Vote for the villager
	werewolves[0].voteForPlayer(targetName)

	if got := werewolves[0].getCurrentVoteTarget(); got != targetName {
		ctx.logger.LogDB("FAIL: vote not recorded")
		t.Fatalf("Expected vote for %s, got %q", targetName, got)
	}
	if got := werewolves[0].getWerewolfVoteCount(targetName); got != "1" {
		t.Errorf("Expected count 1 after voting, got %s", got)
	}

	// Click same card again to unselect
	werewolves[0].voteForPlayer(targetName)

	if got := werewolves[0].getCurrentVoteTarget(); got != "" {
		ctx.logger.LogDB("FAIL: vote not cleared after unselect")
		t.Errorf("Expected no vote after unselect, got %q", got)
	}
	if got := werewolves[0].getWerewolfVoteCount(targetName); got != "0" {
		ctx.logger.LogDB("FAIL: vote count not decremented after unselect")
		t.Errorf("Expected count 0 after unselect, got %s", got)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestDayTransitionOnMajorityVote(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing day transition on majority vote ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 1 villager, 1 werewolf (simplest case - 1 werewolf = automatic majority)
	players := setupNightPhaseGame(ctx, browser, 2, 1)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Failed to find werewolves and villagers")
	}

	ctx.logger.Debug("Werewolf: %s, Villager: %s", werewolves[0].Name, villagers[0].Name)

	// Verify we're in night phase
	if !werewolves[0].isInNightPhase() {
		ctx.logger.LogDB("FAIL: not in night phase")
		t.Fatal("Should start in night phase")
	}

	// Werewolf votes for villager
	targetName := villagers[0].Name
	werewolves[0].voteForPlayer(targetName)

	submitNightSurveysForAllPlayers(players)

	// Wait for phase transition to day (second WebSocket message after vote)
	err := werewolves[0].waitForDayPhase()
	if err != nil {
		ctx.logger.Debug("Warning: timeout waiting for day phase after vote: %v", err)
	}

	ctx.logger.LogDB("after werewolf vote")

	// Check transition to day

	if !werewolves[0].isInDayPhase() {
		content := werewolves[0].getGameContent()
		ctx.logger.LogDB("FAIL: did not transition to day")
		t.Errorf("Should transition to day phase after werewolf majority vote. Content: %s", content)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestStayInNightWithoutMajority(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing stay in night without majority ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 1 villager, 2 werewolves (need both to vote for majority)
	players := setupNightPhaseGame(ctx, browser, 1, 2)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) < 2 || len(villagers) == 0 {
		t.Fatal("Failed to find enough players")
	}

	ctx.logger.Debug("Werewolves: %s, %s, Villager: %s",
		werewolves[0].Name, werewolves[1].Name, villagers[0].Name)

	// Only first werewolf votes
	targetName := villagers[0].Name
	werewolves[0].voteForPlayer(targetName)

	ctx.logger.LogDB("after first werewolf vote")

	// Should still be in night phase (need both werewolves to vote)

	if werewolves[0].isInDayPhase() {
		ctx.logger.LogDB("FAIL: transitioned to day too early")
		t.Error("Should NOT transition to day until all werewolves have voted")
	}

	if !werewolves[0].isInNightPhase() {
		ctx.logger.LogDB("FAIL: not in night phase")
		t.Error("Should still be in night phase")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestCorrectPlayerGetsKilled(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing correct player gets killed ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 2 villagers, 1 werewolf
	players := setupNightPhaseGame(ctx, browser, 2, 1)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) == 0 || len(villagers) < 2 {
		t.Fatal("Failed to find enough players")
	}

	// Choose specific target
	target := villagers[0]
	targetID := target.getPlayerID()
	ctx.logger.Debug("Target for kill: %s (ID: %s)", target.Name, targetID)

	// Werewolf votes
	werewolves[0].voteForPlayer(target.Name)

	ctx.logger.LogDB("after werewolf vote")

	// Toast should fire on all players once all werewolves have voted
	soundMsg := "werewolves have made their choice"
	if !werewolves[0].hasToast(soundMsg) {
		ctx.logger.LogDB("FAIL: werewolf did not receive toast")
		t.Errorf("Werewolf should see toast after voting completes")
	}
	if !villagers[0].hasToast(soundMsg) {
		ctx.logger.LogDB("FAIL: villager did not receive toast")
		t.Errorf("Villager should see toast when werewolves finish voting")
	}
	if !target.hasToast(soundMsg) {
		ctx.logger.LogDB("FAIL: target did not receive toast")
		t.Errorf("Target should also receive the toast")
	}

	submitNightSurveysForAllPlayers(players)

	// Check death announcement
	announcement := werewolves[0].getDeathAnnouncement()
	if !strings.Contains(announcement, target.Name) {
		ctx.logger.LogDB("FAIL: wrong player killed")
		t.Errorf("Death announcement should mention %s, got: %s", target.Name, announcement)
	}

	// Check that the target shows as dead in the sidebar (alive="false" on the .player-card)
	if targetID == "" {
		t.Error("Could not determine target player ID")
	} else {
		deadSelector := ".player-card[data-player-id='" + targetID + "'][alive=false]"
		has, _, _ := werewolves[0].p().Has(deadSelector)
		if !has {
			ctx.logger.LogDB("FAIL: victim not marked as dead in sidebar")
			t.Errorf("Victim %s should have death indicator in sidebar (%s)", target.Name, deadSelector)
		}
	}

	// Night death should appear in history for everyone (public)
	// The record format is "Night 1: NAME (ROLE) was found dead"
	deathEntry := "Night 1: " + target.Name + " (" + target.getRole() + ") was found dead"
	if !werewolves[0].historyContains(deathEntry) {
		ctx.logger.LogDB("FAIL: night death not in werewolf history")
		t.Errorf("Night death should be visible in werewolf history, got: %s", werewolves[0].getHistoryText())
	}
	if !villagers[0].historyContains(deathEntry) {
		ctx.logger.LogDB("FAIL: night death not in villager history")
		t.Errorf("Night death should be visible in villager history, got: %s", villagers[0].getHistoryText())
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolvesVoteSplitNoKill(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing split vote stays in night ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 2 villagers, 2 werewolves (they can split their vote)
	players := setupNightPhaseGame(ctx, browser, 2, 2)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) < 2 || len(villagers) < 2 {
		t.Fatal("Failed to find enough players")
	}

	ctx.logger.Debug("Werewolves: %s, %s", werewolves[0].Name, werewolves[1].Name)
	ctx.logger.Debug("Villagers: %s, %s", villagers[0].Name, villagers[1].Name)

	// Werewolves vote for different targets — second vote triggers End Vote auto-press
	werewolves[0].voteForPlayer(villagers[0].Name)
	werewolves[1].voteForPlayer(villagers[1].Name)

	ctx.logger.LogDB("after split vote + End Vote")

	submitNightSurveysForAllPlayers(players)

	// Split vote resolves as no kill — should transition to day with no death announcement
	if !werewolves[0].isInDayPhase() {
		ctx.logger.LogDB("FAIL: did not transition to day after split vote End Vote")
		t.Error("Should transition to day after End Vote with split vote (no kill)")
	}

	has, _, _ := werewolves[0].p().Has("#no-death-message")
	if !has {
		ctx.logger.LogDB("FAIL: no 'no death' message shown")
		t.Error("Should show 'no one died' message when split vote results in no kill")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolfCanPass(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing werewolf can pass (no kill) ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 2 villagers, 1 werewolf
	players := setupNightPhaseGame(ctx, browser, 2, 1)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) == 0 || len(villagers) < 2 {
		t.Fatal("Failed to find enough players")
	}

	werewolf := werewolves[0]

	// Werewolf presses Pass
	werewolf.clickAndWait("#werewolf-pass-btn")

	ctx.logger.LogDB("after werewolf pass")

	// Toast should NOT fire yet — End Vote not pressed
	soundMsg := "werewolves have made their choice"
	if villagers[0].hasToast(soundMsg) {
		ctx.logger.LogDB("FAIL: toast fired before End Vote")
		t.Errorf("Toast should not appear until End Vote is pressed")
	}

	// End Vote button should now be visible (all wolves acted)
	has, endVoteBtn, _ := werewolf.p().Has("#werewolf-end-vote-btn")
	if !has {
		ctx.logger.LogDB("FAIL: End Vote button not visible after pass")
		t.Fatal("End Vote button should appear after werewolf passes")
	}
	werewolf.clickElementAndWait(endVoteBtn)

	ctx.logger.LogDB("after End Vote press")

	// Toast should now fire on all players
	if !werewolf.hasToast(soundMsg) {
		ctx.logger.LogDB("FAIL: werewolf did not receive toast")
		t.Errorf("Werewolf should see toast after End Vote")
	}
	if !villagers[0].hasToast(soundMsg) {
		ctx.logger.LogDB("FAIL: villager did not receive toast")
		t.Errorf("Villager should see toast after End Vote")
	}

	submitNightSurveysForAllPlayers(players)

	// Should transition to day with no kills
	if !werewolf.isInDayPhase() {
		ctx.logger.LogDB("FAIL: not in day phase after pass + End Vote")
		t.Fatal("Should be in day phase after werewolf passes and presses End Vote")
	}
	hasNoKill, _, _ := werewolf.p().Has("#no-death-message")
	if !hasNoKill {
		ctx.logger.LogDB("FAIL: no 'no death' message shown")
		t.Error("Should show 'no one died' message when werewolf passes")
	}

	ctx.logger.Debug("=== Test passed ===")
}
