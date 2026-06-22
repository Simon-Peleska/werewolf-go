package main

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

type DoppelgangerNightData struct {
	DoppelgangerHasCopied      bool
	DoppelgangerSelectedPlayer *Player
	DoppelgangerCopiedPlayer   *Player
	DoppelgangerTargets        []Player
	DoppelgangerResultCard     *PlayerCardData
	DoppelgangerTargetCards    []PlayerCardData
}

func buildDoppelgangerNightData(db *sqlx.DB, game *Game, playerID int64, player Player, seerInvestigated map[int64]string, aliveTargets []Player) DoppelgangerNightData {
	if player.RoleName != "Doppelganger" || game.Round != 1 {
		return DoppelgangerNightData{}
	}

	d := DoppelgangerNightData{}

	var copyAction GameAction
	if err := db.Get(&copyAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, playerID, ActionDoppelgangerApplyCopy); err == nil && copyAction.TargetPlayerID != nil {
		d.DoppelgangerHasCopied = true
		target, err := getPlayerInGame(db, game.ID, *copyAction.TargetPlayerID)
		if err == nil {
			d.DoppelgangerCopiedPlayer = &target
		}
	} else {
		var selectAction GameAction
		if db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, playerID, ActionDoppelgangerSelectCopy) == nil && selectAction.TargetPlayerID != nil {
			d.DoppelgangerSelectedPlayer = getVisiblePlayer(db, game.ID, *selectAction.TargetPlayerID, player, seerInvestigated)
		}
	}

	for _, t := range aliveTargets {
		if t.PlayerID != playerID {
			d.DoppelgangerTargets = append(d.DoppelgangerTargets, t)
		}
	}

	return d
}

func handleWSDoppelgangerSelect(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSDoppelgangerSelect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" || game.Round != 1 {
		h.sendErrorToast(client.playerID, T(lang, "err_doppelganger_night1_only"))
		return
	}
	doppelganger, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSDoppelgangerSelect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if doppelganger.RoleName != "Doppelganger" || !doppelganger.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_doppelganger_only_living"))
		return
	}
	var copiedCount int
	h.db.Get(&copiedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionDoppelgangerApplyCopy)
	if copiedCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_doppelganger_already_chosen"))
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, T(lang, "err_invalid_target"))
		return
	}
	if targetID == client.playerID {
		h.sendErrorToast(client.playerID, T(lang, "err_cannot_copy_self"))
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
WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionDoppelgangerSelectCopy)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// clicking the same target again deselects it
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, client.playerID, ActionDoppelgangerSelectCopy)
		h.logf("Doppelganger '%s' deselected copy target", doppelganger.Name)
	} else {
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, 1, 'night', ?, ?, ?, ?, '')`,
			game.ID, client.playerID, ActionDoppelgangerSelectCopy, targetID, VisibilityActor)
		h.logf("Doppelganger '%s' selected copy target %d", doppelganger.Name, targetID)
	}

	h.triggerBroadcast()
}

func handleWSDoppelgangerCopy(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSDoppelgangerCopy: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" || game.Round != 1 {
		h.sendErrorToast(client.playerID, T(lang, "err_doppelganger_night1_only"))
		return
	}
	doppelganger, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSDoppelgangerCopy: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if doppelganger.RoleName != "Doppelganger" || !doppelganger.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_doppelganger_only_living"))
		return
	}
	var copiedCount int
	h.db.Get(&copiedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionDoppelgangerApplyCopy)
	if copiedCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_doppelganger_already_chosen"))
		return
	}

	var selectAction GameAction
	if err := h.db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionDoppelgangerSelectCopy); err != nil || selectAction.TargetPlayerID == nil {
		h.sendErrorToast(client.playerID, T(lang, "err_select_copy_first"))
		return
	}
	targetID := *selectAction.TargetPlayerID

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil || !target.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_target_not_found"))
		return
	}

	var targetRoleID, originalRoleID int64
	h.db.Get(&targetRoleID, `SELECT role_id FROM game_player WHERE game_id = ? AND player_id = ?`, game.ID, targetID)
	h.db.Get(&originalRoleID, `SELECT role_id FROM game_player WHERE game_id = ? AND player_id = ?`, game.ID, client.playerID)

	// original_role_id marks them as a former Doppelganger for the end-game reveal
	if _, err := h.db.Exec(`UPDATE game_player SET role_id = ?, original_role_id = ? WHERE game_id = ? AND player_id = ?`,
		targetRoleID, originalRoleID, game.ID, client.playerID); err != nil {
		h.logError("handleWSDoppelgangerCopy: update role", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_apply_role_change"))
		return
	}

	h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionDoppelgangerSelectCopy)
	copyDesc := fmt.Sprintf("Night 1: You secretly became a %s (copied from %s)", target.RoleName, target.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
VALUES (?, 1, 'night', ?, ?, ?, ?, ?, ?, ?)`,
		game.ID, client.playerID, ActionDoppelgangerApplyCopy, targetID, VisibilityActor, copyDesc, "hist_doppelganger", histArgs(target.RoleName, target.Name))
	if err != nil {
		h.logError("handleWSDoppelgangerCopy: insert copy action", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_record_copy"))
		return
	}

	toastMsg := T(lang, "toast_doppelganger_became", T(lang, "role_name_"+target.RoleName))
	h.sendToPlayer(client.playerID, []byte(renderToast(h.templates, h.logf, "info", toastMsg)))

	// the Seer's earlier reading is now stale: it called them a villager before they became a werewolf
	if target.Team == "werewolf" {
		var seerInvestigations []struct {
			ActorPlayerID int64 `db:"actor_player_id"`
		}
		h.db.Select(&seerInvestigations, `
SELECT actor_player_id FROM game_action
WHERE game_id = ? AND action_type = ? AND target_player_id = ?`,
			game.ID, ActionSeerApplyInvestigate, doppelganger.PlayerID)
		for _, inv := range seerInvestigations {
			notif := T(h.getPlayerLang(inv.ActorPlayerID), "toast_seer_outdated_reading", doppelganger.Name)
			h.sendToPlayer(inv.ActorPlayerID, []byte(renderToast(h.templates, h.logf, "warning", notif)))
		}
	}

	h.logf("Doppelganger '%s' immediately became a %s (copied from '%s')", doppelganger.Name, target.RoleName, target.Name)
	LogDBState(h.db, "after doppelganger copy")
	h.resolveWerewolfVotes(game)
}
