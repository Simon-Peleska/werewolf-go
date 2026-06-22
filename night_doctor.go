package main

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

type DoctorNightData struct {
	HasProtected           bool
	DoctorSelectedPlayer   *Player // pending, not yet confirmed
	DoctorProtectingPlayer *Player // confirmed protection target this night
	DoctorResultCard       *PlayerCardData
	DoctorTargetCards      []PlayerCardData
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
		game.ID, game.Round, playerID, ActionDoctorApplyProtect)
	if err == nil && action.TargetPlayerID != nil {
		return DoctorNightData{
			HasProtected:           true,
			DoctorProtectingPlayer: getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated),
		}
	}

	var selectAction GameAction
	if db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, playerID, ActionDoctorSelectProtect) == nil && selectAction.TargetPlayerID != nil {
		return DoctorNightData{
			DoctorSelectedPlayer: getVisiblePlayer(db, game.ID, *selectAction.TargetPlayerID, player, seerInvestigated),
		}
	}

	return DoctorNightData{}
}

func handleWSDoctorSelect(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSDoctorSelect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_phase_act"))
		return
	}
	doctor, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSDoctorSelect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if doctor.RoleName != "Doctor" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_doctor_select"))
		return
	}
	if !doctor.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_act"))
		return
	}
	var existingCount int
	h.db.Get(&existingCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionDoctorApplyProtect)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_already_protected"))
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
		game.ID, game.Round, client.playerID, ActionDoctorSelectProtect)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// clicking the same target again deselects it
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionDoctorSelectProtect)
		h.logf("Doctor '%s' deselected protection target", doctor.Name)
	} else {
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionDoctorSelectProtect, targetID, VisibilityActor)
		h.logf("Doctor '%s' selected protection target %d", doctor.Name, targetID)
	}

	h.triggerBroadcast()
}

func handleWSDoctorProtect(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSDoctorProtect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_phase_protect"))
		return
	}

	doctor, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSDoctorProtect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}

	if doctor.RoleName != "Doctor" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_doctor_protect"))
		return
	}

	if !doctor.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_act"))
		return
	}

	var existingCount int
	h.db.Get(&existingCount, `
SELECT COUNT(*) FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionDoctorApplyProtect)
	if existingCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_already_protected"))
		return
	}

	var selectAction GameAction
	if err := h.db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionDoctorSelectProtect); err != nil || selectAction.TargetPlayerID == nil {
		h.sendErrorToast(client.playerID, T(lang, "err_select_protect_first"))
		return
	}
	targetID := *selectAction.TargetPlayerID

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
		game.ID, game.Round, client.playerID, ActionDoctorSelectProtect)

	doctorDesc := fmt.Sprintf("Night %d: You protected %s", game.Round, target.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
VALUES (?, ?, 'night', ?, ?, ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionDoctorApplyProtect, targetID, VisibilityActor, doctorDesc, "hist_protected", histArgs(game.Round, target.Name))
	if err != nil {
		h.logError("handleWSDoctorProtect: db.Exec insert protection", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_record_protection"))
		return
	}

	h.logf("Doctor '%s' is protecting '%s'", doctor.Name, target.Name)
	DebugLog("handleWSDoctorProtect", "Doctor '%s' protecting '%s'", doctor.Name, target.Name)
	LogDBState(h.db, "after doctor protect")

	h.resolveWerewolfVotes(game)
}
