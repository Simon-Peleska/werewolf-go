package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
)

// NightData holds all data needed to render the night phase.
// Role-specific data is embedded from per-role structs defined in their own files.
type NightData struct {
	Player       *Player
	AliveTargets []Player // alive players; visibility pre-applied
	NightNumber  int
	HasHistory   bool
	Lang         string

	ShowSurvey            bool
	HasSubmittedSurvey    bool
	SurveyCount           int
	AliveCount            int
	SurveyTargets         []Player
	SurveySelectedSuspect *Player

	WerewolfNightData
	SeerNightData
	DoctorNightData
	GuardNightData
	WitchNightData
	MasonNightData
	CupidNightData
	DoppelgangerNightData
}

// playerDoneWithNightAction returns true if the given player has completed their night role action
// and the survey should be shown to them.
func playerDoneWithNightAction(db *sqlx.DB, gameID int64, round int, player Player) bool {
	switch player.RoleName {
	case "Villager", "Mason", "Hunter":
		return true // no night action
	case "Doppelganger":
		// Night 1 only (role changes after copying, so this case is hit before copying)
		var c int
		db.Get(&c, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			gameID, round, player.PlayerID, ActionDoppelgangerCopy)
		return c > 0
	case "Seer":
		var c int
		db.Get(&c, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			gameID, round, player.PlayerID, ActionSeerInvestigate)
		return c > 0
	case "Doctor":
		var c int
		db.Get(&c, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			gameID, round, player.PlayerID, ActionDoctorProtect)
		return c > 0
	case "Guard":
		var c int
		db.Get(&c, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			gameID, round, player.PlayerID, ActionGuardProtect)
		return c > 0
	case "Witch":
		var c int
		db.Get(&c, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			gameID, round, player.PlayerID, ActionWitchApply)
		return c > 0
	case "Werewolf", "Wolf Cub":
		// Survey available after End Vote is pressed (any wolf)
		var c int
		db.Get(&c, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
			gameID, round, ActionWerewolfEndVote)
		if c == 0 {
			return false
		}
		// If Wolf Cub double kill is active this round, also require End Vote 2
		if round > 1 {
			var wolfCubDeathCount int
			db.Get(&wolfCubDeathCount, `
SELECT COUNT(*) FROM game_action ga
JOIN game_player gp ON ga.target_player_id = gp.player_id AND gp.game_id = ga.game_id
JOIN role r ON gp.role_id = r.rowid
WHERE ga.game_id = ? AND ga.round = ?
AND ga.action_type IN ('werewolf_kill', 'elimination', 'hunter_revenge', 'witch_kill')
AND r.name = 'Wolf Cub'`,
				gameID, round-1)
			if wolfCubDeathCount > 0 {
				var c2 int
				db.Get(&c2, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
					gameID, round, ActionWerewolfEndVote2)
				return c2 > 0
			}
		}
		return true
	case "Cupid":
		if round > 1 {
			return true
		}
		var loverCount int
		db.Get(&loverCount, `SELECT COUNT(*) FROM game_lovers WHERE game_id=?`, gameID)
		return loverCount > 0
	default:
		return true
	}
}

