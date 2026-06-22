package main

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

type WitchNightData struct {
	WerewolfVictimPlayer     *Player
	WerewolfVictimPlayer2    *Player
	HealPotionUsed           bool
	PoisonPotionUsed         bool
	WitchPendingHealPlayer   *Player
	WitchPendingPoisonPlayer *Player
	WitchHealedThisNight     bool
	WitchHealedPlayer        *Player
	WitchPoisonedThisNight   bool
	WitchPoisonedPlayer      *Player
	WitchDoneThisNight       bool
	WitchHealCards           []PlayerCardData
	WitchPoisonCards         []PlayerCardData
}

func buildWitchNightData(db *sqlx.DB, game *Game, playerID int64, player Player, seerInvestigated map[int64]string) WitchNightData {
	if player.RoleName != "Witch" {
		return WitchNightData{}
	}

	d := WitchNightData{}

	var healUsed, poisonUsed int
	db.Get(&healUsed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND actor_player_id = ? AND action_type = ?`,
		game.ID, playerID, ActionWitchApplyProtect)
	db.Get(&poisonUsed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND actor_player_id = ? AND action_type = ?`,
		game.ID, playerID, ActionWitchApplyKill)
	d.HealPotionUsed = healUsed > 0
	d.PoisonPotionUsed = poisonUsed > 0

	var selectHealAction GameAction
	if err := db.Get(&selectHealAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionWitchSelectProtect); err == nil && selectHealAction.TargetPlayerID != nil {
		d.WitchPendingHealPlayer = getVisiblePlayer(db, game.ID, *selectHealAction.TargetPlayerID, player, seerInvestigated)
	}

	var selectPoisonAction GameAction
	if err := db.Get(&selectPoisonAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionWitchSelectKill); err == nil && selectPoisonAction.TargetPlayerID != nil {
		d.WitchPendingPoisonPlayer = getVisiblePlayer(db, game.ID, *selectPoisonAction.TargetPlayerID, player, seerInvestigated)
	}

	var healAction GameAction
	if err := db.Get(&healAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionWitchApplyProtect); err == nil {
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
		game.ID, game.Round, playerID, ActionWitchApplyKill); err == nil && killedAction.TargetPlayerID != nil {
		d.WitchPoisonedThisNight = true
		d.WitchPoisonedPlayer = getVisiblePlayer(db, game.ID, *killedAction.TargetPlayerID, player, seerInvestigated)
	}

	var doneCount int
	db.Get(&doneCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, playerID, ActionWitchApply)
	d.WitchDoneThisNight = doneCount > 0

	// Witch sees the wolf victim only after End Vote is pressed
	var endVoteCount int
	db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
		game.ID, game.Round, ActionWerewolfApplyKill)
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
			game.ID, game.Round, ActionWerewolfSelectKill)

		var totalWerewolves int
		db.Get(&totalWerewolves, `
SELECT COUNT(*) FROM game_player gp JOIN role r ON gp.role_id = r.rowid
WHERE gp.game_id = ? AND gp.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

		if len(wvotes) > 0 && totalWerewolves > 0 {
			majority := totalWerewolves/2 + 1
			if wvotes[0].Count >= majority {
				d.WerewolfVictimPlayer = getVisiblePlayer(db, game.ID, wvotes[0].TargetPlayerID, player, seerInvestigated)
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
AND ga.action_type IN (?, ?, ?, ?)
AND r.name = 'Wolf Cub'`,
				game.ID, game.Round-1, ActionWerewolfSelectKill, ActionDayApplyKill, ActionHunterApplyKill, ActionWitchApplyKill)
			if wolfCubDeathCount > 0 {
				var endVote2Count int
				db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
					game.ID, game.Round, ActionWerewolfApplyKill2)
				if endVote2Count > 0 {
					var wvotes2 []voteCount
					db.Select(&wvotes2, `
SELECT ga.target_player_id, COUNT(*) as count
FROM game_action ga
WHERE ga.game_id = ? AND ga.round = ? AND ga.phase = 'night' AND ga.action_type = ?
GROUP BY ga.target_player_id
ORDER BY count DESC`,
						game.ID, game.Round, ActionWerewolfSelectKill2)
					majority := totalWerewolves/2 + 1
					if len(wvotes2) > 0 && wvotes2[0].Count >= majority {
						d.WerewolfVictimPlayer2 = getVisiblePlayer(db, game.ID, wvotes2[0].TargetPlayerID, player, seerInvestigated)
					}
				}
			}
		}
	}

	return d
}

func handleWSWitchSelectHeal(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWitchSelectHeal: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_phase_act"))
		return
	}
	witch, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWitchSelectHeal: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if witch.RoleName != "Witch" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_witch_select_heal"))
		return
	}
	if !witch.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_act"))
		return
	}
	var appliedCount int
	h.db.Get(&appliedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchApply)
	if appliedCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_already_submitted_night"))
		return
	}
	var healUsed int
	h.db.Get(&healUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionWitchApplyProtect)
	if healUsed > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_heal_already_used"))
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, T(lang, "err_invalid_target"))
		return
	}

	var existing GameAction
	selectErr := h.db.Get(&existing, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectProtect)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// clicking the same target again deselects it
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionWitchSelectProtect)
		h.logf("Witch '%s' deselected heal target", witch.Name)
	} else {
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionWitchSelectProtect, targetID, VisibilityActor)
		h.logf("Witch '%s' selected heal target %d", witch.Name, targetID)
	}

	h.triggerBroadcast()
}

