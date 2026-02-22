package main

import (
	"fmt"
	"strings"
	"testing"
)

// ============================================================================
// Night Phase Test Helpers
// ============================================================================

// isInNightPhase checks if the player sees the night phase UI
func (tp *TestPlayer) isInNightPhase() bool {
	// Check the #phase-heading element for "Night" prefix
	result, _, err := tp.Page.Has("#night")
	isNight := err == nil && result
	if tp.logger != nil {
		tp.logger.Debug("[%s] Is in night phase: %v", tp.Name, isNight)
	}
	return isNight
}

// isInDayPhase checks if the player sees the day phase UI
func (tp *TestPlayer) isInDayPhase() bool {
	// Check the #phase-heading element for "Day" prefix
	result, _, err := tp.Page.Has("#day")
	isDay := err == nil && result
	if tp.logger != nil {
		tp.logger.Debug("[%s] Is in day phase: %v", tp.Name, isDay)
	}
	return isDay
}

// waitForDayPhase waits for the player to transition to day phase by listening to WebSocket messages
func (tp *TestPlayer) waitForDayPhase() error {
	checkJS := `(() => {
		const heading = document.querySelector('#phase-heading');
		return heading && heading.textContent.trim().startsWith('Day');
	})`
	return tp.waitUntilCondition(checkJS, "day phase")
}

// waitForNightPhase waits for the player to transition to night phase by listening to WebSocket messages
func (tp *TestPlayer) waitForNightPhase() error {
	checkJS := `(() => {
		const heading = document.querySelector('#phase-heading');
		return heading && heading.textContent.trim().startsWith('Night');
	})`
	return tp.waitUntilCondition(checkJS, "night phase")
}

// canSeeWerewolfVotes checks if the player can see the werewolf voting UI
func (tp *TestPlayer) canSeeWerewolfVotes() bool {
	html, _ := tp.Page.HTML()
	canSee := strings.Contains(html, "Choose a Victim") || strings.Contains(html, "Current Votes")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see werewolf votes: %v", tp.Name, canSee)
	}
	return canSee
}

// canSeeWerewolfPack checks if the player can see other werewolves
func (tp *TestPlayer) canSeeWerewolfPack() bool {
	html, _ := tp.Page.HTML()
	canSee := strings.Contains(html, "Your Pack")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Can see werewolf pack: %v", tp.Name, canSee)
	}
	return canSee
}

// getVoteButtons returns the names of players that can be voted for
func (tp *TestPlayer) getVoteButtons() []string {

	var names []string
	elements, err := tp.p().Elements(".vote-button")
	if err != nil {
		return names
	}
	for _, el := range elements {
		text := strings.TrimSpace(el.MustText())
		// Remove wolf emoji if present
		text = strings.TrimSuffix(text, " 🐺")
		text = strings.TrimSuffix(text, "🐺")
		text = strings.TrimSpace(text)
		if text != "" {
			names = append(names, text)
		}
	}
	if tp.logger != nil {
		tp.logger.Debug("[%s] Vote buttons: %v", tp.Name, names)
	}
	return names
}

// voteForPlayer clicks the vote button for a specific player
func (tp *TestPlayer) voteForPlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Voting for: %s", tp.Name, targetName)
	}

	// Find the button that contains this player's name
	buttons, err := tp.p().Elements(".vote-button")
	if err != nil {
		if tp.logger != nil {
			tp.logger.Debug("[%s] Failed to find vote buttons: %v", tp.Name, err)
		}
		return
	}

	for _, btn := range buttons {
		text := strings.TrimSpace(btn.MustText())
		if strings.Contains(text, targetName) {
			// Click and wait for WebSocket response
			tp.clickElementAndWait(btn)
			tp.logHTML("after voting for " + targetName)
			// Auto-press End Vote if all werewolves have now voted
			if has, endVoteBtn, _ := tp.p().Has("#werewolf-end-vote-btn"); has {
				tp.clickElementAndWait(endVoteBtn)
			}
			return
		}
	}

	if tp.logger != nil {
		tp.logger.Debug("[%s] Could not find vote button for: %s", tp.Name, targetName)
	}
}

