package main

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
)

// WerewolfVote represents a werewolf's vote during the night
type WerewolfVote struct {
	VoterName  string
	TargetName string
}

// SeerResult represents a seer's investigation result
type SeerResult struct {
	Round      int
	TargetName string
	IsWerewolf bool
}

// NightData holds all data needed to render the night phase
type NightData struct {
	Player                    *Player
	AliveTargets              []Player // alive players; visibility pre-applied
	Votes                     []WerewolfVote
	WerewolfVoteCounts        map[int64]int // vote count per target player ID
	CurrentVotePlayer         *Player       // werewolf's current vote (nil = no vote)
	WolfCubDoubleKill         bool          // werewolves must kill two this night
	CurrentVotePlayer2        *Player       // werewolf's current second vote (nil = none)
	NightNumber               int
	HasInvestigated           bool
	SeerSelectedPlayer        *Player // pending investigation target (nil = not yet selected)
	HasProtected              bool
	DoctorSelectedPlayer      *Player // pending protection target (nil = not yet selected)
	DoctorProtectingPlayer    *Player // confirmed protection target this night
	GuardHasProtected         bool
	GuardSelectedPlayer       *Player  // pending protection target (nil = not yet selected)
	GuardProtectingPlayer     *Player  // confirmed protection target this night
	GuardTargets              []Player // alive targets excluding self and last night's; visibility pre-applied
	WitchVictimPlayer         *Player  // werewolf majority target (nil = no majority); visibility pre-applied
	WitchVictimPlayer2        *Player  // Wolf Cub second kill target (nil = not set); visibility pre-applied
	HealPotionUsed            bool     // committed heal used in any prior round
	PoisonPotionUsed          bool     // committed poison used in any prior round
	WitchSelectedHealPlayer   *Player  // pending heal target (nil = none selected)
	WitchSelectedPoisonPlayer *Player  // pending poison target (nil = none selected)
	WitchHealedThisNight      bool     // committed heal applied this night
	WitchHealedPlayer         *Player  // who the witch healed this night
	WitchKilledThisNight      bool     // committed poison applied this night
	WitchKilledPlayer         *Player  // poison target this night
	WitchDoneThisNight        bool     // true after witch_apply submitted
	Masons                    []Player // other alive Masons (excluding self); full role visible
	CupidLinked               bool
	CupidChosen1Player        *Player  // first lover choice (nil = not yet chosen)
	CupidChosen2Player        *Player  // second lover choice (nil = not yet chosen)
	AllWolvesActed            bool     // all werewolves have voted or passed (first kill)
	AllWolvesActed2           bool     // all werewolves have voted or passed (Wolf Cub second kill)
	WolfEndVoted              bool     // End Vote pressed for first kill this round
	WolfEndVoted2             bool     // End Vote pressed for Wolf Cub second kill
	ShowSurvey                bool     // player has finished their role action, show survey
	HasSubmittedSurvey        bool     // this player already pressed Continue
	SurveyCount               int      // how many alive players have submitted the survey
	AliveCount                int      // total alive players
	SurveyTargets             []Player // alive players for the suspect selection
	SurveySelectedSuspect     *Player  // pending suspect selection (nil = none selected)
}

func handleWSWerewolfVote(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfVote: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}

	// Check that the player is a werewolf
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfVote: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can vote at night")
		return
	}

	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot vote")
		return
	}

	// Reject if End Vote already pressed (vote is locked in)
	var endVoteCount int
	h.db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote)
	if endVoteCount > 0 {
		h.sendErrorToast(client.playerID, "The vote has already been locked in")
		return
	}

	// Parse target player ID
	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, "Invalid target")
		return
	}

	// Check that the target is valid (alive)
	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil {
		h.sendErrorToast(client.playerID, "Target not found")
		return
	}

	if !target.IsAlive {
		h.sendErrorToast(client.playerID, "Cannot target a dead player")
		return
	}

	// Toggle: if already voted for this target, unselect (delete the vote)
	var existingTarget sql.NullInt64
	h.db.Get(&existingTarget, `SELECT target_player_id FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill)
	if existingTarget.Valid && existingTarget.Int64 == targetID {
		_, err = h.db.Exec(`DELETE FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
			game.ID, game.Round, client.playerID, ActionWerewolfKill)
		if err != nil {
			h.logError("handleWSWerewolfVote: db.Exec delete vote", err)
			h.sendErrorToast(client.playerID, "Failed to clear vote")
			return
		}
		h.logf("Werewolf %d (%s) unselected vote for player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
		h.triggerBroadcast()
		return
	}

	// Record or update the vote
	description := fmt.Sprintf("Night %d: %s voted to kill %s", game.Round, voter.Name, target.Name)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = ?, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill, targetID, VisibilityTeamWerewolf, description, targetID, description)
	if err != nil {
		h.logError("handleWSWerewolfVote: db.Exec insert vote", err)
		h.sendErrorToast(client.playerID, "Failed to record vote")
		return
	}

	h.logf("Werewolf %d (%s) voted to kill player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSWerewolfVote", "Werewolf '%s' voted to kill '%s'", voter.Name, target.Name)
	LogDBState(h.db, "after werewolf vote")

	h.triggerBroadcast()
}

