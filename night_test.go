package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ============================================================================
// Night Phase Test Helpers
// ============================================================================

// isInNightPhase checks if the player sees the night phase UI
func (tp *TestPlayer) isInNightPhase() bool {
	res, err := tp.Page.Eval(`() => {
		const h = document.querySelector('#topbar-phase-label');
		return !!(h && h.textContent.trim().startsWith('Night'));
	}`)
	isNight := err == nil && res != nil && res.Value.Bool()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Is in night phase: %v", tp.Name, isNight)
	}
	return isNight
}

// isInDayPhase checks if the player sees the day phase UI
func (tp *TestPlayer) isInDayPhase() bool {
	res, err := tp.Page.Eval(`() => {
		const h = document.querySelector('#topbar-phase-label');
		return !!(h && h.textContent.trim().startsWith('Day'));
	}`)
	isDay := err == nil && res != nil && res.Value.Bool()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Is in day phase: %v", tp.Name, isDay)
	}
	return isDay
}

// waitForDayPhase waits for the player to transition to day phase by listening to WebSocket messages
func (tp *TestPlayer) waitForDayPhase() error {
	checkJS := `(() => {
		const heading = document.querySelector('#topbar-phase-label');
		return heading && heading.textContent.trim().startsWith('Day');
	})`
	err := tp.waitUntilCondition(checkJS, "day phase")

	if tp.logger != nil {
		tp.logger.Debug("[%s] Is in day phase", tp.Name)
	}
	return err

}

// waitForNightPhase waits for the player to transition to night phase by listening to WebSocket messages
func (tp *TestPlayer) waitForNightPhase() error {
	checkJS := `(() => {
		const heading = document.querySelector('#topbar-phase-label');
		return heading && heading.textContent.trim().startsWith('Night');
	})`
	return tp.waitUntilCondition(checkJS, "night phase")
}

// submitNightSurvey submits the night survey for this player.
// Waits for the survey form, "waiting" message, or "You are dead" to appear, then submits if form is visible.
func (tp *TestPlayer) submitNightSurvey() {
	checkJS := `() => {
		return !!document.querySelector('#night-survey-form') ||
		       !!document.querySelector('#survey-waiting') ||
		       document.body.textContent.includes('You are dead');
	}`
	tp.waitUntilCondition(checkJS, "night survey state")
	if has, _, _ := tp.p().Has("#night-survey-form button[type='submit']"); has {
		tp.clickAndWait("#night-survey-form button[type='submit']")
	}
}

// submitNightSurveysForAllPlayers submits the night survey for each player.
// Dead players from previous rounds are skipped automatically.
func submitNightSurveysForAllPlayers(players []*TestPlayer) {
	for _, p := range players {
		p.submitNightSurvey()
	}
}

