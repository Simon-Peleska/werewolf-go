package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		return !!(h && h.dataset.phase === 'night');
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
		return !!(h && h.dataset.phase === 'day');
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
		return heading && heading.dataset.phase === 'day';
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
		return heading && heading.dataset.phase === 'night';
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
// Pass nil for suspect to leave no suspect selected.
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
		tp.clickAndWait(fmt.Sprintf("#survey-suspects .player-card[player-name='%s']", suspect.Name))
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
		const cards = document.querySelectorAll("[id^='vote-form-'] .player-card");
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
	tp.clickAndWait("[id^='vote-form-'] .player-card[player-name='" + targetName + "']")
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
		const card = document.querySelector("[id^='vote-form-'] .player-card[player-name='` + targetName + `']");
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
	found, el, err := tp.p().Has("[id^='vote-form-'] .player-card[selected]")
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
// Reads player-name and role-name from .player-card attributes (shadow DOM is not traversable via MustText).
func (tp *TestPlayer) getDeathAnnouncement() string {
	found, el, err := tp.p().Has(".death-announcement")
	if err != nil || !found {
		return ""
	}
	cards, _ := el.Elements(".player-card")
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
		Storyteller:   true,
		OpenAIModel:   "llm-1",
		OpenAIAPIBase: fakeOpenAi.URL + "/v1",
		OpenAIAPIKey:  "test-key",
	})

	ctx.app.hubs["test-game"].storyteller = storyteller
	defer func() { ctx.app.hubs["test-game"].storyteller = nil }()

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

