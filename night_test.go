package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// Night Phase Test Helpers
// ============================================================================

// isInNightPhase checks if the player sees the night phase UI
func (tp *TestPlayer) isInNightPhase() bool {
	html, _ := tp.Page.HTML()
	isNight := strings.Contains(html, "Night 1") || strings.Contains(html, "Night 2")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Is in night phase: %v", tp.Name, isNight)
	}
	return isNight
}

// isInDayPhase checks if the player sees the day phase UI
func (tp *TestPlayer) isInDayPhase() bool {
	html, _ := tp.Page.HTML()
	isDay := strings.Contains(html, "Day 1") || strings.Contains(html, "Day 2")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Is in day phase: %v", tp.Name, isDay)
	}
	return isDay
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
	tp.reload()
	time.Sleep(20 * time.Millisecond)

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
			btn.MustClick()
			time.Sleep(20 * time.Millisecond)
			tp.logHTML("after voting for " + targetName)
			return
		}
	}

	if tp.logger != nil {
		tp.logger.Debug("[%s] Could not find vote button for: %s", tp.Name, targetName)
	}
}

// getCurrentVoteTarget returns who this player has currently voted for (from UI)
func (tp *TestPlayer) getCurrentVoteTarget() string {
	tp.reload()
	time.Sleep(20 * time.Millisecond)

	// Look for a selected button
	el, err := tp.p().Element(".vote-button.selected")
	if err != nil {
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
	el, err := tp.p().Element(".death-announcement")
	if err != nil {
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
		player.waitForGame()
		players = append(players, player)
	}

	time.Sleep(20 * time.Millisecond)

	// Add roles
	for i := 0; i < numVillagers; i++ {
		players[0].addRoleByID(RoleVillager)
	}
	for i := 0; i < numWerewolves; i++ {
		players[0].addRoleByID(RoleWerewolf)
	}

	time.Sleep(20 * time.Millisecond)
	players[0].reload()

	// Start the game
	players[0].startGame()
	time.Sleep(20 * time.Millisecond)

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
	villagers[0].reload()
	time.Sleep(20 * time.Millisecond)

	if villagers[0].canSeeWerewolfVotes() {
		ctx.logger.LogDB("FAIL: villager can see werewolf votes")
		t.Error("Villager should not be able to see werewolf voting UI")
	}

	if villagers[0].canSeeWerewolfPack() {
		ctx.logger.LogDB("FAIL: villager can see werewolf pack")
		t.Error("Villager should not be able to see werewolf pack")
	}

	// Check werewolf CAN see voting UI
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)

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
	players := setupNightPhaseGame(ctx, browser, 1, 1)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) == 0 || len(villagers) == 0 {
		t.Fatal("Failed to find werewolves and villagers")
	}

	ctx.logger.Debug("Werewolf: %s, Villager: %s", werewolves[0].Name, villagers[0].Name)

	// Verify we're in night phase
	werewolves[0].reload()
	if !werewolves[0].isInNightPhase() {
		ctx.logger.LogDB("FAIL: not in night phase")
		t.Fatal("Should start in night phase")
	}

	// Werewolf votes for villager
	targetName := villagers[0].Name
	werewolves[0].voteForPlayer(targetName)
	time.Sleep(50 * time.Millisecond) // Wait for vote resolution

	ctx.logger.LogDB("after werewolf vote")

	// Check transition to day
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)

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
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after first werewolf vote")

	// Should still be in night phase (need both werewolves to vote)
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)

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
	targetName := villagers[0].Name
	ctx.logger.Debug("Target for kill: %s", targetName)

	// Werewolf votes
	werewolves[0].voteForPlayer(targetName)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after werewolf vote")

	// Check death announcement
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)

	announcement := werewolves[0].getDeathAnnouncement()
	if !strings.Contains(announcement, targetName) {
		ctx.logger.LogDB("FAIL: wrong player killed")
		t.Errorf("Death announcement should mention %s, got: %s", targetName, announcement)
	}

	// Check that the target shows as dead
	content := werewolves[0].getGameContent()
	// The dead player should have 💀 next to their name
	if !strings.Contains(content, targetName) || !strings.Contains(content, "💀") {
		ctx.logger.LogDB("FAIL: victim not marked as dead")
		t.Errorf("Victim %s should be marked as dead in player list", targetName)
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

	// Werewolves vote for different targets
	werewolves[0].voteForPlayer(villagers[0].Name)
	time.Sleep(20 * time.Millisecond)
	werewolves[1].voteForPlayer(villagers[1].Name)
	time.Sleep(50 * time.Millisecond)

	ctx.logger.LogDB("after split vote")

	// Should still be in night (no majority for either target)
	werewolves[0].reload()
	time.Sleep(20 * time.Millisecond)

	if werewolves[0].isInDayPhase() {
		ctx.logger.LogDB("FAIL: transitioned to day on split vote")
		t.Error("Should NOT transition to day when votes are split")
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
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	// Add roles: 3 villagers, 1 witch, 2 werewolves = 6 roles for 6 players
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWitch)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

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
	time.Sleep(50 * time.Millisecond)

	// Witch waits for majority then heals
	witch.reload()
	// Witch should see the victim name
	gameContent := witch.getGameContent()
	if !strings.Contains(gameContent, targetVillager.Name) {
		t.Errorf("Witch should see werewolf target name: %s", targetVillager.Name)
	}

	// Send heal action
	witch.p().MustElement("#witch-heal-form").MustClick()
	time.Sleep(100 * time.Millisecond)

	// Witch passes to end night
	witch.reload()
	witch.p().MustElement("#witch-pass-form").MustClick()
	time.Sleep(100 * time.Millisecond)

	// Check day phase - victim should be alive
	targetVillager.reload()
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
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	// Add roles: 3 villagers, 1 witch, 2 werewolves = 6 roles for 6 players
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWitch)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

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
	time.Sleep(50 * time.Millisecond)

	// Witch poisons a different player (not the target)
	witch.reload()
	// Find poison button for otherVillager
	buttons, err := witch.p().Elements(".witch-kill-button")
	if err != nil {
		t.Fatalf("Failed to find poison buttons: %v", err)
	}
	for _, btn := range buttons {
		text := btn.MustText()
		if strings.Contains(text, otherVillager.Name) {
			btn.MustClick()
			break
		}
	}
	time.Sleep(50 * time.Millisecond)

	// Witch passes
	witch.reload()
	witch.p().MustElement("#witch-pass-form").MustClick()
	time.Sleep(100 * time.Millisecond)

	// Check results in day phase
	otherVillager.reload()
	if !otherVillager.isInDayPhase() {
		content := otherVillager.getGameContent()
		t.Errorf("Should be in day phase after night. Content: %s", content)
	}

	// Verify poison target is dead (appears in death announcement with victim and poison target)
	announcement := otherVillager.getDeathAnnouncement()
	if !strings.Contains(announcement, otherVillager.Name) {
		content := otherVillager.getGameContent()
		t.Errorf("Poisoned player %s should be in death announcement. Announcement: %s, Content: %s",
			otherVillager.Name, announcement, content)
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
		p.waitForGame()
		players = append(players, p)
	}
	time.Sleep(20 * time.Millisecond)

	// Add roles: 3 villagers, 1 witch, 2 werewolves = 6 roles for 6 players
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWitch)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	time.Sleep(20 * time.Millisecond)
	players[0].startGame()
	time.Sleep(50 * time.Millisecond)

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
	time.Sleep(50 * time.Millisecond)

	// Witch just passes without using potions
	witch.reload()
	witch.p().MustElement("#witch-pass-form").MustClick()
	time.Sleep(100 * time.Millisecond)

	// Should transition to day
	witch.reload()
	if !witch.isInDayPhase() {
		ctx.logger.LogDB("FAIL: not in day phase after witch pass")
		t.Error("Should transition to day after witch passes")
	}

	ctx.logger.Debug("=== Test passed ===")
}
