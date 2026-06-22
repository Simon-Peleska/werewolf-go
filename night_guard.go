package main

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

type GuardNightData struct {
	GuardHasProtected     bool
	GuardSelectedPlayer   *Player // pending, not yet confirmed
	GuardProtectingPlayer *Player // confirmed protection target this night
	GuardTargets          []Player
	GuardResultCard       *PlayerCardData
	GuardTargetCards      []PlayerCardData
}

func buildGuardNightData(db *sqlx.DB, game *Game, playerID int64, player Player, seerInvestigated map[int64]string, aliveTargets []Player) GuardNightData {
	if player.RoleName != "Guard" {
		return GuardNightData{}
	}

	d := GuardNightData{}

	var action GameAction
	err := db.Get(&action, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionGuardApplyProtect)
	if err == nil && action.TargetPlayerID != nil {
		d.GuardHasProtected = true
		d.GuardProtectingPlayer = getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated)
	} else {
		var selectAction GameAction
		if db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, playerID, ActionGuardSelectProtect) == nil && selectAction.TargetPlayerID != nil {
			d.GuardSelectedPlayer = getVisiblePlayer(db, game.ID, *selectAction.TargetPlayerID, player, seerInvestigated)
		}
	}

	var lastProtectedID int64
	if game.Round > 1 {
		var lastAction GameAction
		err := db.Get(&lastAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
			game.ID, game.Round-1, playerID, ActionGuardApplyProtect)
		if err == nil && lastAction.TargetPlayerID != nil {
			lastProtectedID = *lastAction.TargetPlayerID
		}
	}
	for _, t := range aliveTargets {
		if t.PlayerID == playerID {
			continue // cannot protect self
		}
		if lastProtectedID != 0 && t.PlayerID == lastProtectedID {
			continue // cannot protect same player as last night
		}
		d.GuardTargets = append(d.GuardTargets, t)
	}

	return d
}

func handleWSGuardSelect(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSGuardSelect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_phase_act"))
		return
	}
	guard, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSGuardSelect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if guard.RoleName != "Guard" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_guard_select"))
		return
	}
	if !guard.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_act"))
		return
	}
	var existingCount int
	h.db.Get(&existingCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionGuardApplyProtect)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_already_protected"))
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, T(lang, "err_invalid_target"))
		return
	}
	if targetID == client.playerID {
		h.sendErrorToast(client.playerID, T(lang, "err_guard_no_self"))
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
		game.ID, game.Round, client.playerID, ActionGuardSelectProtect)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// clicking the same target again deselects it
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionGuardSelectProtect)
		h.logf("Guard '%s' deselected protection target", guard.Name)
	} else {
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionGuardSelectProtect, targetID, VisibilityActor)
		h.logf("Guard '%s' selected protection target %d", guard.Name, targetID)
	}

	h.triggerBroadcast()
}

func handleWSGuardProtect(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSGuardProtect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_phase_protect"))
		return
	}

	guard, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSGuardProtect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}

	if guard.RoleName != "Guard" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_guard_protect"))
		return
	}

	if !guard.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_act"))
		return
	}

	var existingCount int
	h.db.Get(&existingCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionGuardApplyProtect)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_already_protected"))
		return
	}

	var selectAction GameAction
	if err := h.db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionGuardSelectProtect); err != nil || selectAction.TargetPlayerID == nil {
		h.sendErrorToast(client.playerID, T(lang, "err_select_protect_first"))
		return
	}
	targetID := *selectAction.TargetPlayerID

	if targetID == client.playerID {
		h.sendErrorToast(client.playerID, T(lang, "err_guard_no_self"))
		return
	}

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil {
		h.sendErrorToast(client.playerID, T(lang, "err_target_not_found"))
		return
	}

	if !target.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_cannot_protect_dead"))
		return
	}

	h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionGuardSelectProtect)

	if game.Round > 1 {
		var lastTargetID int64
		err := h.db.Get(&lastTargetID, `
SELECT target_player_id FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
			game.ID, game.Round-1, client.playerID, ActionGuardApplyProtect)
		if err == nil && lastTargetID == targetID {
			h.sendErrorToast(client.playerID, T(lang, "err_guard_no_repeat"))
			return
		}
	}

	guardDesc := fmt.Sprintf("Night %d: You protected %s", game.Round, target.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
VALUES (?, ?, 'night', ?, ?, ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionGuardApplyProtect, targetID, VisibilityActor, guardDesc, "hist_protected", histArgs(game.Round, target.Name))
	if err != nil {
		h.logError("handleWSGuardProtect: db.Exec insert protection", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_record_protection"))
		return
	}

	h.logf("Guard '%s' is protecting '%s'", guard.Name, target.Name)
	DebugLog("handleWSGuardProtect", "Guard '%s' protecting '%s'", guard.Name, target.Name)
	LogDBState(h.db, "after guard protect")

	h.resolveWerewolfVotes(game)
}
