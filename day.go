package main

import (
	"database/sql"
	"fmt"
	"strconv"
)

type DayData struct {
	Player               *Player
	AliveTargets         []Player
	NightNumber          int
	HasHistory           bool
	NightVictims         []Player
	PassVoters           []string
	CurrentVotePlayer    *Player
	IsAlive              bool
	HunterRevengeNeeded  bool
	HunterRevengeDone    bool
	HunterVictimPlayer   *Player
	IsTheHunter          bool // is this player the dead Hunter needing to shoot?
	HunterSelectedPlayer *Player
	HunterTargets        []Player // alive targets for the Hunter; visibility pre-applied
	AllActed             bool
	HasVoted             bool
	Lang                 string

	NightVictimCards  []PlayerCardData
	HunterTargetCards []PlayerCardData
	VoteTargetCards   []PlayerCardData
}

// applyHeartbreaks recurses so chained heartbreaks resolve (multiple Cupids can link
// overlapping lover pairs). Returns every player ID killed by heartbreak in the chain.
func (h *Hub) applyHeartbreaks(game *Game, phase string, killedIDs []int64) []int64 {
	var allHeartbroken []int64
	toProcess := killedIDs
	for len(toProcess) > 0 {
		var nextRound []int64
		for _, killed := range toProcess {
			partnerID := getLoverPartner(h.db, game.ID, killed)
			if partnerID == 0 {
				continue
			}
			var isAlive bool
			h.db.Get(&isAlive, `SELECT is_alive FROM game_player WHERE game_id = ? AND player_id = ?`, game.ID, partnerID)
			if !isAlive {
				continue
			}
			_, err := h.db.Exec("UPDATE game_player SET is_alive = 0 WHERE game_id = ? AND player_id = ?", game.ID, partnerID)
			if err != nil {
				h.logError("applyHeartbreaks: kill partner", err)
				continue
			}
			// actor = the player whose death triggered this, target = the heartbreak victim
			killedName := getPlayerName(h.db, killed)
			partnerName := getPlayerName(h.db, partnerID)
			heartbreakKey := "hist_heartbreak_night"
			phaseLabel := "Night"
			if phase == "day" {
				heartbreakKey = "hist_heartbreak_day"
				phaseLabel = "Day"
			}
			heartbreakDesc := fmt.Sprintf("%s %d: %s died of heartbreak after their lover %s was killed", phaseLabel, game.Round, partnerName, killedName)
			_, _ = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				game.ID, game.Round, phase, killed, ActionLoverHeartbreak, partnerID, VisibilityPublic, heartbreakDesc, heartbreakKey, histArgs(game.Round, partnerName, killedName))
			h.logf("Heartbreak: '%s' died after their lover '%s' was killed", partnerName, killedName)
			DebugLog("applyHeartbreaks", "'%s' died from heartbreak (lover '%s' was killed)", partnerName, killedName)
			nextRound = append(nextRound, partnerID)
			allHeartbroken = append(allHeartbroken, partnerID)
		}
		toProcess = nextRound
	}
	return allHeartbroken
}

func handleWSDayVote(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSDayVote: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	if game.Status != "day" {
		h.sendErrorToast(client.playerID, T(lang, "err_day_vote_only"))
		return
	}

	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSDayVote: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}

	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_vote"))
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, T(lang, "err_invalid_target"))
		return
	}

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil {
		h.sendErrorToast(client.playerID, T(lang, "err_target_not_found"))
		return
	}

	if !target.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_cannot_vote_dead"))
		return
	}

	var existingTarget sql.NullInt64
	h.db.Get(&existingTarget, `SELECT target_player_id FROM game_action WHERE game_id = ? AND round = ? AND phase = 'day' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionDaySelectKill)
	// voting the same target again retracts the vote
	if existingTarget.Valid && existingTarget.Int64 == targetID {
		_, err = h.db.Exec(`DELETE FROM game_action WHERE game_id = ? AND round = ? AND phase = 'day' AND actor_player_id = ? AND action_type = ?`,
			game.ID, game.Round, client.playerID, ActionDaySelectKill)
		if err != nil {
			h.logError("handleWSDayVote: db.Exec delete vote", err)
			h.sendErrorToast(client.playerID, T(lang, "err_failed_clear_vote"))
			return
		}
		h.logf("Player %d (%s) unselected day vote for player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
		h.triggerBroadcast()
		return
	}

	dayVoteDesc := fmt.Sprintf("Day %d: %s voted to eliminate %s", game.Round, voter.Name, target.Name)
	dvKey, dvArgs := "hist_day_vote", histArgs(game.Round, voter.Name, target.Name)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
		VALUES (?, ?, 'day', ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = ?, description = ?, description_key = ?, description_args = ?`,
		game.ID, game.Round, client.playerID, ActionDaySelectKill, targetID, VisibilityPublic, dayVoteDesc, dvKey, dvArgs, targetID, dayVoteDesc, dvKey, dvArgs)
	if err != nil {
		h.logError("handleWSDayVote: db.Exec insert vote", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_record_vote"))
		return
	}

	h.logf("Player %d (%s) voted to eliminate player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSDayVote", "Player '%s' voted to eliminate '%s'", voter.Name, target.Name)
	LogDBState(h.db, "after day vote")

	h.triggerBroadcast()
}