// getCurrentVoteTarget returns who this player has currently voted for (from UI)
func (tp *TestPlayer) getCurrentVoteTarget() string {

	// Look for a selected button (non-blocking check)
	found, el, err := tp.p().Has(".vote-button.selected")
	if err != nil || !found {
		return ""
	}
	text := strings.TrimSpace(el.MustText())
	text = strings.TrimSuffix(text, " 🐺")
	text = strings.TrimSuffix(text, "🐺")
	text = strings.TrimSpace(text)
	if tp.logger != nil {
		tp.logger.Debug("[%s] Current vote target: %s", tp.Name, text)
	}
	return text
}

// getDeathAnnouncement returns the death announcement text if any
func (tp *TestPlayer) getDeathAnnouncement() string {
	found, el, err := tp.p().Has(".death-announcement")
	if err != nil || !found {
		return ""
	}
	text := el.MustText()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Death announcement: %s", tp.Name, text)
	}
	return text
}

// getGameContent returns the full game content HTML for debugging
func (tp *TestPlayer) getGameContent() string {
	el, err := tp.p().Element("#game-content")
	if err != nil {
		return ""
	}
	return el.MustText()
}

// setupNightPhaseGame creates a game with specified villagers and werewolves and starts it
// Returns the players array where werewolves are at the beginning
func setupNightPhaseGame(ctx *TestContext, browser *TestBrowser, numVillagers, numWerewolves int) []*TestPlayer {
	totalPlayers := numVillagers + numWerewolves

	var players []*TestPlayer
	for i := 0; i < totalPlayers; i++ {
		name := fmt.Sprintf("N%d", i+1)
		player := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, player)
	}

	// Add roles
	for i := 0; i < numVillagers; i++ {
		players[0].addRoleByID(RoleVillager)
	}
	for i := 0; i < numWerewolves; i++ {
		players[0].addRoleByID(RoleWerewolf)
	}

	// Start the game
	players[0].startGame()

	// Wait for transition to night phase on all players (to ensure all have received the update)
	for _, p := range players {
		err := p.waitForNightPhase()
		if err != nil {
			ctx.logger.Debug("Warning: timeout waiting for night phase on %s: %v", p.Name, err)
		}
	}

	ctx.logger.LogDB("after game start")

	return players
}

// findPlayersByRole returns players grouped by their role
func findPlayersByRole(players []*TestPlayer) (werewolves []*TestPlayer, villagers []*TestPlayer) {
	for _, p := range players {
		role := p.getRole()
		if role == "Werewolf" {
			werewolves = append(werewolves, p)
		} else {
			villagers = append(villagers, p)
		}
	}
	return
}

// ============================================================================
// Night Phase Tests
// ============================================================================