// handleWSNightSurveySuspect toggles the player's pending suspect selection.
// Clicking the same player again deselects; clicking a different player replaces the selection.
func handleWSNightSurveySuspect(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSNightSurveySuspect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Survey only available during night phase")
		return
	}
	player, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil || !player.IsAlive {
		h.sendErrorToast(client.playerID, "You must be alive to submit the survey")
		return
	}
	// Don't allow changes after survey is submitted
	var submitted int
	h.db.Get(&submitted, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=? AND actor_player_id=?`,
		game.ID, game.Round, ActionNightSurvey, client.playerID)
	if submitted > 0 {
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, "Invalid target")
		return
	}
	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil || !target.IsAlive {
		h.sendErrorToast(client.playerID, "Invalid target")
		return
	}

	var existing GameAction
	selectErr := h.db.Get(&existing, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionNightSurveySuspect)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// Same player clicked again → deselect
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionNightSurveySuspect)
		h.logf("Player '%s' deselected survey suspect", player.Name)
	} else {
		// Select (or replace prior selection)
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionNightSurveySuspect, targetID, VisibilityActor)
		h.logf("Player '%s' selected survey suspect %d", player.Name, targetID)
	}

	h.triggerBroadcast()
}

// handleWSNightSurvey records a player's night survey answers.
// Once all alive players have submitted, the game transitions to day.
func handleWSNightSurvey(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSNightSurvey: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Survey only available during night phase")
		return
	}

	player, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil || !player.IsAlive {
		h.sendErrorToast(client.playerID, "You must be alive to submit the survey")
		return
	}

	// Idempotent: ignore if already submitted
	var existing int
	h.db.Get(&existing, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=? AND actor_player_id=?`,
		game.ID, game.Round, ActionNightSurvey, client.playerID)
	if existing > 0 {
		return
	}

	// Read suspect from pre-selection (night_survey_suspect action)
	var suspectID int64
	var suspectAction GameAction
	if err := h.db.Get(&suspectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action WHERE game_id=? AND round=? AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionNightSurveySuspect); err == nil && suspectAction.TargetPlayerID != nil {
		suspectID = *suspectAction.TargetPlayerID
	}

	// Build description from non-empty fields
	deathTheory := strings.TrimSpace(msg.DeathTheory)
	notes := strings.TrimSpace(msg.Notes)

	lang := client.lang
	if lang == "" {
		lang = "en"
	}
	var parts []string
	if suspectID > 0 {
		var suspectName string
		h.db.Get(&suspectName, "SELECT name FROM player WHERE rowid=?", suspectID)
		if suspectName != "" {
			parts = append(parts, T(lang, "survey_suspects")+": "+suspectName)
		}
	}
	if deathTheory != "" {
		parts = append(parts, T(lang, "survey_theory")+": "+deathTheory)
	}
	if notes != "" {
		parts = append(parts, T(lang, "survey_notes")+": "+notes)
	}

	var description string
	if len(parts) > 0 {
		description = fmt.Sprintf(T(lang, "survey_prefix"), game.Round, player.Name, strings.Join(parts, " | "))
	}
	// If all empty, description stays "" (counts as submitted, hidden from history)

	_, err = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionNightSurvey, VisibilityResolved, description)
	if err != nil {
		h.logError("handleWSNightSurvey: insert survey", err)
		h.sendErrorToast(client.playerID, "Failed to record survey")
		return
	}

	// Clean up the pending suspect selection now that it's committed
	h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionNightSurveySuspect)

	h.logf("Survey submitted by '%s' (game %d round %d)", player.Name, game.ID, game.Round)

	// Transition to day if all alive players have now submitted
	var aliveCount int
	h.db.Get(&aliveCount, `SELECT COUNT(*) FROM game_player WHERE game_id=? AND is_alive=1`, game.ID)
	var surveyCount int
	h.db.Get(&surveyCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
		game.ID, game.Round, ActionNightSurvey)

	h.logf("Night survey progress: %d/%d", surveyCount, aliveCount)

	if surveyCount >= aliveCount {
		// Apply all pending night kills (description=” marks them as pending)
		type pendingKill struct {
			ID             int64 `db:"id"`
			TargetPlayerID int64 `db:"target_player_id"`
		}
		var pendingKills []pendingKill
		h.db.Select(&pendingKills, `SELECT rowid as id, target_player_id FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=? AND description=''`,
			game.ID, game.Round, ActionNightKill)

		var nightKills []int64
		var nightKillNames []string
		for _, pk := range pendingKills {
			if _, err = h.db.Exec("UPDATE game_player SET is_alive=0 WHERE game_id=? AND player_id=?", game.ID, pk.TargetPlayerID); err != nil {
				h.logError("handleWSNightSurvey: apply kill", err)
				continue
			}
			var name, roleName string
			h.db.Get(&name, "SELECT name FROM player WHERE rowid=?", pk.TargetPlayerID)
			h.db.Get(&roleName, `SELECT r.name FROM game_player gp JOIN role r ON gp.role_id=r.rowid WHERE gp.game_id=? AND gp.player_id=?`, game.ID, pk.TargetPlayerID)
			desc := fmt.Sprintf("Night %d: %s (%s) was found dead", game.Round, name, roleName)
			h.db.Exec(`UPDATE game_action SET description=?, description_key=?, description_args=? WHERE rowid=?`,
				desc, "hist_found_dead", histArgs(game.Round, name, roleName), pk.ID)
			nightKills = append(nightKills, pk.TargetPlayerID)
			nightKillNames = append(nightKillNames, name)
			h.logf("Applied pending night kill: %s (%s)", name, roleName)
		}

		// Transition to day, then apply heartbreaks and check win conditions
		if _, err = h.db.Exec("UPDATE game SET status='day' WHERE rowid=?", game.ID); err != nil {
			h.logError("handleWSNightSurvey: transition to day", err)
			return
		}
		h.applyHeartbreaks(game, "night", nightKills)

		h.logf("Night %d ended (all surveys submitted), transitioning to day", game.Round)
		LogDBState(h.db, "after all surveys submitted and kills applied")

		if h.checkWinConditions(game) {
			return
		}
		if len(nightKillNames) == 0 {
			h.maybeSpeakStory(game.ID, "Dawn breaks. The village survived the night unscathed.")
		} else {
			h.maybeSpeakStory(game.ID, fmt.Sprintf("Dawn breaks. The village awakens to find %s dead.", strings.Join(nightKillNames, " and ")))
		}
		if len(nightKills) > 0 {
			h.maybeGenerateStory(game.ID, game.Round, "night", nightKills[0])
		}
	}

	h.triggerBroadcast()
}