func handleWSWerewolfVote2(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfVote2: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}

	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfVote2: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can vote at night")
		return
	}

	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot vote")
		return
	}

	// Validate that Wolf Cub double kill is actually active this night
	if game.Round <= 1 {
		h.sendErrorToast(client.playerID, "Wolf Cub double kill not active")
		return
	}
	var wolfCubDeathCount int
	h.db.Get(&wolfCubDeathCount, `
		SELECT COUNT(*) FROM game_action ga
		JOIN game_player gp ON ga.target_player_id = gp.player_id AND gp.game_id = ga.game_id
		JOIN role r ON gp.role_id = r.rowid
		WHERE ga.game_id = ? AND ga.round = ?
		AND ga.action_type IN ('werewolf_kill', 'elimination', 'hunter_revenge', 'witch_kill')
		AND r.name = 'Wolf Cub'`,
		game.ID, game.Round-1)
	if wolfCubDeathCount == 0 {
		h.sendErrorToast(client.playerID, "Wolf Cub double kill not active")
		return
	}

	// Reject if End Vote 2 already pressed
	var endVote2Count int
	h.db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote2)
	if endVote2Count > 0 {
		h.sendErrorToast(client.playerID, "The second vote has already been locked in")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, "Invalid target")
		return
	}

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil {
		h.sendErrorToast(client.playerID, "Target not found")
		return
	}

	if !target.IsAlive {
		h.sendErrorToast(client.playerID, "Cannot target a dead player")
		return
	}

	description2 := fmt.Sprintf("Night %d: %s voted to kill %s (Wolf Cub revenge)", game.Round, voter.Name, target.Name)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = ?, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill2, targetID, VisibilityTeamWerewolf, description2, targetID, description2)
	if err != nil {
		h.logError("handleWSWerewolfVote2: db.Exec insert vote2", err)
		h.sendErrorToast(client.playerID, "Failed to record second vote")
		return
	}

	h.logf("Werewolf %d (%s) voted second kill: player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSWerewolfVote2", "Werewolf '%s' second kill vote: '%s'", voter.Name, target.Name)
	LogDBState(h.db, "after werewolf vote2")

	h.triggerBroadcast()
}

func handleWSWerewolfPass(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfPass: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfPass: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can vote at night")
		return
	}
	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot vote")
		return
	}
	var endVoteCount int
	h.db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote)
	if endVoteCount > 0 {
		h.sendErrorToast(client.playerID, "The vote has already been locked in")
		return
	}
	passDesc := fmt.Sprintf("Night %d: %s passed", game.Round, voter.Name)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, NULL, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = NULL, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill, VisibilityTeamWerewolf, passDesc, passDesc)
	if err != nil {
		h.logError("handleWSWerewolfPass: db.Exec", err)
		h.sendErrorToast(client.playerID, "Failed to record pass")
		return
	}
	h.logf("Werewolf %d (%s) passed the kill vote", client.playerID, voter.Name)
	h.triggerBroadcast()
}

func handleWSWerewolfPass2(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfPass2: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfPass2: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can vote at night")
		return
	}
	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot vote")
		return
	}
	var endVote2Count int
	h.db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote2)
	if endVote2Count > 0 {
		h.sendErrorToast(client.playerID, "The second vote has already been locked in")
		return
	}
	passDesc := fmt.Sprintf("Night %d: %s passed (second kill)", game.Round, voter.Name)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, NULL, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = NULL, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill2, VisibilityTeamWerewolf, passDesc, passDesc)
	if err != nil {
		h.logError("handleWSWerewolfPass2: db.Exec", err)
		h.sendErrorToast(client.playerID, "Failed to record pass")
		return
	}
	h.logf("Werewolf %d (%s) passed the second kill vote", client.playerID, voter.Name)
	h.triggerBroadcast()
}

