package main

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

// WitchNightData holds night-phase display data for the Witch.
type WitchNightData struct {
	WitchVictimPlayer         *Player // werewolf majority target visible to witch (nil = none/not yet)
	WitchVictimPlayer2        *Player // Wolf Cub second kill target (nil = not set)
	HealPotionUsed            bool
	PoisonPotionUsed          bool
	WitchSelectedHealPlayer   *Player // pending heal selection
	WitchSelectedPoisonPlayer *Player // pending poison selection
	WitchHealedThisNight      bool
	WitchHealedPlayer         *Player // who was healed this night
	WitchKilledThisNight      bool
	WitchKilledPlayer         *Player // who was poisoned this night
	WitchDoneThisNight        bool    // true after witch_apply submitted
}

func buildWitchNightData(db *sqlx.DB, game *Game, playerID int64, player Player, seerInvestigated map[int64]string) WitchNightData {
	if player.RoleName != "Witch" {
		return WitchNightData{}
	}

	d := WitchNightData{}

	var healUsed, poisonUsed int
	db.Get(&healUsed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND actor_player_id = ? AND action_type = ?`,
		game.ID, playerID, ActionWitchHeal)
	db.Get(&poisonUsed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND actor_player_id = ? AND action_type = ?`,
		game.ID, playerID, ActionWitchKill)
	d.HealPotionUsed = healUsed > 0
	d.PoisonPotionUsed = poisonUsed > 0

	var selectHealAction GameAction
	if err := db.Get(&selectHealAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionWitchSelectHeal); err == nil && selectHealAction.TargetPlayerID != nil {
		d.WitchSelectedHealPlayer = getVisiblePlayer(db, game.ID, *selectHealAction.TargetPlayerID, player, seerInvestigated)
	}

	var selectPoisonAction GameAction
	if err := db.Get(&selectPoisonAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionWitchSelectPoison); err == nil && selectPoisonAction.TargetPlayerID != nil {
		d.WitchSelectedPoisonPlayer = getVisiblePlayer(db, game.ID, *selectPoisonAction.TargetPlayerID, player, seerInvestigated)
	}

	var healAction GameAction
	if err := db.Get(&healAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionWitchHeal); err == nil {
		d.WitchHealedThisNight = true
		if healAction.TargetPlayerID != nil {
			d.WitchHealedPlayer = getVisiblePlayer(db, game.ID, *healAction.TargetPlayerID, player, seerInvestigated)
		}
	}

	var killedAction GameAction
	if err := db.Get(&killedAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionWitchKill); err == nil && killedAction.TargetPlayerID != nil {
		d.WitchKilledThisNight = true
		d.WitchKilledPlayer = getVisiblePlayer(db, game.ID, *killedAction.TargetPlayerID, player, seerInvestigated)
	}

	var doneCount int
	db.Get(&doneCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionWitchApply)
	d.WitchDoneThisNight = doneCount > 0

	// Witch sees the wolf victim only after End Vote is pressed
	var endVoteCount int
	db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
		game.ID, game.Round, ActionWerewolfEndVote)
	if endVoteCount > 0 {
		type voteCount struct {
			TargetPlayerID int64 `db:"target_player_id"`
			Count          int   `db:"count"`
		}
		var wvotes []voteCount
		db.Select(&wvotes, `
SELECT ga.target_player_id, COUNT(*) as count
FROM game_action ga
WHERE ga.game_id = ? AND ga.round = ? AND ga.phase = 'night' AND ga.action_type = ?
GROUP BY ga.target_player_id
ORDER BY count DESC`,
			game.ID, game.Round, ActionWerewolfKill)

		var totalWerewolves int
		db.Get(&totalWerewolves, `
SELECT COUNT(*) FROM game_player gp JOIN role r ON gp.role_id = r.rowid
WHERE gp.game_id = ? AND gp.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

		if len(wvotes) > 0 && totalWerewolves > 0 {
			majority := totalWerewolves/2 + 1
			if wvotes[0].Count >= majority {
				d.WitchVictimPlayer = getVisiblePlayer(db, game.ID, wvotes[0].TargetPlayerID, player, seerInvestigated)
			}
		}

		// Wolf Cub second victim — only visible after End Vote 2 is also pressed
		if game.Round > 1 && totalWerewolves > 0 {
			var wolfCubDeathCount int
			db.Get(&wolfCubDeathCount, `
SELECT COUNT(*) FROM game_action ga
JOIN game_player gp ON ga.target_player_id = gp.player_id AND gp.game_id = ga.game_id
JOIN role r ON gp.role_id = r.rowid
WHERE ga.game_id = ? AND ga.round = ?
AND ga.action_type IN ('werewolf_kill', 'elimination', 'hunter_revenge', 'witch_kill')
AND r.name = 'Wolf Cub'`,
				game.ID, game.Round-1)
			if wolfCubDeathCount > 0 {
				var endVote2Count int
				db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
					game.ID, game.Round, ActionWerewolfEndVote2)
				if endVote2Count > 0 {
					var wvotes2 []voteCount
					db.Select(&wvotes2, `
SELECT ga.target_player_id, COUNT(*) as count
FROM game_action ga
WHERE ga.game_id = ? AND ga.round = ? AND ga.phase = 'night' AND ga.action_type = ?
GROUP BY ga.target_player_id
ORDER BY count DESC`,
						game.ID, game.Round, ActionWerewolfKill2)
					majority := totalWerewolves/2 + 1
					if len(wvotes2) > 0 && wvotes2[0].Count >= majority {
						d.WitchVictimPlayer2 = getVisiblePlayer(db, game.ID, wvotes2[0].TargetPlayerID, player, seerInvestigated)
					}
				}
			}
		}
	}

	return d
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
	var appliedCount int
	h.db.Get(&appliedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchApply)
	if appliedCount > 0 {
		h.sendErrorToast(client.playerID, "You have already submitted your actions for this night")
		return
	}
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

	var existing GameAction
	selectErr := h.db.Get(&existing, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectHeal)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionWitchSelectHeal)
		h.logf("Witch '%s' deselected heal target", witch.Name)
	} else {
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
	var appliedCount int
	h.db.Get(&appliedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchApply)
	if appliedCount > 0 {
		h.sendErrorToast(client.playerID, "You have already submitted your actions for this night")
		return
	}
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

	var existing GameAction
	selectErr := h.db.Get(&existing, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectPoison)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionWitchSelectPoison)
		h.logf("Witch '%s' deselected poison target", witch.Name)
	} else {
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
		var healUsed int
		h.db.Get(&healUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
			game.ID, client.playerID, ActionWitchHeal)
		if healUsed > 0 {
			h.sendErrorToast(client.playerID, "Your heal potion has already been used")
			return
		}
		if targetID == client.playerID {
			h.sendErrorToast(client.playerID, "You cannot heal yourself")
			return
		}
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