func handleWSDayPass(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSDayPass: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	if game.Status != "day" {
		h.sendErrorToast(client.playerID, T(lang, "err_day_vote_only"))
		return
	}

	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSDayPass: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}

	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_vote"))
		return
	}

	// Record pass as a day_vote with NULL target
	passDesc := fmt.Sprintf("Day %d: %s passed", game.Round, voter.Name)
	dpKey, dpArgs := "hist_day_pass", histArgs(game.Round, voter.Name)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
		VALUES (?, ?, 'day', ?, ?, NULL, ?, ?, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = NULL, description = ?, description_key = ?, description_args = ?`,
		game.ID, game.Round, client.playerID, ActionDaySelectKill, VisibilityPublic, passDesc, dpKey, dpArgs, passDesc, dpKey, dpArgs)
	if err != nil {
		h.logError("handleWSDayPass: db.Exec", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_record_pass"))
		return
	}

	h.logf("Player %d (%s) passed the day vote", client.playerID, voter.Name)
	h.triggerBroadcast()
}

func handleWSDayEndVote(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSDayEndVote: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	if game.Status != "day" {
		h.sendErrorToast(client.playerID, T(lang, "err_day_vote_only"))
		return
	}

	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSDayEndVote: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}

	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_end_vote"))
		return
	}

	var alivePlayers []Player
	h.db.Select(&alivePlayers, `
		SELECT g.rowid as id, g.player_id as player_id, p.name as name
		FROM game_player g
		JOIN player p ON g.player_id = p.rowid
		WHERE g.game_id = ? AND g.is_alive = 1`, game.ID)

	var totalActed int
	h.db.Get(&totalActed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'day' AND action_type = ?`,
		game.ID, game.Round, ActionDaySelectKill)

	if totalActed < len(alivePlayers) {
		h.sendErrorToast(client.playerID, T(lang, "err_players_not_done", totalActed, len(alivePlayers)))
		return
	}

	h.logf("Day End Vote triggered by player %d (%s)", client.playerID, voter.Name)
	h.resolveDayVotes(game)
}