func handleWSWerewolfEndVote(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfEndVote: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfEndVote: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can end the vote")
		return
	}

	// Check all alive werewolves have acted
	var werewolves []Player
	h.db.Select(&werewolves, `
		SELECT g.rowid as id, g.player_id as player_id, p.name as name
		FROM game_player g
		JOIN player p ON g.player_id = p.rowid
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

	var totalActed int
	h.db.Get(&totalActed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfKill)

	if totalActed < len(werewolves) {
		h.sendErrorToast(client.playerID, fmt.Sprintf("Not all werewolves have voted yet (%d/%d)", totalActed, len(werewolves)))
		return
	}

	// Record End Vote (INSERT OR IGNORE — idempotent)
	_, err = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, NULL, ?, '')`,
		game.ID, game.Round, client.playerID, ActionWerewolfEndVote, VisibilityTeamWerewolf)
	if err != nil {
		h.logError("handleWSWerewolfEndVote: record end vote", err)
	}

	h.logf("Werewolf %d (%s) ended the kill vote", client.playerID, voter.Name)
	html := renderToast(h.templates, h.logf, "info", "🐺 The werewolves have made their choice...")
	if html != "" {
		h.broadcast <- []byte(html)
	}
	h.maybeSpeakStory(game.ID, "The werewolves have made their choice. Silence falls over the village.")
	h.resolveWerewolfVotes(game)
}

func handleWSWerewolfEndVote2(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfEndVote2: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfEndVote2: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can end the vote")
		return
	}

	var werewolves []Player
	h.db.Select(&werewolves, `
		SELECT g.rowid as id, g.player_id as player_id, p.name as name
		FROM game_player g
		JOIN player p ON g.player_id = p.rowid
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

	var totalActed2 int
	h.db.Get(&totalActed2, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfKill2)

	if totalActed2 < len(werewolves) {
		h.sendErrorToast(client.playerID, fmt.Sprintf("Not all werewolves have voted for the second kill yet (%d/%d)", totalActed2, len(werewolves)))
		return
	}

	_, err = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, NULL, ?, '')`,
		game.ID, game.Round, client.playerID, ActionWerewolfEndVote2, VisibilityTeamWerewolf)
	if err != nil {
		h.logError("handleWSWerewolfEndVote2: record end vote 2", err)
	}

	h.logf("Werewolf %d (%s) ended the second kill vote", client.playerID, voter.Name)
	h.resolveWerewolfVotes(game)
}

// handleWSSeerSelect toggles the seer's pending investigation selection.
// Clicking the same player again deselects; clicking a different player replaces the selection.
func handleWSSeerSelect(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSSeerSelect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Can only act during night phase")
		return
	}
	investigator, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSSeerSelect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if investigator.RoleName != "Seer" {
		h.sendErrorToast(client.playerID, "Only the Seer can select an investigation target")
		return
	}
	if !investigator.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}
	// Don't allow re-selection if already investigated
	var existingCount int
	h.db.Get(&existingCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerInvestigate)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, "You have already investigated this night")
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
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerSelect)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// Same player clicked again → deselect
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionSeerSelect)
		h.logf("Seer '%s' deselected investigation target", investigator.Name)
	} else {
		// Select (or replace prior selection)
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionSeerSelect, targetID, VisibilityActor)
		h.logf("Seer '%s' selected investigation target %d", investigator.Name, targetID)
	}

	h.triggerBroadcast()
}

func handleWSSeerInvestigate(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSSeerInvestigate: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Can only investigate during night phase")
		return
	}

	investigator, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSSeerInvestigate: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if investigator.RoleName != "Seer" {
		h.sendErrorToast(client.playerID, "Only the Seer can investigate")
		return
	}

	if !investigator.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}

	// Check if already investigated this night
	var existingCount int
	h.db.Get(&existingCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionSeerInvestigate)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, "You have already investigated this night")
		return
	}

	// Read the pending selection from DB
	var selectAction GameAction
	if err := h.db.Get(&selectAction, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerSelect); err != nil || selectAction.TargetPlayerID == nil {
		h.sendErrorToast(client.playerID, "Select a player to investigate first")
		return
	}
	targetID := *selectAction.TargetPlayerID

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil {
		h.sendErrorToast(client.playerID, "Target not found")
		return
	}

	if !target.IsAlive {
		h.sendErrorToast(client.playerID, "Cannot investigate a dead player")
		return
	}

	// Remove the pending selection
	h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerSelect)

	result := "not a werewolf"
	if target.Team == "werewolf" {
		result = "a werewolf"
	}
	seerDesc := fmt.Sprintf("Night %d: You investigated %s — they are %s", game.Round, target.Name, result)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionSeerInvestigate, targetID, VisibilityActor, seerDesc)
	if err != nil {
		h.logError("handleWSSeerInvestigate: db.Exec insert investigation", err)
		h.sendErrorToast(client.playerID, "Failed to record investigation")
		return
	}

	toastMsg := fmt.Sprintf("🔮 %s is not a werewolf.", target.Name)
	if target.Team == "werewolf" {
		toastMsg = fmt.Sprintf("🔮 %s is a werewolf!", target.Name)
	}
	h.sendToPlayer(client.playerID, []byte(renderToast(h.templates, h.logf, "info", toastMsg)))

	h.logf("Seer '%s' investigated '%s' (team: %s)", investigator.Name, target.Name, target.Team)
	DebugLog("handleWSSeerInvestigate", "Seer '%s' investigated '%s' (team: %s)", investigator.Name, target.Name, target.Team)
	LogDBState(h.db, "after seer investigation")

	h.resolveWerewolfVotes(game)
}