// submitNightSurveyWithAnswers fills in the survey form with the given answers before submitting.
// Pass nil for suspect to leave the dropdown at "no suspicion".
func (tp *TestPlayer) submitNightSurveyWithAnswers(suspect *TestPlayer, theory, notes string) {
	checkJS := `() => {
		return !!document.querySelector('#night-survey-form') ||
		       !!document.querySelector('#survey-waiting') ||
		       document.body.textContent.includes('You are dead');
	}`
	tp.waitUntilCondition(checkJS, "night survey state")

	if has, _, _ := tp.p().Has("#night-survey-form"); !has {
		return // already submitted or dead
	}

	if suspect != nil {
		el, err := tp.p().Element("select[name='suspect_player_id']")
		if err != nil {
			tp.t.Fatalf("[%s] submitNightSurveyWithAnswers: suspect dropdown not found: %v", tp.Name, err)
			return
		}
		if err := el.Select([]string{suspect.Name}, true, "text"); err != nil {
			tp.t.Fatalf("[%s] submitNightSurveyWithAnswers: could not select suspect %q: %v", tp.Name, suspect.Name, err)
			return
		}
	}
	if theory != "" {
		el, err := tp.p().Element("input[name='death_theory']")
		if err != nil {
			tp.t.Fatalf("[%s] submitNightSurveyWithAnswers: death_theory input not found: %v", tp.Name, err)
			return
		}
		if err := el.Input(theory); err != nil {
			tp.t.Fatalf("[%s] submitNightSurveyWithAnswers: could not input theory: %v", tp.Name, err)
			return
		}
	}
	if notes != "" {
		el, err := tp.p().Element("textarea[name='notes']")
		if err != nil {
			tp.t.Fatalf("[%s] submitNightSurveyWithAnswers: notes textarea not found: %v", tp.Name, err)
			return
		}
		if err := el.Input(notes); err != nil {
			tp.t.Fatalf("[%s] submitNightSurveyWithAnswers: could not input notes: %v", tp.Name, err)
			return
		}
	}

	tp.clickAndWait("#night-survey-form button[type='submit']")
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
	result, err := tp.p().Eval(`() => {
		const cards = document.querySelectorAll("[id^='vote-form-'] player-card");
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
		tp.logger.Debug("[%s] Vote buttons: %v", tp.Name, names)
	}
	return names
}

// voteForPlayer clicks the vote card for a specific player
func (tp *TestPlayer) voteForPlayer(targetName string) {
	if tp.logger != nil {
		tp.logger.Debug("[%s] Voting for: %s", tp.Name, targetName)
	}
	tp.clickAndWait("[id^='vote-form-'] player-card[player-name='" + targetName + "']")
	tp.logHTML("after voting for " + targetName)
	// Auto-press End Vote if the button is present and enabled (all werewolves have voted)
	if has, endVoteBtn, _ := tp.p().Has("#werewolf-end-vote-btn:not([disabled])"); has {
		if tp.logger != nil {
			tp.logger.Debug("[%s] Auto-pressing End Vote after voting for %s", tp.Name, targetName)
		}
		tp.clickElementAndWait(endVoteBtn)
	} else {
		if tp.logger != nil {
			tp.logger.Debug("[%s] End Vote button not yet available after voting for %s", tp.Name, targetName)
		}
	}
}

// getWerewolfVoteCount returns the vote count shown on a werewolf vote card for a given player
func (tp *TestPlayer) getWerewolfVoteCount(targetName string) string {
	result, err := tp.p().Eval(`() => {
		const card = document.querySelector("[id^='vote-form-'] player-card[player-name='` + targetName + `']");
		return card ? (card.getAttribute('count') || '0') : '0';
	}`)
	if err != nil {
		return "0"
	}
	v := result.Value.String()
	if tp.logger != nil {
		tp.logger.Debug("[%s] Werewolf vote count for %s: %s", tp.Name, targetName, v)
	}
	return v
}

// getCurrentVoteTarget returns who this player has currently voted for (from UI)
func (tp *TestPlayer) getCurrentVoteTarget() string {
	found, el, err := tp.p().Has("[id^='vote-form-'] player-card[selected]")
	if err != nil || !found {
		return ""
	}
	name, err := el.Attribute("player-name")
	if err != nil || name == nil {
		return ""
	}
	if tp.logger != nil {
		tp.logger.Debug("[%s] Current vote target: %s", tp.Name, *name)
	}
	return *name
}

// getDeathAnnouncement returns the death announcement text if any.
// Reads player-name and role-name from player-card attributes (shadow DOM is not traversable via MustText).
func (tp *TestPlayer) getDeathAnnouncement() string {
	found, el, err := tp.p().Has(".death-announcement")
	if err != nil || !found {
		return ""
	}
	cards, _ := el.Elements("player-card")
	var parts []string
	for _, card := range cards {
		name, _ := card.Attribute("player-name")
		role, _ := card.Attribute("role-name")
		if name != nil && role != nil {
			parts = append(parts, *name+" ("+*role+")")
		} else if name != nil {
			parts = append(parts, *name)
		}
	}
	result := strings.Join(parts, ", ")
	if tp.logger != nil {
		tp.logger.Debug("[%s] Death announcement: %s", tp.Name, result)
	}
	return result
}

// getGameContent returns the full game content HTML for debugging
func (tp *TestPlayer) getGameContent() string {
	el, err := tp.p().Element("#game-content")
	if err != nil {
		return ""
	}
	text, _ := el.Text()
	return text
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

	// Sound toast should NOT fire yet — only one of two werewolves has voted
	if villagers[0].hasSoundToast("werewolves have made their choice") {
		ctx.logger.LogDB("FAIL: sound toast fired before all werewolves voted")
		t.Errorf("Sound toast should not appear until all werewolves have voted")
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

	submitNightSurveysForAllPlayers(players)

	// Check death announcement
	announcement := werewolves[0].getDeathAnnouncement()
	if !strings.Contains(announcement, target.Name) {
		ctx.logger.LogDB("FAIL: wrong player killed")
		t.Errorf("Death announcement should mention %s, got: %s", target.Name, announcement)
	}

	// Check that the target shows as dead in the sidebar (alive="false" on the player-card)
	if targetID == "" {
		t.Error("Could not determine target player ID")
	} else {
		deadSelector := "player-card[data-player-id='" + targetID + "'][alive=false]"
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
	witch.clickAndWait("[id^='witch-select-heal-form-'] player-card[player-name='" + targetVillager.Name + "']")
	// Wait for server to confirm selection (card gains `selected` attribute)
	witch.waitUntilCondition(
		`() => !!document.querySelector('[id^="witch-select-heal-form-"] player-card[selected]')`,
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
	witch.clickAndWait("[id^='witch-select-poison-form-'] player-card[player-name='" + otherVillager.Name + "']")
	// Wait for server to confirm selection (card gains `selected` attribute)
	witch.waitUntilCondition(
		`() => !!document.querySelector('[id^="witch-select-poison-form-"] player-card[selected]')`,
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

// canSeeMasonList checks if the player's page shows the mason card list
func (tp *TestPlayer) canSeeMasonList() bool {
	found, _, err := tp.p().Has("#mason-card-list")
	return err == nil && found
}

// canSeeMasonInList checks if a specific player appears as a card in the mason list
func (tp *TestPlayer) canSeeMasonInList(name string) bool {
	found, _, err := tp.p().Has("#mason-card-list player-card[player-name='" + name + "']")
	return err == nil && found
}

// witchCanSeeVictim waits for the witch's heal targets section to contain the named player.
// The victim only appears after werewolves press End Vote, so we poll until it shows up.
func (tp *TestPlayer) witchCanSeeVictim(name string) bool {
	checkJS := fmt.Sprintf(`(() => !!document.querySelector("#witch-heal-targets player-card[player-name='%s']"))`, name)
	err := tp.waitUntilCondition(checkJS, "witch victim "+name)
	return err == nil
}

// ============================================================================
// Mason Tests
// ============================================================================

func TestMasonsKnowEachOther(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Test: Masons know each other ===")

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
	ctx.logger.Debug("Werewolves: %v, Villagers: %v, Masons: %v",
		playerNames(werewolves), playerNames(villagers), playerNames(masons))

	if len(masons) < 2 {
		t.Fatalf("Need at least 2 masons, got %d", len(masons))
	}

	mason1 := masons[0]
	mason2 := masons[1]

	// Mason1 should see mason2 in the mason card list
	if !mason1.canSeeMasonList() {
		ctx.logger.LogDB("FAIL: mason1 cannot see mason list")
		t.Errorf("Mason '%s' should see mason list", mason1.Name)
	}
	if !mason1.canSeeMasonInList(mason2.Name) {
		ctx.logger.LogDB("FAIL: mason1 cannot see mason2")
		t.Errorf("Mason '%s' should see fellow mason '%s' in the list", mason1.Name, mason2.Name)
	}

	// Mason2 should see mason1 in the mason card list
	if !mason2.canSeeMasonInList(mason1.Name) {
		ctx.logger.LogDB("FAIL: mason2 cannot see mason1")
		t.Errorf("Mason '%s' should see fellow mason '%s' in the list", mason2.Name, mason1.Name)
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
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Test: Single mason sees no others ===")

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

// getLoverInfo returns the player-name of the sidebar card marked as the viewer's lover.
// The card is identified by the boolean "lover" attribute set in the sidebar template.
// Returns "" if no lover card is visible.
func (tp *TestPlayer) getLoverInfo() string {
	found, el, _ := tp.p().Has("#player-list player-card[lover]")
	if !found {
		return ""
	}
	name, _ := el.Attribute("player-name")
	if name == nil {
		return ""
	}
	return *name
}

// cupidPickLover clicks the cupid card to pick a lover
func (tp *TestPlayer) cupidPickLover(targetName string) {
	tp.clickAndWait("[id^='cupid-form-'] player-card[player-name='" + targetName + "']")
	tp.logHTML(fmt.Sprintf("after cupid pick: %s", targetName))
}

// cupidLinkLovers clicks Cupid's confirm button to finalize the selected lovers.
func (tp *TestPlayer) cupidLinkLovers() {
	tp.clickAndWait("#cupid-link-button")
	tp.logHTML("after cupid link")
}

// isCupidLinkButtonEnabled returns true when Cupid's link button is present and enabled.
func (tp *TestPlayer) isCupidLinkButtonEnabled() bool {
	found, el, _ := tp.p().Has("#cupid-link-button")
	if !found {
		return false
	}
	disabled, _ := el.Attribute("disabled")
	return disabled == nil
}

// getCupidSelectedNames returns the names of players currently shown as selected in the Cupid UI.
func (tp *TestPlayer) getCupidSelectedNames() []string {
	result, err := tp.p().Eval(`() => {
		const cards = document.querySelectorAll("[id^='cupid-form-'] player-card[selected]");
		return Array.from(cards).map(c => c.getAttribute('player-name') || '').filter(Boolean).join('\n');
	}`)
	if err != nil {
		return nil
	}
	raw := result.Value.String()
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}

// ============================================================================
// Cupid Tests
// ============================================================================

// TestCupidLinksLovers verifies Cupid can link two players, night resolves only after Cupid acts,
// and both lovers can see each other's identity.
func TestCupidLinksLovers(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing cupid can link first lovers ===")

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
	if cupid.isCupidLinkButtonEnabled() {
		t.Fatal("Cupid link button should be disabled until two lovers are selected")
	}

	// Cupid picks second lover
	cupid.cupidPickLover(lover2.Name)
	if !cupid.isCupidLinkButtonEnabled() {
		t.Fatal("Cupid link button should be enabled after two lovers are selected")
	}

	// Night should still not resolve until Cupid confirms link
	if werewolves[0].isInDayPhase() {
		t.Fatal("Night should not resolve until Cupid confirms linking")
	}

	cupid.cupidLinkLovers()

	submitNightSurveysForAllPlayers(players)

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

func TestCupidCanUnselectFirstLover(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing cupid can unselect first lover independently ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	var players []*TestPlayer
	for _, name := range []string{"CU1", "CU2", "CU3", "CU4", "CU5"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}
	players[0].addRoleByID(RoleCupid)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	_, villagers, cupids := findPlayersByRoleWithCupid(players)
	if len(cupids) == 0 || len(villagers) < 2 {
		t.Skip("Role assignment didn't produce expected roles")
	}
	cupid := cupids[0]
	lover1, lover2 := villagers[0].Name, villagers[1].Name

	// Select both lovers
	cupid.cupidPickLover(lover1)
	cupid.cupidPickLover(lover2)

	selected := cupid.getCupidSelectedNames()
	if len(selected) != 2 {
		ctx.logger.LogDB("FAIL: expected 2 selected after picking both")
		t.Fatalf("Expected 2 selected, got %v", selected)
	}

	// Unselect the first lover — second should remain
	cupid.cupidPickLover(lover1)

	selected = cupid.getCupidSelectedNames()
	if len(selected) != 1 || selected[0] != lover2 {
		ctx.logger.LogDB("FAIL: expected only second lover selected after unselecting first")
		t.Errorf("Expected [%s] selected, got %v", lover2, selected)
	}
	if cupid.isCupidLinkButtonEnabled() {
		t.Error("Link button should be disabled with only one lover selected")
	}

	ctx.logger.Debug("=== Test passed ===")
}

func TestCupidCanUnselectSecondLover(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing cupid can unselect second lover independently ===")

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	var players []*TestPlayer
	for _, name := range []string{"CU1", "CU2", "CU3", "CU4", "CU5"} {
		p := browser.signupPlayer(ctx.baseURL, name)
		players = append(players, p)
	}
	players[0].addRoleByID(RoleCupid)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	_, villagers, cupids := findPlayersByRoleWithCupid(players)
	if len(cupids) == 0 || len(villagers) < 2 {
		t.Skip("Role assignment didn't produce expected roles")
	}
	cupid := cupids[0]
	lover1, lover2 := villagers[0].Name, villagers[1].Name

	// Select both lovers
	cupid.cupidPickLover(lover1)
	cupid.cupidPickLover(lover2)

	selected := cupid.getCupidSelectedNames()
	if len(selected) != 2 {
		ctx.logger.LogDB("FAIL: expected 2 selected after picking both")
		t.Fatalf("Expected 2 selected, got %v", selected)
	}

	// Unselect the second lover — first should remain
	cupid.cupidPickLover(lover2)

	selected = cupid.getCupidSelectedNames()
	if len(selected) != 1 || selected[0] != lover1 {
		ctx.logger.LogDB("FAIL: expected only first lover selected after unselecting second")
		t.Errorf("Expected [%s] selected, got %v", lover1, selected)
	}
	if cupid.isCupidLinkButtonEnabled() {
		t.Error("Link button should be disabled with only one lover selected")
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestHeartbreakOnNightKill verifies that when a lover is killed at night,
// their partner dies from heartbreak and both deaths appear in the morning.
func TestHeartbreakOnNightKill(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing lover dies from heartbreak if second lover dies ===")

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
	cupid.cupidLinkLovers()

	// Werewolves kill lover1
	for _, w := range werewolves {
		w.voteForPlayer(lover1.Name)
	}

	submitNightSurveysForAllPlayers(players)

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
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing lovers can win together ===")

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
	cupid.cupidLinkLovers()

	// Werewolf kills Cupid (the non-lover)
	werewolf.voteForPlayer(cupid.Name)

	submitNightSurveysForAllPlayers(players)

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

func createFakeOpenAiServer(t *testing.T, storyText []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}

		type streamResp struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}

		send := func(text string) {
			resp := streamResp{
				Choices: []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				}{
					{
						Delta: struct {
							Content string `json:"content"`
						}{
							Content: text,
						},
					},
				},
			}

			b, _ := json.Marshal(resp)

			w.Write([]byte("data: "))
			w.Write(b)
			w.Write([]byte("\n\n"))
			flusher.Flush()
		}

		for _, text := range storyText {
			send(text)
		}

		send("wept ")
		send("in silence.")

		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
}

func TestAIStoryteller(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing Ai Storyteller can send story ===")

	// Enable mock storyteller for this test; restore nil afterwards
	storyText := []string{"The village ", "wept ", "in silence."}

	// ── 2. Fake OpenAi server (mimics OpenAI /v1/response endpoint) ─────────

	fakeOpenAi := createFakeOpenAiServer(t, storyText)
	defer fakeOpenAi.Close()

	storyteller := initStoryteller(AppConfig{
		StorytellerProvider: "openai-compatible",
		StorytellerModel:    "llm-1",
		StorytellerURL:      fakeOpenAi.URL + "/v1/",
		StorytellerAPIKey:   "test-key",
	})

	ctx.app.hub.storyteller = storyteller
	defer func() { ctx.app.hub.storyteller = nil }()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// 1 werewolf + 2 villagers
	var players []*TestPlayer
	for _, name := range []string{"ST1", "ST2", "ST3"} {
		players = append(players, browser.signupPlayer(ctx.baseURL, name))
	}

	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].startGame()

	werewolves, villagers := findPlayersByRole(players)
	if len(werewolves) == 0 || len(villagers) < 2 {
		t.Skip("Role assignment didn't produce expected roles")
	}

	target := villagers[0]
	watcher := villagers[1]

	// Werewolf kills target (voteForPlayer auto-presses End Vote)
	werewolves[0].voteForPlayer(target.Name)

	submitNightSurveysForAllPlayers(players)

	// Wait for the async AI story to appear in the watcher's history
	err := watcher.waitUntilCondition(
		fmt.Sprintf(`() => { const h = document.querySelector('#history-bar'); return h && h.textContent.includes(%q); }`, strings.Join(storyText, "")),
		"AI story appears in history",
	)
	if err != nil {
		ctx.logger.LogDB("FAIL: AI story not in history")
		t.Errorf("AI story did not appear in history: %v", err)
	}

	// Story is public — werewolf should see it too
	if !werewolves[0].historyContains(strings.Join(storyText, "")) {
		t.Errorf("Werewolf cannot see AI story in history")
	}

	ctx.logger.Debug("=== TestAIStoryteller passed ===")
}

// ============================================================================
// Night Survey Tests
// ============================================================================

// TestNightSurveyBlocksDayTransition verifies that day does not start until
// ALL alive players have submitted the night survey.
func TestNightSurveyBlocksDayTransition(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing night survey blocks day transition ===")

	// Setup: 1 werewolf + 2 villagers = 3 players
	players := setupNightPhaseGame(ctx, browser, 2, 1)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) == 0 || len(villagers) < 2 {
		t.Fatal("Need at least 1 werewolf and 2 villagers")
	}

	villager1 := villagers[0]
	villager2 := villagers[1]

	// Villagers have no night action, so they immediately see the survey form.
	// Submit villager1's survey first.
	villager1.submitNightSurvey()

	// Villager1 should now see the "waiting for N more" message
	if has, _, _ := villager1.p().Has("#survey-waiting"); !has {
		ctx.logger.LogDB("FAIL: no survey-waiting message after submit")
		t.Error("After submitting survey, player should see 'Waiting for N more' message")
	}

	// Day should NOT have started yet (wolf hasn't voted, villager2 hasn't submitted)
	if villager1.isInDayPhase() {
		ctx.logger.LogDB("FAIL: day started too early")
		t.Error("Day should not start until all players submit survey")
	}

	// Werewolf votes and End Vote is auto-pressed (1 wolf = majority immediately)
	werewolves[0].voteForPlayer(villager1.Name)

	// Day should STILL NOT have started (wolf and villager2 haven't submitted yet)
	if werewolves[0].isInDayPhase() {
		ctx.logger.LogDB("FAIL: day started before wolf/villager2 submitted survey")
		t.Error("Day should not start until all players submit survey")
	}

	// Wolf submits survey (2 of 3 submitted — villager2 still outstanding)
	werewolves[0].submitNightSurvey()

	// Day should STILL NOT have started (villager2 hasn't submitted yet)
	if werewolves[0].isInDayPhase() {
		ctx.logger.LogDB("FAIL: day started before villager2 submitted survey")
		t.Error("Day should not start until villager2 submits survey")
	}

	// Villager2 submits survey — this is the last survey, triggers day transition
	villager2.submitNightSurvey()

	// All surveys submitted → day should start now
	err := werewolves[0].waitForDayPhase()
	if err != nil {
		ctx.logger.LogDB("FAIL: day did not start after all surveys")
		t.Errorf("Day should start after all surveys submitted: %v", err)
	}

	if !werewolves[0].isInDayPhase() {
		content := werewolves[0].getGameContent()
		ctx.logger.LogDB("FAIL: not in day phase")
		t.Errorf("Should be in day phase after all surveys submitted. Content: %s", content)
	}

	// The night kill should now be revealed (villager1 died at night)
	announcement := werewolves[0].getDeathAnnouncement()
	if !strings.Contains(announcement, villager1.Name) {
		t.Errorf("Death announcement should mention villager1 (%s), got: %s", villager1.Name, announcement)
	}

	ctx.logger.Debug("=== Test passed ===")
}

// TestNightSurveyAnswersVisibleInHistory verifies that non-empty survey answers
// are written to history and become visible to all players once the day starts.
func TestNightSurveyAnswersVisibleInHistory(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	ctx.logger.Debug("=== Testing night survey answers visible in history ===")

	// 1 werewolf + 2 villagers
	players := setupNightPhaseGame(ctx, browser, 2, 1)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) == 0 || len(villagers) < 2 {
		t.Fatal("Need at least 1 werewolf and 2 villagers")
	}

	villager1 := villagers[0]
	villager2 := villagers[1]
	wolf := werewolves[0]

	// Villager1 submits with a suspect, theory, and notes
	theory := "I think it was the wolf"
	notes := "seemed nervous during discussion"
	villager1.submitNightSurveyWithAnswers(wolf, theory, notes)

	// Wolf votes to kill villager1, then submits empty survey
	wolf.voteForPlayer(villager1.Name)
	wolf.submitNightSurvey()

	// Villager2 submits empty survey — last one, triggers day transition
	villager2.submitNightSurvey()

	err := villager2.waitForDayPhase()
	if err != nil {
		ctx.logger.LogDB("FAIL: day did not start")
		t.Fatalf("Day should start after all surveys: %v", err)
	}

	// Full history entry format: "Night 1: <name> — Suspects: <wolf> | Theory: <text> | Notes: <text>"
	// VisibilityResolved means visible to everyone once day starts.
	expected := fmt.Sprintf("Night 1: %s — Suspects: %s | Theory: %s | Notes: %s",
		villager1.Name, wolf.Name, theory, notes)

	for _, viewer := range []*TestPlayer{villager2, wolf} {
		if !viewer.historyContains(expected) {
			ctx.logger.LogDB("FAIL: survey not in history for " + viewer.Name)
			t.Errorf("[%s] Survey not in history.\nExpected: %q\nGot: %s", viewer.Name, expected, viewer.getHistoryText())
		}
	}

	ctx.logger.Debug("=== Test passed ===")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
