package main

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

// CupidNightData holds night-phase display data for Cupid.
type CupidNightData struct {
	CupidLinked        bool
	CupidChosen1Player *Player
	CupidChosen2Player *Player
}

func buildCupidNightData(db *sqlx.DB, game *Game, playerID int64, player Player, seerInvestigated map[int64]string) CupidNightData {
	if player.RoleName != "Cupid" || game.Round != 1 {
		return CupidNightData{}
	}

	d := CupidNightData{}
	var cupidChosen1ID, cupidChosen2ID int64
	var finalized int
	db.Get(&finalized, `SELECT COUNT(*) FROM game_lovers WHERE game_id = ?`, game.ID)
	if finalized > 0 {
		d.CupidLinked = true
		db.Get(&cupidChosen1ID, `SELECT player1_id FROM game_lovers WHERE game_id = ? LIMIT 1`, game.ID)
		db.Get(&cupidChosen2ID, `SELECT player2_id FROM game_lovers WHERE game_id = ? LIMIT 1`, game.ID)
	} else {
		db.Get(&cupidChosen1ID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
			game.ID, playerID, ActionCupidLink)
		db.Get(&cupidChosen2ID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
			game.ID, playerID, ActionCupidLink2)
	}
	if cupidChosen1ID != 0 {
		d.CupidChosen1Player = getVisiblePlayer(db, game.ID, cupidChosen1ID, player, seerInvestigated)
	}
	if cupidChosen2ID != 0 {
		d.CupidChosen2Player = getVisiblePlayer(db, game.ID, cupidChosen2ID, player, seerInvestigated)
	}
	return d
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
	_, _ = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args) VALUES (?, 1, 'night', ?, ?, ?, ?, ?, ?, ?)`,
		game.ID, firstLoverID, ActionCupidLink, secondLoverID, VisibilityActor, desc1, "hist_cupid_lover", histArgs(second.Name))
	_, _ = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args) VALUES (?, 1, 'night', ?, ?, ?, ?, ?, ?, ?)`,
		game.ID, secondLoverID, ActionCupidLink, firstLoverID, VisibilityActor, desc2, "hist_cupid_lover", histArgs(first.Name))

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