// handleWSDoctorSelect toggles the doctor's pending protection target selection.
// Clicking the same player again deselects; clicking a different player replaces the selection.
func handleWSDoctorSelect(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSDoctorSelect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Can only act during night phase")
		return
	}
	doctor, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSDoctorSelect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if doctor.RoleName != "Doctor" {
		h.sendErrorToast(client.playerID, "Only the Doctor can select a protection target")
		return
	}
	if !doctor.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}
	// Don't allow re-selection if already protected
	var existingCount int
	h.db.Get(&existingCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionDoctorProtect)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, "You have already protected someone this night")
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
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionDoctorSelect)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// Same player clicked again → deselect
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionDoctorSelect)
		h.logf("Doctor '%s' deselected protection target", doctor.Name)
	} else {
		// Select (or replace prior selection)
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionDoctorSelect, targetID, VisibilityActor)
		h.logf("Doctor '%s' selected protection target %d", doctor.Name, targetID)
	}

	h.triggerBroadcast()
}

func handleWSDoctorProtect(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSDoctorProtect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Can only protect during night phase")
		return
	}

	doctor, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSDoctorProtect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if doctor.RoleName != "Doctor" {
		h.sendErrorToast(client.playerID, "Only the Doctor can protect players")
		return
	}

	if !doctor.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}

	// One protection per night
	var existingCount int
	h.db.Get(&existingCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionDoctorProtect)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, "You have already protected someone this night")
		return
	}

	// Read the pending selection from DB
	var selectAction GameAction
	if err := h.db.Get(&selectAction, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionDoctorSelect); err != nil || selectAction.TargetPlayerID == nil {
		h.sendErrorToast(client.playerID, "Select a player to protect first")
		return
	}
	targetID := *selectAction.TargetPlayerID

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil {
		h.sendErrorToast(client.playerID, "Target not found")
		return
	}

	if !target.IsAlive {
		h.sendErrorToast(client.playerID, "Cannot protect a dead player")
		return
	}

	// Remove the pending selection
	h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionDoctorSelect)

	doctorDesc := fmt.Sprintf("Night %d: You protected %s", game.Round, target.Name)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionDoctorProtect, targetID, VisibilityActor, doctorDesc)
	if err != nil {
		h.logError("handleWSDoctorProtect: db.Exec insert protection", err)
		h.sendErrorToast(client.playerID, "Failed to record protection")
		return
	}

	h.logf("Doctor '%s' is protecting '%s'", doctor.Name, target.Name)
	DebugLog("handleWSDoctorProtect", "Doctor '%s' protecting '%s'", doctor.Name, target.Name)
	LogDBState(h.db, "after doctor protect")

	h.resolveWerewolfVotes(game)
}

// handleWSGuardSelect toggles the guard's pending protection target selection.
// Clicking the same player again deselects; clicking a different player replaces the selection.
func handleWSGuardSelect(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSGuardSelect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Can only act during night phase")
		return
	}
	guard, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSGuardSelect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if guard.RoleName != "Guard" {
		h.sendErrorToast(client.playerID, "Only the Guard can select a protection target")
		return
	}
	if !guard.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}
	// Don't allow re-selection if already protected
	var existingCount int
	h.db.Get(&existingCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionGuardProtect)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, "You have already protected someone this night")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, "Invalid target")
		return
	}
	if targetID == client.playerID {
		h.sendErrorToast(client.playerID, "Guard cannot protect themselves")
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
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionGuardSelect)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// Same player clicked again → deselect
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionGuardSelect)
		h.logf("Guard '%s' deselected protection target", guard.Name)
	} else {
		// Select (or replace prior selection)
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionGuardSelect, targetID, VisibilityActor)
		h.logf("Guard '%s' selected protection target %d", guard.Name, targetID)
	}

	h.triggerBroadcast()
}