func (h *Hub) resolveDayVotes(game *Game) {
	var alivePlayers []Player
	err := h.db.Select(&alivePlayers, `
		SELECT g.rowid as id, g.player_id as player_id, p.name as name
		FROM game_player g
		JOIN player p ON g.player_id = p.rowid
		WHERE g.game_id = ? AND g.is_alive = 1`, game.ID)
	if err != nil {
		h.logError("resolveDayVotes: get alive players", err)
		return
	}

	voteCounts, totalVotes, err := getVoteCounts(h.db, game.ID, game.Round, "day", ActionDaySelectKill)
	if err != nil {
		h.logError("resolveDayVotes: getVoteCounts", err)
		return
	}

	h.logf("Day vote check: %d alive players, %d votes", len(alivePlayers), totalVotes)

	realVoteCount := 0
	for _, c := range voteCounts {
		realVoteCount += c
	}
	passCount := totalVotes - realVoteCount
	if passCount > len(alivePlayers)/2 {
		h.logf("Majority passed (%d/%d) — no elimination this day", passCount, len(alivePlayers))
		h.transitionToNight(game)
		return
	}

	var maxVotes int
	var eliminatedID int64
	var isTie bool
	for targetID, count := range voteCounts {
		if count > maxVotes {
			maxVotes = count
			eliminatedID = targetID
			isTie = false
		} else if count == maxVotes {
			isTie = true
		}
	}

	majority := len(alivePlayers)/2 + 1
	if maxVotes < majority || isTie {
		h.logf("No majority reached (need %d, max is %d, tie: %v) - no elimination", majority, maxVotes, isTie)
		h.transitionToNight(game)
		return
	}

	_, err = h.db.Exec("UPDATE game_player SET is_alive = 0 WHERE game_id = ? AND player_id = ?", game.ID, eliminatedID)
	if err != nil {
		h.logError("resolveDayVotes: eliminate player", err)
		return
	}

	eliminatedName := getPlayerName(h.db, eliminatedID)
	eliminatedRole := getRoleName(h.db, game.ID, eliminatedID)

	eliminationDesc := fmt.Sprintf("Day %d: %s (%s) was eliminated by the village", game.Round, eliminatedName, eliminatedRole)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
		VALUES (?, ?, 'day', ?, ?, ?, ?, ?, ?, ?)`,
		game.ID, game.Round, eliminatedID, ActionDayApplyKill, eliminatedID, VisibilityPublic, eliminationDesc, "hist_eliminated", histArgs(game.Round, eliminatedName, eliminatedRole))
	if err != nil {
		h.logError("resolveDayVotes: record elimination", err)
	}
	h.logf("Village eliminated %s (player ID %d)", eliminatedName, eliminatedID)
	DebugLog("resolveDayVotes", "Village eliminated '%s'", eliminatedName)
	h.maybeGenerateStory(game.ID, game.Round, "day", eliminatedID)

	heartbroken := h.applyHeartbreaks(game, "day", []int64{eliminatedID})

	for _, deadID := range append([]int64{eliminatedID}, heartbroken...) {
		if getRoleName(h.db, game.ID, deadID) == "Hunter" {
			deadName := getPlayerName(h.db, deadID)
			h.logf("Hunter '%s' was eliminated — waiting for revenge shot before transitioning", deadName)
			LogDBState(h.db, "after hunter elimination - waiting for revenge")
			h.triggerBroadcast()
			return
		}
	}

	if h.checkWinConditions(game) {
		return // Game ended
	}

	h.transitionToNight(game)
}

func handleWSHunterSelect(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSHunterSelect: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "day" {
		h.sendErrorToast(client.playerID, T(lang, "err_hunter_revenge_inactive"))
		return
	}
	hunter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSHunterSelect: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if hunter.RoleName != "Hunter" {
		h.sendErrorToast(client.playerID, T(lang, "err_hunter_only_select"))
		return
	}
	if hunter.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_hunter_revenge_only_dead"))
		return
	}
	var revengeCount int
	h.db.Get(&revengeCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionHunterApplyKill)
	if revengeCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_already_shot"))
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
		WHERE game_id=? AND round=? AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionHunterSelectKill)
	// clicking the same target again deselects it
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionHunterSelectKill)
		h.logf("Hunter '%s' deselected revenge target", hunter.Name)
	} else {
		h.db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'day', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionHunterSelectKill, targetID, VisibilityActor)
		h.logf("Hunter '%s' selected revenge target %d", hunter.Name, targetID)
	}

	h.triggerBroadcast()
}

func handleWSHunterRevenge(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSHunterRevenge: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	if game.Status != "day" {
		h.sendErrorToast(client.playerID, T(lang, "err_hunter_revenge_inactive"))
		return
	}

	hunter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSHunterRevenge: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}

	if hunter.RoleName != "Hunter" {
		h.sendErrorToast(client.playerID, T(lang, "err_hunter_only_shoot"))
		return
	}

	if hunter.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_hunter_revenge_only_dead"))
		return
	}

	var revengeCount int
	h.db.Get(&revengeCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionHunterApplyKill)
	if revengeCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_already_shot"))
		return
	}

	var selectAction GameAction
	if err := h.db.Get(&selectAction, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionHunterSelectKill); err != nil || selectAction.TargetPlayerID == nil {
		h.sendErrorToast(client.playerID, T(lang, "err_select_shoot_first"))
		return
	}
	targetID := *selectAction.TargetPlayerID

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil {
		h.sendErrorToast(client.playerID, T(lang, "err_target_not_found"))
		return
	}

	if !target.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_cannot_shoot_dead"))
		return
	}

	h.db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionHunterSelectKill)

	_, err = h.db.Exec("UPDATE game_player SET is_alive = 0 WHERE game_id = ? AND player_id = ?", game.ID, targetID)
	if err != nil {
		h.logError("handleWSHunterRevenge: kill target", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_kill_target"))
		return
	}

	hunterRevengeDesc := fmt.Sprintf("Day %d: Hunter %s shot %s", game.Round, hunter.Name, target.Name)
	_, err = h.db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
		VALUES (?, ?, 'day', ?, ?, ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionHunterApplyKill, targetID, VisibilityPublic, hunterRevengeDesc, "hist_hunter_shot", histArgs(game.Round, hunter.Name, target.Name))
	if err != nil {
		h.logError("handleWSHunterRevenge: record action", err)
	}

	h.logf("Hunter '%s' took revenge on '%s'", hunter.Name, target.Name)
	DebugLog("handleWSHunterRevenge", "Hunter '%s' shot '%s'", hunter.Name, target.Name)
	LogDBState(h.db, "after hunter revenge")
	h.maybeGenerateStory(game.ID, game.Round, "day", targetID)

	heartbroken := h.applyHeartbreaks(game, "day", []int64{targetID})

	for _, deadID := range append([]int64{targetID}, heartbroken...) {
		if getRoleName(h.db, game.ID, deadID) == "Hunter" {
			deadName := getPlayerName(h.db, deadID)
			h.logf("Hunter '%s' was killed — entering chained revenge", deadName)
			h.triggerBroadcast()
			return
		}
	}

	if h.checkWinConditions(game) {
		return // Game ended
	}

	// distinguishes whether this revenge chain started from a day-vote elimination
	// (transition to night) or a night kill (stay in day for voting)
	var dayEliminationCount int
	h.db.Get(&dayEliminationCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'day' AND action_type = ?`,
		game.ID, game.Round, ActionDayApplyKill)

	if dayEliminationCount > 0 {
		h.transitionToNight(game)
	} else {
		h.triggerBroadcast()
	}
}