func handleWSWitchSelectPoison(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWitchSelectPoison: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_phase_act"))
		return
	}
	witch, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWitchSelectPoison: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if witch.RoleName != "Witch" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_witch_select_poison"))
		return
	}
	if !witch.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_act"))
		return
	}
	var appliedCount int
	h.db.Get(&appliedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchApply)
	if appliedCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_already_submitted_night"))
		return
	}
	var poisonUsed int
	h.db.Get(&poisonUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionWitchApplyKill)
	if poisonUsed > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_poison_already_used"))
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, T(lang, "err_invalid_target"))
		return
	}

	var existing GameAction
	selectErr := h.db.Get(&existing, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectKill)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// clicking the same target again deselects it
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionWitchSelectKill)
		h.logf("Witch '%s' deselected poison target", witch.Name)
	} else {
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionWitchSelectKill, targetID, VisibilityActor)
		h.logf("Witch '%s' selected poison target %d", witch.Name, targetID)
	}

	h.triggerBroadcast()
}

func handleWSWitchApply(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWitchApply: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_phase_act"))
		return
	}
	witch, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWitchApply: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if witch.RoleName != "Witch" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_witch_apply"))
		return
	}
	if !witch.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_act"))
		return
	}
	var appliedCount int
	h.db.Get(&appliedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchApply)
	if appliedCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_already_submitted_night"))
		return
	}

	var healAction GameAction
	if err := h.db.Get(&healAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectProtect); err == nil && healAction.TargetPlayerID != nil {

		targetID := *healAction.TargetPlayerID
		var healUsed int
		h.db.Get(&healUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
			game.ID, client.playerID, ActionWitchApplyProtect)
		if healUsed > 0 {
			h.sendErrorToast(client.playerID, T(lang, "err_heal_already_used"))
			return
		}
		if targetID == client.playerID {
			h.sendErrorToast(client.playerID, T(lang, "err_cannot_heal_self"))
			return
		}
		var endVoteCount int
		h.db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
			game.ID, game.Round, ActionWerewolfApplyKill)
		if endVoteCount == 0 {
			h.sendErrorToast(client.playerID, T(lang, "err_werewolves_not_locked"))
			return
		}
		var isVictim int
		h.db.Get(&isVictim, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type IN (?,?) AND target_player_id=?`,
			game.ID, game.Round, ActionWerewolfSelectKill, ActionWerewolfSelectKill2, targetID)
		if isVictim == 0 {
			h.sendErrorToast(client.playerID, T(lang, "err_heal_must_target_werewolf"))
			return
		}
		var targetName string
		h.db.Get(&targetName, "SELECT name FROM player WHERE rowid = ?", targetID)
		witchHealDesc := fmt.Sprintf("Night %d: You saved %s with your heal potion", game.Round, targetName)
		_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
VALUES (?, ?, 'night', ?, ?, ?, ?, ?, ?, ?)`,
			game.ID, game.Round, client.playerID, ActionWitchApplyProtect, targetID, VisibilityActor, witchHealDesc, "hist_witch_heal", histArgs(game.Round, targetName))
		if err != nil {
			h.logError("handleWSWitchApply: commit heal", err)
			h.sendErrorToast(client.playerID, T(lang, "err_failed_commit_heal"))
			return
		}
		h.logf("Witch '%s' committed heal on player %d (%s)", witch.Name, targetID, targetName)
	}

	var poisonAction GameAction
	if err := h.db.Get(&poisonAction, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectKill); err == nil && poisonAction.TargetPlayerID != nil {

		targetID := *poisonAction.TargetPlayerID
		var poisonUsed int
		h.db.Get(&poisonUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
			game.ID, client.playerID, ActionWitchApplyKill)
		if poisonUsed > 0 {
			h.sendErrorToast(client.playerID, T(lang, "err_poison_already_used"))
			return
		}
		target, err := getPlayerInGame(h.db, game.ID, targetID)
		if err != nil || !target.IsAlive {
			h.sendErrorToast(client.playerID, T(lang, "err_poison_target_invalid"))
			return
		}
		witchKillDesc := fmt.Sprintf("Night %d: You poisoned %s", game.Round, target.Name)
		_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
VALUES (?, ?, 'night', ?, ?, ?, ?, ?, ?, ?)`,
			game.ID, game.Round, client.playerID, ActionWitchApplyKill, targetID, VisibilityActor, witchKillDesc, "hist_witch_poison", histArgs(game.Round, target.Name))
		if err != nil {
			h.logError("handleWSWitchApply: commit poison", err)
			h.sendErrorToast(client.playerID, T(lang, "err_failed_commit_poison"))
			return
		}
		h.logf("Witch '%s' committed poison on player %d (%s)", witch.Name, targetID, target.Name)
	}

	witchApplyDesc := fmt.Sprintf("Night %d: Witch %s confirmed her actions", game.Round, witch.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, visibility, description, description_key, description_args)
VALUES (?, ?, 'night', ?, ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionWitchApply, VisibilityActor, witchApplyDesc, "hist_witch_confirmed", histArgs(game.Round, witch.Name))
	if err != nil {
		h.logError("handleWSWitchApply: insert apply", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_record_witch_action"))
		return
	}

	h.logf("Witch '%s' applied actions for night %d", witch.Name, game.Round)
	DebugLog("handleWSWitchApply", "Witch '%s' applied", witch.Name)
	LogDBState(h.db, "after witch apply")

	h.resolveWerewolfVotes(game)
}