func handleWSGuardProtect(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSGuardProtect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Can only protect during night phase")
		return
	}

	guard, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSGuardProtect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if guard.RoleName != "Guard" {
		h.sendErrorToast(client.playerID, "Only the Guard can protect players")
		return
	}

	if !guard.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}

	// One protection per night
	var existingCount int
	h.db.Get(&existingCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionGuardProtect)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, "You have already protected someone this night")
		return
	}

	// Read the pending selection from DB
	var selectAction GameAction
	if err := h.db.Get(&selectAction, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionGuardSelect); err != nil || selectAction.TargetPlayerID == nil {
		h.sendErrorToast(client.playerID, "Select a player to protect first")
		return
	}
	targetID := *selectAction.TargetPlayerID

	// Guard cannot protect themselves (defensive re-check)
	if targetID == client.playerID {
		h.sendErrorToast(client.playerID, "Guard cannot protect themselves")
		return
	}

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil {
		h.sendErrorToast(client.playerID, "Target not found")
		return
	}

	if !target.IsAlive {
		h.sendErrorToast(client.playerID, "Cannot protect a dead player")
		return
	}

	// Remove the pending selection
	h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionGuardSelect)

	// Guard cannot protect the same player as last night
	if game.Round > 1 {
		var lastTargetID int64
		err := h.db.Get(&lastTargetID, `
			SELECT target_player_id FROM game_action
			WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
			game.ID, game.Round-1, client.playerID, ActionGuardProtect)
		if err == nil && lastTargetID == targetID {
			h.sendErrorToast(client.playerID, "Cannot protect the same player two nights in a row")
			return
		}
	}

	guardDesc := fmt.Sprintf("Night %d: You protected %s", game.Round, target.Name)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionGuardProtect, targetID, VisibilityActor, guardDesc)
	if err != nil {
		h.logError("handleWSGuardProtect: db.Exec insert protection", err)
		h.sendErrorToast(client.playerID, "Failed to record protection")
		return
	}

	h.logf("Guard '%s' is protecting '%s'", guard.Name, target.Name)
	DebugLog("handleWSGuardProtect", "Guard '%s' protecting '%s'", guard.Name, target.Name)
	LogDBState(h.db, "after guard protect")

	h.resolveWerewolfVotes(game)
}

// handleWSCupidChoose handles Cupid's lover selection on Night 1.
// Picks are staged (replaceable) until Cupid explicitly confirms via handleWSCupidLink.
func handleWSCupidChoose(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSCupidChoose: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" || game.Round != 1 {
		h.sendErrorToast(client.playerID, "Cupid can only act on Night 1")
		return
	}

	cupid, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSCupidChoose: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if cupid.RoleName != "Cupid" || !cupid.IsAlive {
		h.sendErrorToast(client.playerID, "Only the living Cupid can link lovers")
		return
	}

	// Reject if already finalized
	var finalized int
	h.db.Get(&finalized, `SELECT COUNT(*) FROM game_lovers WHERE game_id = ?`, game.ID)
	if finalized > 0 {
		h.sendErrorToast(client.playerID, "You have already linked the lovers")
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

	// Toggle: if this player is already in a slot, remove them from that slot.
	// Query each slot directly by (action_type, target_player_id) — fully independent.
	var inSlot1, inSlot2 int
	h.db.Get(&inSlot1, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ? AND target_player_id = ?`,
		game.ID, client.playerID, ActionCupidLink, targetID)
	h.db.Get(&inSlot2, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ? AND target_player_id = ?`,
		game.ID, client.playerID, ActionCupidLink2, targetID)

	if inSlot1 > 0 {
		_, err = h.db.Exec(`DELETE FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
			game.ID, client.playerID, ActionCupidLink)
		if err != nil {
			h.logError("handleWSCupidChoose: delete slot1", err)
			h.sendErrorToast(client.playerID, "Failed to clear choice")
			return
		}
		h.logf("Cupid '%s' unselected first lover", cupid.Name)
		h.triggerBroadcast()
		return
	}
	if inSlot2 > 0 {
		_, err = h.db.Exec(`DELETE FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
			game.ID, client.playerID, ActionCupidLink2)
		if err != nil {
			h.logError("handleWSCupidChoose: delete slot2", err)
			h.sendErrorToast(client.playerID, "Failed to clear choice")
			return
		}
		h.logf("Cupid '%s' unselected second lover", cupid.Name)
		h.triggerBroadcast()
		return
	}

	// Not currently selected — read slot occupancy and fill the first empty slot.
	var slot1ID, slot2ID int64
	h.db.Get(&slot1ID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
		game.ID, client.playerID, ActionCupidLink)
	h.db.Get(&slot2ID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
		game.ID, client.playerID, ActionCupidLink2)

	var fillType string
	if slot1ID == 0 {
		if slot2ID == targetID {
			h.sendErrorToast(client.playerID, "The two lovers must be different players")
			return
		}
		fillType = ActionCupidLink
	} else if slot2ID == 0 {
		if slot1ID == targetID {
			h.sendErrorToast(client.playerID, "The two lovers must be different players")
			return
		}
		fillType = ActionCupidLink2
	} else {
		// Both filled — replace slot 2
		if slot1ID == targetID {
			h.sendErrorToast(client.playerID, "The two lovers must be different players")
			return
		}
		fillType = ActionCupidLink2
	}

	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, 1, 'night', ?, ?, ?, ?, '')
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = ?, description = ''`,
		game.ID, client.playerID, fillType, targetID, VisibilityActor, targetID)
	if err != nil {
		h.logError("handleWSCupidChoose: insert", err)
		h.sendErrorToast(client.playerID, "Failed to record choice")
		return
	}
	h.logf("Cupid '%s' chose lover '%s' (slot: %s)", cupid.Name, target.Name, fillType)
	h.triggerBroadcast()
}

