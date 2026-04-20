package main

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

// GuardNightData holds night-phase display data for the Guard.
type GuardNightData struct {
	GuardHasProtected     bool
	GuardSelectedPlayer   *Player  // pending selection (nil = none)
	GuardProtectingPlayer *Player  // confirmed protection target this night
	GuardTargets          []Player // alive players excluding self and last night's target
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
		game.ID, game.Round, playerID, ActionGuardProtect)
	if err == nil && action.TargetPlayerID != nil {
		d.GuardHasProtected = true
		d.GuardProtectingPlayer = getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated)
	} else {
		var selectAction GameAction
		if db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, playerID, ActionGuardSelect) == nil && selectAction.TargetPlayerID != nil {
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
			game.ID, game.Round-1, playerID, ActionGuardProtect)
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
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionGuardSelect)
		h.logf("Guard '%s' deselected protection target", guard.Name)
	} else {
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

	var existingCount int
	h.db.Get(&existingCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionGuardProtect)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, "You have already protected someone this night")
		return
	}

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

	h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionGuardSelect)

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
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
VALUES (?, ?, 'night', ?, ?, ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionGuardProtect, targetID, VisibilityActor, guardDesc, "hist_protected", histArgs(game.Round, target.Name))
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