// TestStorytellerExtraParams verifies storyteller_extra_params is merged into
// the chat completion request body (e.g. OpenRouter's "provider", "top_p"),
// and that it cannot clobber the fields the storyteller itself controls.
func TestStorytellerExtraParams(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	fakeOpenAi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("fake server: invalid request JSON: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer fakeOpenAi.Close()

	storyteller := initStoryteller(AppConfig{
		Storyteller:   true,
		OpenAIModel:   "llm-1",
		OpenAIAPIBase: fakeOpenAi.URL + "/v1",
		OpenAIAPIKey:  "test-key",
		StorytellerExtraParams: `{
			"top_p": 0.9,
			"provider": {"order": ["Anthropic"]},
			"model": "should-not-win"
		}`,
	})

	if _, err := storyteller.Tell(context.Background(), "system", "hello", nil); err != nil {
		t.Fatalf("Tell failed: %v", err)
	}

	if gotBody["top_p"] != 0.9 {
		t.Errorf("expected top_p 0.9 to pass through, got %v", gotBody["top_p"])
	}
	provider, ok := gotBody["provider"].(map[string]any)
	if !ok || provider["order"].([]any)[0] != "Anthropic" {
		t.Errorf("expected provider.order [Anthropic] to pass through, got %v", gotBody["provider"])
	}
	if gotBody["model"] != "llm-1" {
		t.Errorf("extra params must not override model, got %v", gotBody["model"])
	}
}

// TestAISwitchDisablesStoryteller verifies the sidebar AI switch suppresses
// the storyteller for the whole game: with AI off, no story is generated even
// when a storyteller is configured.
func TestAISwitchDisablesStoryteller(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	ctx.logger.Debug("=== Testing AI switch suppresses the storyteller ===")

	storyText := []string{"The village ", "wept ", "in silence."}
	fakeOpenAi := createFakeOpenAiServer(t, storyText)
	defer fakeOpenAi.Close()

	hub := ctx.app.hubs["test-game"]
	hub.storyteller = initStoryteller(AppConfig{
		Storyteller:   true,
		OpenAIModel:   "llm-1",
		OpenAIAPIBase: fakeOpenAi.URL + "/v1",
		OpenAIAPIKey:  "test-key",
	})
	defer func() { hub.storyteller = nil }()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	var players []*TestPlayer
	for _, name := range []string{"SW1", "SW2", "SW3"} {
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
	wolf := werewolves[0]
	other := villagers[1]

	// The switch starts on (AI enabled by default) for everyone.
	if !wolf.aiSwitchChecked() {
		t.Fatalf("AI switch should start checked for the wolf (AI on by default)")
	}
	if !other.aiSwitchChecked() {
		t.Fatalf("AI switch should start checked for the other player too")
	}

	// The wolf flips it OFF — clickAndWait waits for the wolf's own WS rebroadcast.
	wolf.clickAndWait("#ai-toggle-switch")
	if wolf.aiSwitchChecked() {
		t.Errorf("AI switch should be unchecked for the wolf after toggling off")
	}
	// End to end: the OTHER player's UI must show the switch flipped off too.
	if err := other.waitUntilSwitchChecked(false); err != nil {
		t.Errorf("AI switch did not propagate off to the other player: %v", err)
	}
	// The setting is game-wide and server-authoritative.
	game, _ := hub.getGame()
	if hub.aiEnabled(game.ID) {
		t.Errorf("AI should be disabled in the game state after toggling off")
	}

	// Run a full night kill. With AI off, no story should ever be generated.
	wolf.voteForPlayer(villagers[0].Name)
	submitNightSurveysForAllPlayers(players)

	// Wait until day begins so the (gated) story step has run.
	if err := other.waitUntilCondition(
		`() => { const l = document.querySelector('#topbar-phase-label'); return l && l.dataset.phase === 'day'; }`,
		"day phase begins",
	); err != nil {
		t.Fatalf("day phase did not begin: %v", err)
	}
	if other.historyContains(strings.Join(storyText, "")) {
		t.Errorf("AI story appeared even though AI features were switched off")
	}

	// Flip it back ON from the other player; the wolf's UI must reflect it.
	other.clickAndWait("#ai-toggle-switch")
	if !other.aiSwitchChecked() {
		t.Errorf("AI switch should be checked again after toggling on")
	}
	if err := wolf.waitUntilSwitchChecked(true); err != nil {
		t.Errorf("AI switch did not propagate on to the wolf: %v", err)
	}

	ctx.logger.Debug("=== TestAISwitchDisablesStoryteller passed ===")
}

// aiSwitchChecked reports the live checked state of the sidebar AI switch.
func (tp *TestPlayer) aiSwitchChecked() bool {
	return tp.p().MustElement("#ai-toggle-switch").MustProperty("checked").Bool()
}

// waitUntilSwitchChecked waits until this player's AI switch reaches the wanted state.
func (tp *TestPlayer) waitUntilSwitchChecked(want bool) error {
	return tp.waitUntilCondition(
		fmt.Sprintf(`() => { const s = document.querySelector('#ai-toggle-switch'); return s && s.checked === %t; }`, want),
		fmt.Sprintf("AI switch reaches checked=%t", want),
	)
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

	// Setup: 1 werewolf + 3 villagers = 4 players (need 3 villagers so after night kill
	// 1 wolf vs 2 villagers → day phase starts, not immediate werewolf win)
	players := setupNightPhaseGame(ctx, browser, 3, 1)
	werewolves, villagers := findPlayersByRole(players)

	if len(werewolves) == 0 || len(villagers) < 2 {
		t.Fatal("Need at least 1 werewolf and 2 villagers")
	}

	villager1 := villagers[0]
	villager2 := villagers[1]

	// Villagers have no night action, so they immediately see the survey form.
	// Submit villager1's survey first.
	villager1.submitNightSurvey()

	// Villager1 should now see the "waiting for N more" message (data-phase starts with "night-wait")
	phase := villager1.p().MustElement("#game-content").MustAttribute("data-phase")
	if phase == nil || !strings.HasPrefix(*phase, "night-wait") {
		ctx.logger.LogDB("FAIL: no survey-waiting message after submit")
		t.Errorf("After submitting survey, #game-content should have data-phase=night-wait-*, got: %v", phase)
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

	// Wolf submits survey (2 of 4 submitted — villager2 and villager3 still outstanding)
	werewolves[0].submitNightSurvey()

	// Villager3 submits survey (3 of 4 — villager2 still outstanding)
	villagers[2].submitNightSurvey()

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

// TestNightSurveyFormNotResetByOtherPlayerSubmit verifies that when player B
// submits their night survey, the form fields player A has already typed into
// are NOT cleared by the resulting broadcast/morph update.
func TestNightSurveyFormNotResetByOtherPlayerSubmit(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// 1 werewolf + 2 villagers — villagers have no night action so they see the
	// survey form immediately after the wolf locks in the kill vote.
	players := setupNightPhaseGame(ctx, browser, 2, 1)
	werewolves, villagers := findPlayersByRole(players)
	if len(werewolves) == 0 || len(villagers) < 2 {
		t.Fatal("Need at least 1 werewolf and 2 villagers")
	}

	villager1 := villagers[0]
	villager2 := villagers[1]
	wolf := werewolves[0]

	// Wolf votes — single wolf so End Vote fires immediately, revealing the
	// survey form to all players.
	wolf.voteForPlayer(villager1.Name)

	// Wait for the survey form to appear for villager1.
	villager1.waitUntilCondition(`() => !!document.querySelector('#night-survey-form')`, "survey form appears for villager1")

	// Villager1 fills in all three fields but does NOT submit yet.
	const theory = "it was definitely the baker"
	const notes = "suspicious bread crumbs near the body"

	villager1.clickAndWait(fmt.Sprintf("#survey-suspects .player-card[player-name='%s']", wolf.Name))

	el, err := villager1.p().Element("input[name='death_theory']")
	if err != nil {
		t.Fatalf("[%s] death_theory input: %v", villager1.Name, err)
	}
	if err := el.Input(theory); err != nil {
		t.Fatalf("[%s] input theory: %v", villager1.Name, err)
	}
	el2, err := villager1.p().Element("textarea[name='notes']")
	if err != nil {
		t.Fatalf("[%s] notes textarea: %v", villager1.Name, err)
	}
	if err := el2.Input(notes); err != nil {
		t.Fatalf("[%s] input notes: %v", villager1.Name, err)
	}

	// Set up a WS message counter on villager1's page so we can wait for the
	// broadcast that villager2's submit will trigger.
	if _, err := villager1.p().Eval(`() => {
		window._wsCount = 0;
		document.body.addEventListener('htmx:wsAfterMessage', () => { window._wsCount++; });
	}`); err != nil {
		t.Fatalf("inject WS counter: %v", err)
	}

	// Villager2 submits their survey — triggers a broadcast to all players.
	villager2.submitNightSurvey()

	// Wait until villager1 has received the broadcast.
	villager1.waitUntilCondition(`() => window._wsCount > 0`, "villager1 receives broadcast after villager2 submit")

	// All three fields must survive the morph triggered by villager2's submit.
	// The suspect is stored server-side; check that the card still has [selected].
	suspectRes, err := villager1.p().Eval(`() => document.querySelector("#survey-suspects .player-card[selected]")?.dataset.playerId ?? ""`)
	if err != nil {
		t.Fatalf("read suspect value: %v", err)
	}
	theoryRes, err := villager1.p().Eval(`() => document.querySelector("input[name='death_theory']")?.value ?? ""`)
	if err != nil {
		t.Fatalf("read theory value: %v", err)
	}
	notesRes, err := villager1.p().Eval(`() => document.querySelector("textarea[name='notes']")?.value ?? ""`)
	if err != nil {
		t.Fatalf("read notes value: %v", err)
	}

	// Check a suspect card is still marked selected (wolf was selected).
	if got := suspectRes.Value.Str(); got == "" {
		t.Errorf("suspect selection was reset by broadcast: no card has [selected] attribute")
	}
	if got := theoryRes.Value.Str(); got != theory {
		t.Errorf("death_theory was reset by broadcast: want %q, got %q", theory, got)
	}
	if got := notesRes.Value.Str(); got != notes {
		t.Errorf("notes was reset by broadcast: want %q, got %q", notes, got)
	}

	ctx.logger.Debug("=== Test passed ===")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