// handleWSCupidLink finalizes Cupid's staged lover choices on Night 1.
func handleWSCupidLink(client *Client) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSCupidLink: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" || game.Round != 1 {
		h.sendErrorToast(client.playerID, "Cupid can only act on Night 1")
		return
	}

	cupid, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSCupidLink: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if cupid.RoleName != "Cupid" || !cupid.IsAlive {
		h.sendErrorToast(client.playerID, "Only the living Cupid can link lovers")
		return
	}

	var finalized int
	h.db.Get(&finalized, `SELECT COUNT(*) FROM game_lovers WHERE game_id = ?`, game.ID)
	if finalized > 0 {
		h.sendErrorToast(client.playerID, "You have already linked the lovers")
		return
	}

	var firstLoverID int64
	var secondLoverID int64
	h.db.Get(&firstLoverID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
		game.ID, client.playerID, ActionCupidLink)
	h.db.Get(&secondLoverID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
		game.ID, client.playerID, ActionCupidLink2)

	if firstLoverID == 0 || secondLoverID == 0 {
		h.sendErrorToast(client.playerID, "Choose two lovers before linking them")
		return
	}
	if firstLoverID == secondLoverID {
		h.sendErrorToast(client.playerID, "The two lovers must be different players")
		return
	}

	var first Player
	var second Player
	first, err = getPlayerInGame(h.db, game.ID, firstLoverID)
	if err != nil || !first.IsAlive {
		h.sendErrorToast(client.playerID, "First lover is invalid")
		return
	}
	second, err = getPlayerInGame(h.db, game.ID, secondLoverID)
	if err != nil || !second.IsAlive {
		h.sendErrorToast(client.playerID, "Second lover is invalid")
		return
	}

	_, err = h.db.Exec(`INSERT OR IGNORE INTO game_lovers (game_id, player1_id, player2_id) VALUES (?, ?, ?)`,
		game.ID, firstLoverID, secondLoverID)
	if err != nil {
		h.logError("handleWSCupidLink: insert lovers row1", err)
		h.sendErrorToast(client.playerID, "Failed to link lovers")
		return
	}
	_, err = h.db.Exec(`INSERT OR IGNORE INTO game_lovers (game_id, player1_id, player2_id) VALUES (?, ?, ?)`,
		game.ID, secondLoverID, firstLoverID)
	if err != nil {
		h.logError("handleWSCupidLink: insert lovers row2", err)
		h.sendErrorToast(client.playerID, "Failed to link lovers")
		return
	}

	desc1 := fmt.Sprintf("Night 1: Your lover is %s", second.Name)
	desc2 := fmt.Sprintf("Night 1: Your lover is %s", first.Name)
	_, _ = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, 1, 'night', ?, ?, ?, ?, ?)`,
		game.ID, firstLoverID, ActionCupidLink, secondLoverID, VisibilityActor, desc1)
	_, _ = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, 1, 'night', ?, ?, ?, ?, ?)`,
		game.ID, secondLoverID, ActionCupidLink, firstLoverID, VisibilityActor, desc2)

	// Clear Cupid's staged picks once finalized.
	_, _ = h.db.Exec(`DELETE FROM game_action WHERE game_id = ? AND round = 1 AND phase = 'night' AND actor_player_id = ? AND action_type IN (?, ?)`,
		game.ID, client.playerID, ActionCupidLink, ActionCupidLink2)

	h.sendToPlayer(firstLoverID, []byte(renderToast(h.templates, h.logf, "info", fmt.Sprintf("💞 Cupid has linked you! Your lover is %s.", second.Name))))
	h.sendToPlayer(secondLoverID, []byte(renderToast(h.templates, h.logf, "info", fmt.Sprintf("💞 Cupid has linked you! Your lover is %s.", first.Name))))

	h.logf("Cupid '%s' linked lovers: '%s' and '%s'", cupid.Name, first.Name, second.Name)
	DebugLog("handleWSCupidLink", "Cupid '%s' linked '%s' and '%s'", cupid.Name, first.Name, second.Name)
	LogDBState(h.db, "after cupid links lovers")
	h.resolveWerewolfVotes(game)
}

