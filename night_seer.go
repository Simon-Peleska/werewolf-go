package main

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

type SeerNightData struct {
	HasInvestigated    bool
	SeerSelectedPlayer *Player // pending, or confirmed once investigated
	SeerResultCard     *PlayerCardData
	SeerTargetCards    []PlayerCardData
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
		game.ID, game.Round, playerID, ActionSeerApplyInvestigate)

	if err == nil && action.TargetPlayerID != nil {
		return SeerNightData{
			HasInvestigated:    true,
			SeerSelectedPlayer: getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated),
		}
	}

	var selectAction GameAction
	if db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, playerID, ActionSeerSelectInvestigate) == nil && selectAction.TargetPlayerID != nil {
		return SeerNightData{
			SeerSelectedPlayer: getVisiblePlayer(db, game.ID, *selectAction.TargetPlayerID, player, seerInvestigated),
		}
	}

	return SeerNightData{}
}

func handleWSSeerSelect(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSSeerSelect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_phase_act"))
		return
	}
	investigator, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSSeerSelect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if investigator.RoleName != "Seer" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_seer_select"))
		return
	}
	if !investigator.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_act"))
		return
	}
	var existingCount int
	h.db.Get(&existingCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerApplyInvestigate)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_already_investigated"))
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, T(lang, "err_invalid_target"))
		return
	}
	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil || !target.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_invalid_target"))
		return
	}

	var existing GameAction
	selectErr := h.db.Get(&existing, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerSelectInvestigate)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// clicking the same target again deselects it
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionSeerSelectInvestigate)
		h.logf("Seer '%s' deselected investigation target", investigator.Name)
	} else {
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionSeerSelectInvestigate, targetID, VisibilityActor)
		h.logf("Seer '%s' selected investigation target %d", investigator.Name, targetID)
	}

	h.triggerBroadcast()
}

func handleWSSeerInvestigate(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSSeerInvestigate: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_phase_investigate"))
		return
	}

	investigator, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSSeerInvestigate: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}

	if investigator.RoleName != "Seer" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_seer_investigate"))
		return
	}

	if !investigator.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_act"))
		return
	}

	var existingCount int
	h.db.Get(&existingCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionSeerApplyInvestigate)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_already_investigated"))
		return
	}

	var selectAction GameAction
	if err := h.db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerSelectInvestigate); err != nil || selectAction.TargetPlayerID == nil {
		h.sendErrorToast(client.playerID, T(lang, "err_select_investigate_first"))
		return
	}
	targetID := *selectAction.TargetPlayerID

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil {
		h.sendErrorToast(client.playerID, T(lang, "err_target_not_found"))
		return
	}

	if !target.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_cannot_investigate_dead"))
		return
	}

	h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerSelectInvestigate)

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
		game.ID, game.Round, client.playerID, ActionSeerApplyInvestigate, targetID, VisibilityActor, seerDesc, seerKey, histArgs(game.Round, target.Name))
	if err != nil {
		h.logError("handleWSSeerInvestigate: db.Exec insert investigation", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_record_investigation"))
		return
	}

	toastMsg := T(lang, "toast_seer_not_werewolf", target.Name)
	if target.Team == "werewolf" {
		toastMsg = T(lang, "toast_seer_is_werewolf", target.Name)
	}
	h.sendToPlayer(client.playerID, []byte(renderToast(h.templates, h.logf, "info", toastMsg)))

	h.logf("Seer '%s' investigated '%s' (team: %s)", investigator.Name, target.Name, target.Team)
	DebugLog("handleWSSeerInvestigate", "Seer '%s' investigated '%s' (team: %s)", investigator.Name, target.Name, target.Team)
	LogDBState(h.db, "after seer investigation")

	h.resolveWerewolfVotes(game)
}