func TestVillagerCannotSeeWerewolfVotes(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing villager cannot see werewolf votes ===")

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
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing werewolf can vote ===")

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

	// Sound toast should NOT fire yet — only one of two werewolves has voted
	if villagers[0].hasSoundToast("werewolves have made their choice") {
		ctx.logger.LogDB("FAIL: sound toast fired before all werewolves voted")
		t.Errorf("Sound toast should not appear until all werewolves have voted")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolfCanChangeVote(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing werewolf can change vote ===")

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

func TestDayTransitionOnMajorityVote(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing day transition on majority vote ===")

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
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing stay in night without majority ===")

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
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing correct player gets killed ===")

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

	// Sound toast should fire on all players once all werewolves have voted
	soundMsg := "werewolves have made their choice"
	if !werewolves[0].hasSoundToast(soundMsg) {
		ctx.logger.LogDB("FAIL: werewolf did not receive sound toast")
		t.Errorf("Werewolf should see sound toast after voting completes")
	}
	if !villagers[0].hasSoundToast(soundMsg) {
		ctx.logger.LogDB("FAIL: villager did not receive sound toast")
		t.Errorf("Villager should see sound toast when werewolves finish voting")
	}
	if !target.hasSoundToast(soundMsg) {
		ctx.logger.LogDB("FAIL: target did not receive sound toast")
		t.Errorf("Target should also receive the sound toast")
	}

	// Check death announcement
	announcement := werewolves[0].getDeathAnnouncement()
	if !strings.Contains(announcement, target.Name) {
		ctx.logger.LogDB("FAIL: wrong player killed")
		t.Errorf("Death announcement should mention %s, got: %s", target.Name, announcement)
	}

	// Check that the target shows as dead in the sidebar using #is-dead-{playerID}
	if targetID == "" {
		t.Error("Could not determine target player ID")
	} else {
		deadSelector := "#is-dead-" + targetID
		has, _, _ := werewolves[0].p().Has(deadSelector)
		if !has {
			ctx.logger.LogDB("FAIL: victim not marked as dead in sidebar")
			t.Errorf("Victim %s should have death indicator in sidebar (%s)", target.Name, deadSelector)
		}
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWerewolvesVoteSplitNoKill(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing split vote stays in night ===")

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
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing werewolf can pass (no kill) ===")

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

	// Sound toast should NOT fire yet — End Vote not pressed
	soundMsg := "werewolves have made their choice"
	if villagers[0].hasSoundToast(soundMsg) {
		ctx.logger.LogDB("FAIL: sound toast fired before End Vote")
		t.Errorf("Sound toast should not appear until End Vote is pressed")
	}

	// End Vote button should now be visible (all wolves acted)
	has, endVoteBtn, _ := werewolf.p().Has("#werewolf-end-vote-btn")
	if !has {
		ctx.logger.LogDB("FAIL: End Vote button not visible after pass")
		t.Fatal("End Vote button should appear after werewolf passes")
	}
	werewolf.clickElementAndWait(endVoteBtn)

	ctx.logger.LogDB("after End Vote press")

	// Sound toast should now fire on all players
	if !werewolf.hasSoundToast(soundMsg) {
		ctx.logger.LogDB("FAIL: werewolf did not receive sound toast")
		t.Errorf("Werewolf should see sound toast after End Vote")
	}
	if !villagers[0].hasSoundToast(soundMsg) {
		ctx.logger.LogDB("FAIL: villager did not receive sound toast")
		t.Errorf("Villager should see sound toast after End Vote")
	}

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

func TestWitchHealSavesVictim(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

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

	var werewolves, villagers []*TestPlayer
	werewolves, villagers = findPlayersByRole(players)

	ctx.logger.Debug("=== Test: Witch heals victim ===")
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
	gameContent := witch.getGameContent()
	if !strings.Contains(gameContent, targetVillager.Name) {
		t.Errorf("Witch should see werewolf target name: %s", targetVillager.Name)
	}

	// Send heal action — click the first heal button
	witch.clickAndWait(".witch-heal-button")

	// Witch passes to end night
	witch.clickAndWait("#witch-pass-button")

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
	ctx := newTestContext(t)
	defer ctx.cleanup()

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

	ctx.logger.Debug("=== Test: Witch poison kills player ===")

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

	// Find poison button for otherVillager
	buttons, err := witch.p().Elements(".witch-kill-button")
	if err != nil {
		t.Fatalf("Failed to find poison buttons: %v", err)
	}
	for _, btn := range buttons {
		text := btn.MustText()
		if strings.Contains(text, otherVillager.Name) {
			witch.clickElementAndWait(btn)
			break
		}
	}

	// Witch passes - click the button by id
	witch.clickAndWait("#witch-pass-button")

	// Wait for phase transition to day (use a living player, not the poisoned one)
	err = witch.waitForDayPhase()
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

	// History: witch poison is actor-only — only the witch sees it
	poisonEntry := "You poisoned " + otherVillager.Name
	if !witch.historyContains(poisonEntry) {
		ctx.logger.LogDB("FAIL: witch cannot see own poison in history")
		t.Errorf("Witch should see their poison in history, got: %s", witch.getHistoryText())
	}
	if werewolves[0].historyContains(poisonEntry) {
		ctx.logger.LogDB("FAIL: werewolf can see witch poison in history")
		t.Errorf("Werewolf should not see witch poison in history")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestWitchPassEndNight(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

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

	ctx.logger.Debug("=== Test: Witch pass ends night ===")

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

	// Witch just passes without using potions
	witch.clickAndWait("#witch-pass-button")

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

	// History: witch pass is actor-only — only the witch sees it
	passEntry := "Witch " + witch.Name + " passed"
	if !witch.historyContains(passEntry) {
		ctx.logger.LogDB("FAIL: witch cannot see own pass in history")
		t.Errorf("Witch should see their pass in history, got: %s", witch.getHistoryText())
	}
	if werewolves[0].historyContains(passEntry) {
		ctx.logger.LogDB("FAIL: werewolf can see witch pass in history")
		t.Errorf("Werewolf should not see witch pass in history")
	}

	ctx.logger.Debug("=== Test passed ===")
}

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

// canSeeMasonList checks if the player's page shows any mason-player elements
func (tp *TestPlayer) canSeeMasonList() bool {
	elements, err := tp.p().Elements(".mason-player")
	return err == nil && len(elements) > 0
}

// ============================================================================
// Mason Tests
// ============================================================================

func TestMasonsKnowEachOther(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

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
	ctx.logger.Debug("=== Test: Masons know each other ===")
	ctx.logger.Debug("Werewolves: %v, Villagers: %v, Masons: %v",
		playerNames(werewolves), playerNames(villagers), playerNames(masons))

	if len(masons) < 2 {
		t.Fatalf("Need at least 2 masons, got %d", len(masons))
	}

	mason1 := masons[0]
	mason2 := masons[1]

	// Mason1 should see mason2's name
	content1 := mason1.getGameContent()
	if !strings.Contains(content1, mason2.Name) {
		ctx.logger.LogDB("FAIL: mason1 cannot see mason2")
		t.Errorf("Mason '%s' should see fellow mason '%s'. Content: %s", mason1.Name, mason2.Name, content1)
	}
	if !mason1.canSeeMasonList() {
		t.Errorf("Mason '%s' should see mason list", mason1.Name)
	}

	// Mason2 should see mason1's name
	content2 := mason2.getGameContent()
	if !strings.Contains(content2, mason1.Name) {
		ctx.logger.LogDB("FAIL: mason2 cannot see mason1")
		t.Errorf("Mason '%s' should see fellow mason '%s'. Content: %s", mason2.Name, mason1.Name, content2)
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
	ctx := newTestContext(t)
	defer ctx.cleanup()

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
	ctx.logger.Debug("=== Test: Single mason sees no others ===")

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
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Wolf Cub night kill triggers double kill ===")

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

	// Second kill vote — click vote2 button
	vote2Buttons, err := werewolf.p().Elements("[id^='vote2-btn-']")
	if err != nil || len(vote2Buttons) == 0 {
		ctx.logger.LogDB("FAIL: no vote2 buttons found")
		t.Fatal("Should see vote2 buttons for second victim")
	}
	for _, btn := range vote2Buttons {
		text := strings.TrimSpace(btn.MustText())
		if strings.Contains(text, victim2.Name) {
			werewolf.clickElementAndWait(btn)
			break
		}
	}
	if has, endVote2Btn, _ := werewolf.p().Has("#werewolf-end-vote2-btn"); has {
		werewolf.clickElementAndWait(endVote2Btn)
	}

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
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing witch saves second victim in Wolf Cub double kill ===")

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
	// Witch passes to end the night
	witch.clickAndWait("#witch-pass-button")

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

	vote2Buttons, err := werewolf.p().Elements("[id^='vote2-btn-']")
	if err != nil || len(vote2Buttons) == 0 {
		ctx.logger.LogDB("FAIL: no vote2 buttons found")
		t.Fatal("Should see vote2 buttons for second victim")
	}
	for _, btn := range vote2Buttons {
		if strings.Contains(strings.TrimSpace(btn.MustText()), victim2.Name) {
			werewolf.clickElementAndWait(btn)
			break
		}
	}
	if has, endVote2Btn, _ := werewolf.p().Has("#werewolf-end-vote2-btn"); has {
		werewolf.clickElementAndWait(endVote2Btn)
	}

	// Witch should see both victims listed
	witchContent := witch.getGameContent()
	if !strings.Contains(witchContent, victim1.Name) {
		ctx.logger.LogDB("FAIL: witch cannot see victim1")
		t.Errorf("Witch should see victim1 '%s'", victim1.Name)
	}
	if !strings.Contains(witchContent, victim2.Name) {
		ctx.logger.LogDB("FAIL: witch cannot see victim2")
		t.Errorf("Witch should see victim2 '%s'", victim2.Name)
	}

	// Witch saves victim2 specifically by clicking their heal button
	healButtons, err := witch.p().Elements(".witch-heal-button")
	if err != nil || len(healButtons) == 0 {
		ctx.logger.LogDB("FAIL: no heal buttons found")
		t.Fatal("Witch should see heal buttons")
	}
	savedVictim2 := false
	for _, btn := range healButtons {
		if strings.Contains(strings.TrimSpace(btn.MustText()), victim2.Name) {
			witch.clickElementAndWait(btn)
			savedVictim2 = true
			break
		}
	}
	if !savedVictim2 {
		t.Fatalf("Could not find heal button for victim2 '%s'", victim2.Name)
	}

	// Witch passes to end the night
	witch.clickAndWait("#witch-pass-button")

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
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing Wolf Cub day elimination triggers double kill ===")

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

	vote2Buttons, err := werewolf.p().Elements("[id^='vote2-btn-']")
	if err != nil || len(vote2Buttons) == 0 {
		ctx.logger.LogDB("FAIL: no vote2 buttons found")
		t.Fatal("Should see vote2 buttons for second victim")
	}
	for _, btn := range vote2Buttons {
		text := strings.TrimSpace(btn.MustText())
		if strings.Contains(text, victim2.Name) {
			werewolf.clickElementAndWait(btn)
			break
		}
	}
	if has, endVote2Btn, _ := werewolf.p().Has("#werewolf-end-vote2-btn"); has {
		werewolf.clickElementAndWait(endVote2Btn)
	}

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

// ============================================================================
// Cupid Test Helpers
// ============================================================================

func findPlayersByRoleWithCupid(players []*TestPlayer) (werewolves, villagers, cupids []*TestPlayer) {
	for _, p := range players {
		role := p.getRole()
		switch role {
		case "Werewolf":
			werewolves = append(werewolves, p)
		case "Cupid":
			cupids = append(cupids, p)
		default:
			villagers = append(villagers, p)
		}
	}
	return
}

// canSeeCupidUI checks if the player sees the Cupid lover-linking UI
func (tp *TestPlayer) canSeeCupidUI() bool {
	html, _ := tp.Page.HTML()
	return strings.Contains(html, "Link Two Lovers")
}

// getLoverInfo returns the sidebar player entry text for the viewer's lover.
// The entry is identified by a span with id "lover-of-{partnerID}".
// Returns "" if no lover is visible in the sidebar.
func (tp *TestPlayer) getLoverInfo() string {
	lis, err := tp.p().Elements("#player-list li")
	if err != nil {
		return ""
	}
	for _, li := range lis {
		found, _, _ := li.Has("span[id^='lover-of-']")
		if found {
			return strings.TrimSpace(li.MustText())
		}
	}
	return ""
}

// cupidPickLover clicks the cupid button to pick a lover
func (tp *TestPlayer) cupidPickLover(targetName string) {
	elements, err := tp.p().Elements(".cupid-button")
	if err != nil {
		return
	}
	for _, el := range elements {
		text := strings.TrimSpace(el.MustText())
		if text == targetName {
			tp.clickElementAndWait(el)
			tp.logHTML(fmt.Sprintf("after cupid pick: %s", targetName))
			return
		}
	}
}

// ============================================================================
// Cupid Tests
// ============================================================================

// TestCupidLinksLovers verifies Cupid can link two players, night resolves only after Cupid acts,
// and both lovers can see each other's identity.
func TestCupidLinksLovers(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 1 Cupid + 2 Villagers + 2 Werewolves = 5 players
	var players []*TestPlayer
	for _, name := range []string{"C1", "C2", "C3", "C4", "C5"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleCupid)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	werewolves, villagers, cupids := findPlayersByRoleWithCupid(players)
	ctx.logger.Debug("Cupids: %v, Werewolves: %v, Villagers: %v",
		playerNames(cupids), playerNames(werewolves), playerNames(villagers))

	if len(cupids) == 0 || len(werewolves) == 0 || len(villagers) < 2 {
		t.Skip("Role assignment didn't produce expected roles")
	}

	cupid := cupids[0]

	// Cupid should see the linking UI
	if !cupid.canSeeCupidUI() {
		ctx.logger.LogDB("FAIL: cupid UI not visible")
		t.Fatal("Cupid should see the link lovers UI")
	}

	// Werewolves vote to kill a villager
	victim := villagers[0]
	for _, w := range werewolves {
		w.voteForPlayer(victim.Name)
	}

	// Night should NOT resolve yet — Cupid hasn't linked lovers
	if werewolves[0].isInDayPhase() {
		t.Fatal("Night should not resolve until Cupid links lovers")
	}

	// Cupid picks two lovers (the two villagers)
	lover1 := villagers[0]
	lover2 := villagers[1]
	cupid.cupidPickLover(lover1.Name)

	// After first pick, night still shouldn't resolve
	if werewolves[0].isInDayPhase() {
		t.Fatal("Night should not resolve after only first lover is picked")
	}

	// Cupid picks second lover
	cupid.cupidPickLover(lover2.Name)

	// Night should now resolve (werewolves already voted, Cupid done)
	if !werewolves[0].isInDayPhase() {
		ctx.logger.LogDB("FAIL: not in day after Cupid links")
		t.Fatal("Night should resolve after Cupid links lovers")
	}

	// Both lovers should see each other's name in the sidebar player list (💞 indicator)
	if !strings.Contains(lover1.getLoverInfo(), lover2.Name) {
		t.Errorf("Lover1 (%s) should see Lover2's name in sidebar, got: %s", lover1.Name, lover1.getLoverInfo())
	}
	if !strings.Contains(lover2.getLoverInfo(), lover1.Name) {
		t.Errorf("Lover2 (%s) should see Lover1's name in sidebar, got: %s", lover2.Name, lover2.getLoverInfo())
	}

	// Non-lovers should NOT see the 💞 indicator
	for _, w := range werewolves {
		if w.getLoverInfo() != "" {
			t.Errorf("Non-lover werewolf (%s) should not see lover info, got: %s", w.Name, w.getLoverInfo())
		}
	}

	// History: lover notifications are actor-only — each lover sees their own, others don't
	lover1Entry := "Your lover is " + lover2.Name
	if !lover1.historyContains(lover1Entry) {
		ctx.logger.LogDB("FAIL: lover1 cannot see their lover notification in history")
		t.Errorf("Lover1 should see their lover notification in history, got: %s", lover1.getHistoryText())
	}
	lover2Entry := "Your lover is " + lover1.Name
	if !lover2.historyContains(lover2Entry) {
		ctx.logger.LogDB("FAIL: lover2 cannot see their lover notification in history")
		t.Errorf("Lover2 should see their lover notification in history, got: %s", lover2.getHistoryText())
	}
	for _, w := range werewolves {
		if w.historyContains(lover1Entry) || w.historyContains(lover2Entry) {
			t.Errorf("Werewolf (%s) should not see lover notifications in history", w.Name)
		}
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestHeartbreakOnNightKill verifies that when a lover is killed at night,
// their partner dies from heartbreak and both deaths appear in the morning.
func TestHeartbreakOnNightKill(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 1 Cupid + 2 Villagers + 2 Werewolves
	var players []*TestPlayer
	for _, name := range []string{"H1", "H2", "H3", "H4", "H5"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleCupid)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	werewolves, villagers, cupids := findPlayersByRoleWithCupid(players)
	if len(cupids) == 0 || len(werewolves) == 0 || len(villagers) < 2 {
		t.Skip("Role assignment didn't produce expected roles")
	}

	cupid := cupids[0]
	lover1 := villagers[0]
	lover2 := villagers[1]

	// Cupid links the two villagers as lovers
	cupid.cupidPickLover(lover1.Name)
	cupid.cupidPickLover(lover2.Name)

	// Werewolves kill lover1
	for _, w := range werewolves {
		w.voteForPlayer(lover1.Name)
	}

	// Day should begin
	if !werewolves[0].isInDayPhase() {
		ctx.logger.LogDB("FAIL: not in day phase")
		t.Fatal("Should be in day phase")
	}

	// Both lover1 AND lover2 should appear in the death announcement
	announcement := werewolves[0].getDeathAnnouncement()
	ctx.logger.Debug("Death announcement: %s", announcement)
	if !strings.Contains(announcement, lover1.Name) {
		t.Errorf("Announcement should mention lover1 (%s) killed by werewolves", lover1.Name)
	}
	if !strings.Contains(announcement, lover2.Name) {
		t.Errorf("Announcement should mention lover2 (%s) dying from heartbreak", lover2.Name)
	}

	// History: heartbreak is public — all players see it
	heartbreakEntry := "died of heartbreak after their lover " + lover1.Name + " was killed"
	if !werewolves[0].historyContains(heartbreakEntry) {
		ctx.logger.LogDB("FAIL: werewolf cannot see heartbreak in history")
		t.Errorf("Werewolf should see heartbreak in history (public action), got: %s", werewolves[0].getHistoryText())
	}
	if !cupid.historyContains(heartbreakEntry) {
		ctx.logger.LogDB("FAIL: cupid cannot see heartbreak in history")
		t.Errorf("Cupid should see heartbreak in history (public action), got: %s", cupid.getHistoryText())
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestLoversWinCondition verifies that when only the two lovers remain alive,
// they win together (even if on opposite teams).
func TestLoversWinCondition(t *testing.T) {
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Setup: 1 Cupid + 1 Villager + 1 Werewolf = 3 players
	// Cupid links the villager and werewolf, then is killed. Only the two lovers remain.
	var players []*TestPlayer
	for _, name := range []string{"L1", "L2", "L3"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}

	players[0].addRoleByID(RoleCupid)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	werewolves, villagers, cupids := findPlayersByRoleWithCupid(players)
	if len(cupids) == 0 || len(werewolves) == 0 || len(villagers) == 0 {
		t.Skip("Role assignment didn't produce expected roles")
	}

	cupid := cupids[0]
	villager := villagers[0]
	werewolf := werewolves[0]

	// Cupid links the villager and the werewolf as lovers
	cupid.cupidPickLover(villager.Name)
	cupid.cupidPickLover(werewolf.Name)

	// Werewolf kills Cupid (the non-lover)
	werewolf.voteForPlayer(cupid.Name)

	// With only 2 players alive (villager + werewolf lovers), lovers win immediately
	// after win conditions are checked during the day phase

	html, _ := villager.Page.HTML()
	ctx.logger.Debug("Villager page after night: %s", html[:minInt(len(html), 500)])

	// The game might transition to day, then immediately end due to lovers win.
	// Try voting if still in day phase.
	if !strings.Contains(html, "lovers") && !strings.Contains(html, "Lovers") {
		if villager.isInDayPhase() {
			// Tie vote → no elimination → transition to night → win check should trigger
			villager.dayVoteForPlayer(werewolf.Name)
			werewolf.dayVoteForPlayer(villager.Name)
			html, _ = villager.Page.HTML()
		}
	}

	if !strings.Contains(html, "lovers") && !strings.Contains(html, "Lovers") {
		ctx.logger.LogDB("FAIL: lovers win condition not triggered")
		t.Errorf("Expected lovers win, got page: %s", html[:minInt(len(html), 500)])
	}

	ctx.logger.Debug("=== Test passed ===")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
