package main

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

// DoppelgangerNightData holds night-phase display data for the Doppelganger.
type DoppelgangerNightData struct {
	DoppelgangerHasCopied      bool
	DoppelgangerSelectedPlayer *Player
	DoppelgangerCopiedPlayer   *Player
	DoppelgangerTargets        []Player
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
		game.ID, playerID, ActionDoppelgangerCopy); err == nil && copyAction.TargetPlayerID != nil {
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
			game.ID, playerID, ActionDoppelgangerSelect) == nil && selectAction.TargetPlayerID != nil {
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

// handleWSDoppelgangerSelect toggles the Doppelganger's pending copy target on Night 1.
func handleWSDoppelgangerSelect(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSDoppelgangerSelect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" || game.Round != 1 {
		h.sendErrorToast(client.playerID, "Doppelganger can only act on Night 1")
		return
	}
	doppelganger, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSDoppelgangerSelect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if doppelganger.RoleName != "Doppelganger" || !doppelganger.IsAlive {
		h.sendErrorToast(client.playerID, "Only the living Doppelganger can copy a role")
		return
	}
	var copiedCount int
	h.db.Get(&copiedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionDoppelgangerCopy)
	if copiedCount > 0 {
		h.sendErrorToast(client.playerID, "You have already chosen a role to copy")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, "Invalid target")
		return
	}
	if targetID == client.playerID {
		h.sendErrorToast(client.playerID, "You cannot copy yourself")
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
WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionDoppelgangerSelect)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, client.playerID, ActionDoppelgangerSelect)
		h.logf("Doppelganger '%s' deselected copy target", doppelganger.Name)
	} else {
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, 1, 'night', ?, ?, ?, ?, '')`,
			game.ID, client.playerID, ActionDoppelgangerSelect, targetID, VisibilityActor)
		h.logf("Doppelganger '%s' selected copy target %d", doppelganger.Name, targetID)
	}

	h.triggerBroadcast()
}

// handleWSDoppelgangerCopy finalizes the Doppelganger's choice on Night 1.
// The role change is applied immediately so the player sees their new role right away.
// A newly-transformed Doppelganger is excluded from Night 1 blocking role checks
// (they act as their new role starting from Night 2).
func handleWSDoppelgangerCopy(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSDoppelgangerCopy: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" || game.Round != 1 {
		h.sendErrorToast(client.playerID, "Doppelganger can only act on Night 1")
		return
	}
	doppelganger, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSDoppelgangerCopy: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if doppelganger.RoleName != "Doppelganger" || !doppelganger.IsAlive {
		h.sendErrorToast(client.playerID, "Only the living Doppelganger can copy a role")
		return
	}
	var copiedCount int
	h.db.Get(&copiedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionDoppelgangerCopy)
	if copiedCount > 0 {
		h.sendErrorToast(client.playerID, "You have already chosen a role to copy")
		return
	}

	var selectAction GameAction
	if err := h.db.Get(&selectAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionDoppelgangerSelect); err != nil || selectAction.TargetPlayerID == nil {
		h.sendErrorToast(client.playerID, "Select a player to copy first")
		return
	}
	targetID := *selectAction.TargetPlayerID

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil || !target.IsAlive {
		h.sendErrorToast(client.playerID, "Target not found")
		return
	}

	// Get role IDs for the swap
	var targetRoleID, originalRoleID int64
	h.db.Get(&targetRoleID, `SELECT role_id FROM game_player WHERE game_id = ? AND player_id = ?`, game.ID, targetID)
	h.db.Get(&originalRoleID, `SELECT role_id FROM game_player WHERE game_id = ? AND player_id = ?`, game.ID, client.playerID)

	// Apply role change immediately; original_role_id marks this player as a former Doppelganger
	if _, err := h.db.Exec(`UPDATE game_player SET role_id = ?, original_role_id = ? WHERE game_id = ? AND player_id = ?`,
		targetRoleID, originalRoleID, game.ID, client.playerID); err != nil {
		h.logError("handleWSDoppelgangerCopy: update role", err)
		h.sendErrorToast(client.playerID, "Failed to apply role change")
		return
	}

	// Remove pending selection and record the copy for history
	h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=1 AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionDoppelgangerSelect)
	copyDesc := fmt.Sprintf("Night 1: You secretly became a %s (copied from %s)", target.RoleName, target.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
VALUES (?, 1, 'night', ?, ?, ?, ?, ?)`,
		game.ID, client.playerID, ActionDoppelgangerCopy, targetID, VisibilityActor, copyDesc)
	if err != nil {
		h.logError("handleWSDoppelgangerCopy: insert copy action", err)
		h.sendErrorToast(client.playerID, "Failed to record copy")
		return
	}

	toastMsg := fmt.Sprintf("🎭 You are now a %s!", target.RoleName)
	h.sendToPlayer(client.playerID, []byte(renderToast(h.templates, h.logf, "info", toastMsg)))

	// Notify any Seers who previously investigated the Doppelganger if the copied role is werewolf team
	if target.Team == "werewolf" {
		var seerInvestigations []struct {
			ActorPlayerID int64 `db:"actor_player_id"`
		}
		h.db.Select(&seerInvestigations, `
SELECT actor_player_id FROM game_action
WHERE game_id = ? AND action_type = ? AND target_player_id = ?`,
			game.ID, ActionSeerInvestigate, doppelganger.PlayerID)
		for _, inv := range seerInvestigations {
			notif := fmt.Sprintf("⚠️ %s (whom you investigated) has become a werewolf — your earlier reading is outdated!", doppelganger.Name)
			h.sendToPlayer(inv.ActorPlayerID, []byte(renderToast(h.templates, h.logf, "warning", notif)))
		}
	}

	h.logf("Doppelganger '%s' immediately became a %s (copied from '%s')", doppelganger.Name, target.RoleName, target.Name)
	LogDBState(h.db, "after doppelganger copy")
	h.resolveWerewolfVotes(game)
}
