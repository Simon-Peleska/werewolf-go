package main

import (
	"fmt"
	"strings"
	"testing"
)

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
	found, el, _ := tp.p().Has("#player-list .player-card[lover]")
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
	tp.clickAndWait("[id^='cupid-form-'] .player-card[player-name='" + targetName + "']")
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
		const cards = document.querySelectorAll("[id^='cupid-form-'] .player-card[selected]");
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

// TestCupidUIHiddenOnNight2 verifies that the Cupid "Link Two Lovers" UI does
// not appear on Night 2 (Cupid only acts on Night 1).
func TestCupidUIHiddenOnNight2(t *testing.T) {
	t.Parallel()
	ctx := newTestContext(t)
	defer ctx.cleanup()

	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// 1 Cupid + 1 Werewolf + 4 Villagers = 6 players.
	// Cupid links V1+V2. Wolf kills V3 (unlinked — no heartbreak).
	// Day 1 vote eliminates V4. 4 players survive to Night 2.
	var players []*TestPlayer
	for _, name := range []string{"CU1", "CU2", "CU3", "CU4", "CU5", "CU6"} {
		players = append(players, browser.signupPlayer(ctx.baseURL, name))
	}
	players[0].addRoleByID(RoleCupid)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleWerewolf)
	players[0].startGame()

	werewolves, villagers, cupids := findPlayersByRoleWithCupid(players)
	if len(cupids) == 0 || len(werewolves) == 0 || len(villagers) < 4 {
		t.Skip("Role assignment didn't produce expected roles")
	}

	cupid := cupids[0]
	wolf := werewolves[0]

	// Night 1: Cupid must see the UI.
	if !cupid.canSeeCupidUI() {
		t.Fatal("Cupid should see the link lovers UI on Night 1")
	}

	// Cupid links villagers[0]+villagers[1]. Wolf kills villagers[2] (unlinked).
	cupid.cupidPickLover(villagers[0].Name)
	cupid.cupidPickLover(villagers[1].Name)
	cupid.clickAndWait(`#cupid-link-button`)

	wolf.voteForPlayer(villagers[2].Name)
	submitNightSurveysForAllPlayers(players)

	// Day 1: vote out villagers[3] so the game continues into Night 2.
	if err := wolf.waitForDayPhase(); err != nil {
		t.Fatalf("Day 1 did not start: %v", err)
	}
	// 5 players alive after night kill (wolf + cupid + V0 + V1 + V3).
	// Vote out V3 — 4 survive, game continues. All 5 must vote for End Vote to trigger.
	wolf.dayVoteForPlayer(villagers[3].Name)
	cupid.dayVoteForPlayer(villagers[3].Name)
	villagers[0].dayVoteForPlayer(villagers[3].Name)
	villagers[1].dayVoteForPlayer(villagers[3].Name)
	villagers[3].dayVoteForPlayer(wolf.Name) // V3 votes wolf; 4/5 votes for V3 = majority

	if err := wolf.waitForNightPhase(); err != nil {
		t.Fatalf("Night 2 did not start: %v", err)
	}

	// Night 2: Cupid UI must NOT be shown.
	if cupid.canSeeCupidUI() {
		t.Error("Cupid 'Link Two Lovers' UI should not appear on Night 2")
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
