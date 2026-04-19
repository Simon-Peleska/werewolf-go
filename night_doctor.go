package main

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

// DoctorNightData holds night-phase display data for the Doctor.
type DoctorNightData struct {
	HasProtected           bool
	DoctorSelectedPlayer   *Player // pending selection (nil = none)
	DoctorProtectingPlayer *Player // confirmed protection target this night
}

func buildDoctorNightData(db *sqlx.DB, game *Game, playerID int64, player Player, seerInvestigated map[int64]string) DoctorNightData {
	if player.RoleName != "Doctor" {
		return DoctorNightData{}
	}

	var action GameAction
	err := db.Get(&action, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionDoctorProtect)
	if err == nil && action.TargetPlayerID != nil {
		return DoctorNightData{
			HasProtected:           true,
			DoctorProtectingPlayer: getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated),
		}
	}

	// Pending selection
	var selectAction GameAction
	if db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, playerID, ActionDoctorSelect) == nil && selectAction.TargetPlayerID != nil {
		return DoctorNightData{
			DoctorSelectedPlayer: getVisiblePlayer(db, game.ID, *selectAction.TargetPlayerID, player, seerInvestigated),
		}
	}

	return DoctorNightData{}
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
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionDoctorSelect)
		h.logf("Doctor '%s' deselected protection target", doctor.Name)
	} else {
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

	var existingCount int
	h.db.Get(&existingCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionDoctorProtect)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, "You have already protected someone this night")
		return
	}

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