// handleWSWitchSelectHeal toggles the witch's pending heal selection.
// Clicking the same player again deselects; clicking a different player replaces the selection.
func handleWSWitchSelectHeal(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWitchSelectHeal: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Can only act during night phase")
		return
	}
	witch, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWitchSelectHeal: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if witch.RoleName != "Witch" {
		h.sendErrorToast(client.playerID, "Only the Witch can select a heal target")
		return
	}
	if !witch.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}
	// Reject if already applied this night
	var appliedCount int
	h.db.Get(&appliedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchApply)
	if appliedCount > 0 {
		h.sendErrorToast(client.playerID, "You have already submitted your actions for this night")
		return
	}
	// Reject if heal potion already committed in a prior round
	var healUsed int
	h.db.Get(&healUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionWitchHeal)
	if healUsed > 0 {
		h.sendErrorToast(client.playerID, "Your heal potion has already been used")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, "Invalid target")
		return
	}

	// Toggle: if same player already selected, deselect
	var existing GameAction
	selectErr := h.db.Get(&existing, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectHeal)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// Same player clicked again → deselect
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionWitchSelectHeal)
		h.logf("Witch '%s' deselected heal target", witch.Name)
	} else {
		// Select (or replace prior selection)
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionWitchSelectHeal, targetID, VisibilityActor)
		h.logf("Witch '%s' selected heal target %d", witch.Name, targetID)
	}

	h.triggerBroadcast()
}

// handleWSWitchSelectPoison toggles the witch's pending poison selection.
func handleWSWitchSelectPoison(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWitchSelectPoison: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Can only act during night phase")
		return
	}
	witch, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWitchSelectPoison: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if witch.RoleName != "Witch" {
		h.sendErrorToast(client.playerID, "Only the Witch can select a poison target")
		return
	}
	if !witch.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}
	// Reject if already applied this night
	var appliedCount int
	h.db.Get(&appliedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchApply)
	if appliedCount > 0 {
		h.sendErrorToast(client.playerID, "You have already submitted your actions for this night")
		return
	}
	// Reject if poison potion already committed in a prior round
	var poisonUsed int
	h.db.Get(&poisonUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionWitchKill)
	if poisonUsed > 0 {
		h.sendErrorToast(client.playerID, "Your poison potion has already been used")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, "Invalid target")
		return
	}

	// Toggle: if same player already selected, deselect
	var existing GameAction
	selectErr := h.db.Get(&existing, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectPoison)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// Same player clicked again → deselect
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionWitchSelectPoison)
		h.logf("Witch '%s' deselected poison target", witch.Name)
	} else {
		// Select (or replace prior selection)
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionWitchSelectPoison, targetID, VisibilityActor)
		h.logf("Witch '%s' selected poison target %d", witch.Name, targetID)
	}

	h.triggerBroadcast()
}