// recordPublicDeath inserts a public history entry when a player dies at night.
func recordPublicDeath(h *Hub, game *Game, playerID int64) {
	var name string
	h.db.Get(&name, "SELECT name FROM player WHERE rowid = ?", playerID)
	var roleName string
	h.db.Get(&roleName, `SELECT r.name FROM game_player gp JOIN role r ON gp.role_id = r.rowid WHERE gp.game_id = ? AND gp.player_id = ?`, game.ID, playerID)
	desc := fmt.Sprintf("Night %d: %s (%s) was found dead", game.Round, name, roleName)
	_, err := h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args) VALUES (?, ?, 'night', ?, ?, ?, ?, ?, ?, ?)`,
		game.ID, game.Round, playerID, ActionNightKill, playerID, VisibilityPublic, desc, "hist_found_dead", histArgs(game.Round, name, roleName))
	if err != nil {
		h.logError("recordPublicDeath: insert death record", err)
	} else {
		h.logf("Recorded public death: %s", desc)
	}
}

// resolveWerewolfVotes checks if all werewolves have voted and resolves the kill
func (h *Hub) resolveWerewolfVotes(game *Game) {
	// Get all living werewolves
	var werewolves []Player
	err := h.db.Select(&werewolves, `
SELECT g.rowid as id, g.player_id as player_id, p.name as name
FROM game_player g
JOIN player p ON g.player_id = p.rowid
JOIN role r ON g.role_id = r.rowid
WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)
	if err != nil {
		h.logError("resolveWerewolfVotes: get werewolves", err)
		return
	}

	// Get all werewolf votes for this night
	var votes []GameAction
	err = h.db.Select(&votes, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfKill)
	if err != nil {
		h.logError("resolveWerewolfVotes: get votes", err)
		return
	}

	h.logf("Werewolf vote check: %d werewolves, %d votes", len(werewolves), len(votes))

	// Check if all werewolves have voted or passed
	if len(votes) < len(werewolves) {
		h.logf("Not all werewolves have voted yet (%d/%d)", len(votes), len(werewolves))
		h.triggerBroadcast()
		return
	}

	// Check if End Vote has been pressed — don't resolve until one wolf locks it in
	var endVoteCount int
	h.db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote)
	if endVoteCount == 0 {
		h.logf("Werewolves have all voted but End Vote not pressed yet")
		h.triggerBroadcast()
		return
	}

	// Count votes for each target (NULL = pass, excluded from counts)
	voteCounts := make(map[int64]int)
	for _, v := range votes {
		if v.TargetPlayerID != nil {
			voteCounts[*v.TargetPlayerID]++
		}
	}

	// Find the target with the most votes
	var maxVotes int
	var victim int64
	for targetID, count := range voteCounts {
		if count > maxVotes {
			maxVotes = count
			victim = targetID
		}
	}

	// Check for majority (more than half of werewolves)
	// If no majority (all passed or split), victim = 0 (no kill) — proceed to check other night roles
	majority := len(werewolves)/2 + 1
	if maxVotes < majority {
		h.logf("No majority reached (need %d, max is %d) — no kill this night", majority, maxVotes)
		victim = 0
	}

	// Check if Wolf Cub died last round → double kill required
	wolfCubDoubleKill := false
	var victim2 int64
	if game.Round > 1 {
		var wolfCubDeathCount int
		h.db.Get(&wolfCubDeathCount, `
SELECT COUNT(*) FROM game_action ga
JOIN game_player gp ON ga.target_player_id = gp.player_id AND gp.game_id = ga.game_id
JOIN role r ON gp.role_id = r.rowid
WHERE ga.game_id = ? AND ga.round = ?
AND ga.action_type IN ('werewolf_kill', 'elimination', 'hunter_revenge', 'witch_kill')
AND r.name = 'Wolf Cub'`,
			game.ID, game.Round-1)
		wolfCubDoubleKill = wolfCubDeathCount > 0
	}

	if wolfCubDoubleKill {
		var votes2 []GameAction
		h.db.Select(&votes2, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
			game.ID, game.Round, ActionWerewolfKill2)

		if len(votes2) < len(werewolves) {
			h.logf("Wolf Cub double kill: waiting for second votes (%d/%d)", len(votes2), len(werewolves))
			h.triggerBroadcast()
			return
		}

		var endVote2Count int
		h.db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
			game.ID, game.Round, ActionWerewolfEndVote2)
		if endVote2Count == 0 {
			h.logf("Wolf Cub double kill: End Vote 2 not pressed yet")
			h.triggerBroadcast()
			return
		}

		voteCounts2 := make(map[int64]int)
		for _, v := range votes2 {
			if v.TargetPlayerID != nil {
				voteCounts2[*v.TargetPlayerID]++
			}
		}
		var maxVotes2 int
		for targetID, count := range voteCounts2 {
			if count > maxVotes2 {
				maxVotes2 = count
				victim2 = targetID
			}
		}
		if maxVotes2 < majority {
			h.logf("Wolf Cub double kill: no majority for second victim (need %d, max is %d) — no second kill", majority, maxVotes2)
			victim2 = 0
		}
	}

	// Night 1: check Cupid has linked lovers before resolving
	if game.Round == 1 {
		var aliveCupidCount int
		h.db.Get(&aliveCupidCount, `
SELECT COUNT(*) FROM game_player g
JOIN role r ON g.role_id = r.rowid
WHERE g.game_id = ? AND g.is_alive = 1 AND r.name = 'Cupid'`, game.ID)
		if aliveCupidCount > 0 {
			var loverCount int
			h.db.Get(&loverCount, `SELECT COUNT(*) FROM game_lovers WHERE game_id = ?`, game.ID)
			if loverCount == 0 {
				h.logf("Waiting for Cupid to link lovers")
				h.triggerBroadcast()
				return
			}
		}
	}

	// Night 1: check all alive Doppelgangers have copied before resolving
	// (after copying, role_id changes so this count drops to 0)
	if game.Round == 1 {
		var aliveDoppelgangerCount int
		h.db.Get(&aliveDoppelgangerCount, `
SELECT COUNT(*) FROM game_player g
JOIN role r ON g.role_id = r.rowid
WHERE g.game_id = ? AND g.is_alive = 1 AND r.name = 'Doppelganger'`, game.ID)
		if aliveDoppelgangerCount > 0 {
			h.logf("Waiting for Doppelganger(s) to copy (%d remaining)", aliveDoppelgangerCount)
			h.triggerBroadcast()
			return
		}
	}

	// Check if all alive Seers have investigated before resolving the night
	var aliveSeerCount int
	h.db.Get(&aliveSeerCount, `
SELECT COUNT(*) FROM game_player g
JOIN role r ON g.role_id = r.rowid
WHERE g.game_id = ? AND g.is_alive = 1 AND r.name = 'Seer'`, game.ID)

	var seerInvestigateCount int
	h.db.Get(&seerInvestigateCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionSeerInvestigate)

	if seerInvestigateCount < aliveSeerCount {
		h.logf("Waiting for seers to investigate (%d/%d)", seerInvestigateCount, aliveSeerCount)
		h.triggerBroadcast()
		return
	}

	// Check if all alive Doctors have protected before resolving the night
	var aliveDoctorCount int
	h.db.Get(&aliveDoctorCount, `
SELECT COUNT(*) FROM game_player g
JOIN role r ON g.role_id = r.rowid
WHERE g.game_id = ? AND g.is_alive = 1 AND r.name = 'Doctor'`, game.ID)

	var doctorProtectCount int
	h.db.Get(&doctorProtectCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionDoctorProtect)

	if doctorProtectCount < aliveDoctorCount {
		h.logf("Waiting for doctors to protect (%d/%d)", doctorProtectCount, aliveDoctorCount)
		h.triggerBroadcast()
		return
	}

	// Check if all alive Guards have protected before resolving the night
	var aliveGuardCount int
	h.db.Get(&aliveGuardCount, `
SELECT COUNT(*) FROM game_player g
JOIN role r ON g.role_id = r.rowid
WHERE g.game_id = ? AND g.is_alive = 1 AND r.name = 'Guard'`, game.ID)

	var guardProtectCount int
	h.db.Get(&guardProtectCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionGuardProtect)

	if guardProtectCount < aliveGuardCount {
		h.logf("Waiting for guards to protect (%d/%d)", guardProtectCount, aliveGuardCount)
		h.triggerBroadcast()
		return
	}

	// Check if all alive Witches have passed before resolving the night
	var aliveWitchCount int
	h.db.Get(&aliveWitchCount, `
SELECT COUNT(*) FROM game_player g
JOIN role r ON g.role_id = r.rowid
WHERE g.game_id = ? AND g.is_alive = 1 AND r.name = 'Witch'`, game.ID)

	if aliveWitchCount > 0 {
		var witchApplyCount int
		h.db.Get(&witchApplyCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
			game.ID, game.Round, ActionWitchApply)

		if witchApplyCount < aliveWitchCount {
			h.logf("Waiting for witch to apply (%d/%d)", witchApplyCount, aliveWitchCount)
			h.triggerBroadcast()
			return
		}
	}

	// No kill this night (wolves passed or no majority) — record pending kills for wolf cub and witch
	if victim == 0 {
		h.logf("No werewolf kill this night (wolves passed or no majority)")
		// Wolf Cub second kill (pending — applied when surveys complete)
		if wolfCubDoubleKill && victim2 != 0 {
			var protect2Count int
			h.db.Get(&protect2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type IN (?, ?, ?) AND target_player_id = ?`,
				game.ID, game.Round, ActionDoctorProtect, ActionGuardProtect, ActionWitchHeal, victim2)
			var victim2Name string
			h.db.Get(&victim2Name, "SELECT name FROM player WHERE rowid = ?", victim2)
			if protect2Count > 0 {
				h.logf("Protection saved %s from Wolf Cub double kill", victim2Name)
			} else {
				h.logf("Wolf Cub double kill pending: %s", victim2Name)
				h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
					game.ID, game.Round, victim2, ActionNightKill, victim2, VisibilityPublic)
			}
		}
		// Witch poison (pending — applied when surveys complete)
		var witchKillActionNoVictim GameAction
		if wkErr := h.db.Get(&witchKillActionNoVictim, `SELECT * FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`, game.ID, game.Round, ActionWitchKill); wkErr == nil && witchKillActionNoVictim.TargetPlayerID != nil {
			var poisonName string
			h.db.Get(&poisonName, "SELECT name FROM player WHERE rowid = ?", *witchKillActionNoVictim.TargetPlayerID)
			h.logf("Witch poison pending: %s", poisonName)
			h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
				game.ID, game.Round, *witchKillActionNoVictim.TargetPlayerID, ActionNightKill, *witchKillActionNoVictim.TargetPlayerID, VisibilityPublic)
		}
		h.logf("Night %d: no wolf kill, waiting for surveys", game.Round)
		h.triggerBroadcast()
		return
	}

	// Check if the victim is protected by any Doctor
	var protectionCount int
	h.db.Get(&protectionCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ? AND target_player_id = ?`,
		game.ID, game.Round, ActionDoctorProtect, victim)

	// Check if the victim is protected by any Guard
	var guardProtectionCount int
	h.db.Get(&guardProtectionCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ? AND target_player_id = ?`,
		game.ID, game.Round, ActionGuardProtect, victim)

	// Check if the victim is healed by the Witch (target-specific)
	var witchHealCount int
	h.db.Get(&witchHealCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ? AND target_player_id = ?`,
		game.ID, game.Round, ActionWitchHeal, victim)

	if protectionCount > 0 || guardProtectionCount > 0 || witchHealCount > 0 {
		var victimName string
		h.db.Get(&victimName, "SELECT name FROM player WHERE rowid = ?", victim)
		if protectionCount > 0 {
			h.logf("Doctor saved %s (player ID %d) from werewolf attack", victimName, victim)
		}
		if guardProtectionCount > 0 {
			h.logf("Guard saved %s (player ID %d) from werewolf attack", victimName, victim)
		}
		if witchHealCount > 0 {
			h.logf("Witch saved %s (player ID %d) from werewolf attack", victimName, victim)
		}

		// Wolf Cub second kill may still land even if main victim is protected
		if wolfCubDoubleKill && victim2 != 0 {
			var protect2Count int
			h.db.Get(&protect2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type IN (?, ?, ?) AND target_player_id = ?`,
				game.ID, game.Round, ActionDoctorProtect, ActionGuardProtect, ActionWitchHeal, victim2)
			var victim2Name string
			h.db.Get(&victim2Name, "SELECT name FROM player WHERE rowid = ?", victim2)
			if protect2Count > 0 {
				h.logf("Protection saved %s from Wolf Cub double kill", victim2Name)
			} else {
				h.logf("Wolf Cub double kill pending: %s", victim2Name)
				h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
					game.ID, game.Round, victim2, ActionNightKill, victim2, VisibilityPublic)
			}
		}
		// Witch poison is separate from the main wolf kill
		var witchKillActionP2 GameAction
		if wkErr := h.db.Get(&witchKillActionP2, `SELECT * FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`, game.ID, game.Round, ActionWitchKill); wkErr == nil && witchKillActionP2.TargetPlayerID != nil {
			var poisonName string
			h.db.Get(&poisonName, "SELECT name FROM player WHERE rowid = ?", *witchKillActionP2.TargetPlayerID)
			h.logf("Witch poison pending: %s", poisonName)
			h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
				game.ID, game.Round, *witchKillActionP2.TargetPlayerID, ActionNightKill, *witchKillActionP2.TargetPlayerID, VisibilityPublic)
		}

		h.logf("Night %d: main victim protected, waiting for surveys", game.Round)
		LogDBState(h.db, "after protection save")
		h.triggerBroadcast()
		return
	}

	// Insert pending kill for main wolf victim (description=” hides from history until surveys complete)
	var victimName string
	h.db.Get(&victimName, "SELECT name FROM player WHERE rowid = ?", victim)
	h.logf("Werewolf kill pending: %s (player ID %d)", victimName, victim)
	DebugLog("resolveWerewolfVotes", "Werewolf kill pending: '%s', waiting for surveys", victimName)
	h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
		game.ID, game.Round, victim, ActionNightKill, victim, VisibilityPublic)

	// Witch poison (pending — applied when surveys complete)
	var witchKillAction GameAction
	if err := h.db.Get(&witchKillAction, `SELECT * FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`, game.ID, game.Round, ActionWitchKill); err == nil && witchKillAction.TargetPlayerID != nil {
		var poisonVictimName string
		h.db.Get(&poisonVictimName, "SELECT name FROM player WHERE rowid = ?", *witchKillAction.TargetPlayerID)
		h.logf("Witch poison pending: %s (player ID %d)", poisonVictimName, *witchKillAction.TargetPlayerID)
		h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, *witchKillAction.TargetPlayerID, ActionNightKill, *witchKillAction.TargetPlayerID, VisibilityPublic)
	}

	// Wolf Cub second kill (pending — applied when surveys complete)
	if wolfCubDoubleKill && victim2 != 0 && victim2 != victim {
		var protect2Count int
		h.db.Get(&protect2Count, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night'
AND action_type IN (?, ?, ?) AND target_player_id = ?`,
			game.ID, game.Round, ActionDoctorProtect, ActionGuardProtect, ActionWitchHeal, victim2)
		var victim2Name string
		h.db.Get(&victim2Name, "SELECT name FROM player WHERE rowid = ?", victim2)
		if protect2Count > 0 {
			h.logf("Protection saved %s (player ID %d) from Wolf Cub double kill", victim2Name, victim2)
		} else {
			h.logf("Wolf Cub double kill pending: %s (player ID %d)", victim2Name, victim2)
			h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
				game.ID, game.Round, victim2, ActionNightKill, victim2, VisibilityPublic)
		}
	}

	h.logf("Night %d: kills pending, waiting for surveys", game.Round)
	LogDBState(h.db, "after pending night kills recorded")
	h.triggerBroadcast()
}
