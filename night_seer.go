package main

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

// SeerNightData holds night-phase display data for the Seer.
type SeerNightData struct {
	HasInvestigated    bool
	SeerSelectedPlayer *Player // confirmed target (after investigate) or pending selection
}

func buildSeerNightData(db *sqlx.DB, game *Game, playerID int64, player Player, seerInvestigated map[int64]string) SeerNightData {
	if player.RoleName != "Seer" {
		return SeerNightData{}
	}

	var action GameAction
	err := db.Get(&action, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionSeerInvestigate)

	if err == nil && action.TargetPlayerID != nil {
		return SeerNightData{
			HasInvestigated:    true,
			SeerSelectedPlayer: getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated),
		}
	}

	// Pending selection (not yet confirmed)
	var selectAction GameAction
	if db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, playerID, ActionSeerSelect) == nil && selectAction.TargetPlayerID != nil {
		return SeerNightData{
			SeerSelectedPlayer: getVisiblePlayer(db, game.ID, *selectAction.TargetPlayerID, player, seerInvestigated),
		}
	}

	return SeerNightData{}
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
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionSeerSelect)
		h.logf("Seer '%s' deselected investigation target", investigator.Name)
	} else {
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

	var existingCount int
	h.db.Get(&existingCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionSeerInvestigate)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, "You have already investigated this night")
		return
	}

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

	h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerSelect)

	seerKey := "hist_seer_not_wolf"
	result := "not a werewolf"
	if target.Team == "werewolf" {
		seerKey = "hist_seer_wolf"
		result = "a werewolf"
	}
	seerDesc := fmt.Sprintf("Night %d: You investigated %s — they are %s", game.Round, target.Name, result)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
VALUES (?, ?, 'night', ?, ?, ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionSeerInvestigate, targetID, VisibilityActor, seerDesc, seerKey, histArgs(game.Round, target.Name))
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