// handleWSWitchApply commits the witch's pending selections and ends her night turn.
func handleWSWitchApply(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWitchApply: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Can only act during night phase")
		return
	}
	witch, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWitchApply: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if witch.RoleName != "Witch" {
		h.sendErrorToast(client.playerID, "Only the Witch can apply actions")
		return
	}
	if !witch.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}
	// Idempotency guard
	var appliedCount int
	h.db.Get(&appliedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchApply)
	if appliedCount > 0 {
		h.sendErrorToast(client.playerID, "You have already submitted your actions for this night")
		return
	}

	// Commit pending heal selection if present
	var healAction GameAction
	if err := h.db.Get(&healAction, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectHeal); err == nil && healAction.TargetPlayerID != nil {

		targetID := *healAction.TargetPlayerID
		// Final validation: heal potion not used globally
		var healUsed int
		h.db.Get(&healUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
			game.ID, client.playerID, ActionWitchHeal)
		if healUsed > 0 {
			h.sendErrorToast(client.playerID, "Your heal potion has already been used")
			return
		}
		// Cannot heal self
		if targetID == client.playerID {
			h.sendErrorToast(client.playerID, "You cannot heal yourself")
			return
		}
		// Heal target must be a wolf victim (End Vote must have been pressed)
		var endVoteCount int
		h.db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
			game.ID, game.Round, ActionWerewolfEndVote)
		if endVoteCount == 0 {
			h.sendErrorToast(client.playerID, "Werewolves have not locked in their vote yet")
			return
		}
		var isVictim int
		h.db.Get(&isVictim, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type IN (?,?) AND target_player_id=?`,
			game.ID, game.Round, ActionWerewolfKill, ActionWerewolfKill2, targetID)
		if isVictim == 0 {
			h.sendErrorToast(client.playerID, "You can only heal a werewolf target")
			return
		}
		var targetName string
		h.db.Get(&targetName, "SELECT name FROM player WHERE rowid = ?", targetID)
		witchHealDesc := fmt.Sprintf("Night %d: You saved %s with your heal potion", game.Round, targetName)
		_, err = h.db.Exec(`
			INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
			VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
			game.ID, game.Round, client.playerID, ActionWitchHeal, targetID, VisibilityActor, witchHealDesc)
		if err != nil {
			h.logError("handleWSWitchApply: commit heal", err)
			h.sendErrorToast(client.playerID, "Failed to commit heal")
			return
		}
		h.logf("Witch '%s' committed heal on player %d (%s)", witch.Name, targetID, targetName)
	}

	// Commit pending poison selection if present
	var poisonAction GameAction
	if err := h.db.Get(&poisonAction, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectPoison); err == nil && poisonAction.TargetPlayerID != nil {

		targetID := *poisonAction.TargetPlayerID
		// Final validation: poison potion not used globally
		var poisonUsed int
		h.db.Get(&poisonUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
			game.ID, client.playerID, ActionWitchKill)
		if poisonUsed > 0 {
			h.sendErrorToast(client.playerID, "Your poison potion has already been used")
			return
		}
		target, err := getPlayerInGame(h.db, game.ID, targetID)
		if err != nil || !target.IsAlive {
			h.sendErrorToast(client.playerID, "Poison target is no longer valid")
			return
		}
		witchKillDesc := fmt.Sprintf("Night %d: You poisoned %s", game.Round, target.Name)
		_, err = h.db.Exec(`
			INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
			VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
			game.ID, game.Round, client.playerID, ActionWitchKill, targetID, VisibilityActor, witchKillDesc)
		if err != nil {
			h.logError("handleWSWitchApply: commit poison", err)
			h.sendErrorToast(client.playerID, "Failed to commit poison")
			return
		}
		h.logf("Witch '%s' committed poison on player %d (%s)", witch.Name, targetID, target.Name)
	}

	// Record that the witch has finished her night turn
	witchApplyDesc := fmt.Sprintf("Night %d: Witch %s confirmed her actions", game.Round, witch.Name)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionWitchApply, VisibilityActor, witchApplyDesc)
	if err != nil {
		h.logError("handleWSWitchApply: insert apply", err)
		h.sendErrorToast(client.playerID, "Failed to record witch action")
		return
	}

	h.logf("Witch '%s' applied actions for night %d", witch.Name, game.Round)
	DebugLog("handleWSWitchApply", "Witch '%s' applied", witch.Name)
	LogDBState(h.db, "after witch apply")

	h.resolveWerewolfVotes(game)
}

// playerDoneWithNightAction returns true if the given player has completed their night role action
// and the survey should be shown to them.
func playerDoneWithNightAction(db *sqlx.DB, gameID int64, round int, player Player) bool {
	switch player.RoleName {
	case "Villager", "Mason", "Hunter":
		return true // no night action
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

	var parts []string
	if suspectID > 0 {
		var suspectName string
		h.db.Get(&suspectName, "SELECT name FROM player WHERE rowid=?", suspectID)
		if suspectName != "" {
			parts = append(parts, "Suspects: "+suspectName)
		}
	}
	if deathTheory != "" {
		parts = append(parts, "Theory: "+deathTheory)
	}
	if notes != "" {
		parts = append(parts, "Notes: "+notes)
	}

	var description string
	if len(parts) > 0 {
		description = fmt.Sprintf("Night %d: %s — %s", game.Round, player.Name, strings.Join(parts, " | "))
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
		// Apply all pending night kills (description='' marks them as pending)
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
			h.db.Exec(`UPDATE game_action SET description=? WHERE rowid=?`, desc, pk.ID)
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
	_, err := h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
		game.ID, game.Round, playerID, ActionNightKill, playerID, VisibilityPublic, desc)
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

	// Insert pending kill for main wolf victim (description='' hides from history until surveys complete)
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
