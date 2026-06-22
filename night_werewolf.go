package main

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

type WerewolfNightData struct {
	WerewolfVoteCounts map[int64]int
	VotersByTarget     map[int64][]VoterChip
	PassVoters         []string
	CurrentVotePlayer  *Player
	WolfCubDoubleKill  bool
	CurrentVotePlayer2 *Player
	AllWolvesActed     bool
	AllWolvesActed2    bool
	WolfEndVoted       bool
	WolfEndVoted2      bool
	WolfTargetCards    []PlayerCardData
	WolfTargetCards2   []PlayerCardData
}

func buildWerewolfNightData(db *sqlx.DB, game *Game, playerID int64, player Player, seerInvestigated map[int64]string, aliveTargets []Player) WerewolfNightData {
	if player.Team != "werewolf" {
		return WerewolfNightData{}
	}

	type voteCountRow struct {
		TargetPlayerID int64 `db:"target_player_id"`
		Count          int   `db:"count"`
	}
	var countRows []voteCountRow
	db.Select(&countRows, `
SELECT target_player_id, COUNT(*) as count FROM game_action
WHERE game_id=? AND round=? AND phase='night' AND action_type=? AND target_player_id IS NOT NULL
GROUP BY target_player_id`,
		game.ID, game.Round, ActionWerewolfSelectKill)
	werewolfVoteCounts := map[int64]int{}
	for _, r := range countRows {
		werewolfVoteCounts[r.TargetPlayerID] = r.Count
	}

	var actions []GameAction
	db.Select(&actions, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfSelectKill)

	votersByTarget := map[int64][]VoterChip{}
	var passVoters []string
	var currentVotePlayer *Player
	for _, action := range actions {
		var voterName string
		db.Get(&voterName, "SELECT name FROM player WHERE rowid = ?", action.ActorPlayerID)
		if action.TargetPlayerID != nil {
			votersByTarget[*action.TargetPlayerID] = append(votersByTarget[*action.TargetPlayerID], VoterChip{Name: voterName, PlayerUID: action.ActorPlayerID})
			if action.ActorPlayerID == playerID {
				currentVotePlayer = getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated)
			}
		} else {
			passVoters = append(passVoters, voterName)
		}
	}

	wolfCubDoubleKill := false
	var currentVotePlayer2 *Player
	if game.Round > 1 {
		var wolfCubDeathCount int
		db.Get(&wolfCubDeathCount, `
SELECT COUNT(*) FROM game_action ga
JOIN game_player gp ON ga.target_player_id = gp.player_id AND gp.game_id = ga.game_id
JOIN role r ON gp.role_id = r.rowid
WHERE ga.game_id = ? AND ga.round = ?
AND ga.action_type IN (?, ?, ?, ?)
AND r.name = 'Wolf Cub'`,
			game.ID, game.Round-1, ActionWerewolfSelectKill, ActionDayApplyKill, ActionHunterApplyKill, ActionWitchApplyKill)
		wolfCubDoubleKill = wolfCubDeathCount > 0

		if wolfCubDoubleKill {
			var vote2Action GameAction
			err := db.Get(&vote2Action, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionWerewolfSelectKill2)
			if err == nil && vote2Action.TargetPlayerID != nil {
				currentVotePlayer2 = getVisiblePlayer(db, game.ID, *vote2Action.TargetPlayerID, player, seerInvestigated)
			}
		}
	}

	var werewolfCount int
	db.Get(&werewolfCount, `
SELECT COUNT(*) FROM game_player gp JOIN role r ON gp.role_id = r.rowid
WHERE gp.game_id = ? AND gp.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

	var voted1 int
	db.Get(&voted1, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfSelectKill)
	allWolvesActed := voted1 >= werewolfCount

	var endVote1 int
	db.Get(&endVote1, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfApplyKill)
	wolfEndVoted := endVote1 > 0

	var allWolvesActed2, wolfEndVoted2 bool
	if wolfCubDoubleKill {
		var voted2 int
		db.Get(&voted2, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
			game.ID, game.Round, ActionWerewolfSelectKill2)
		allWolvesActed2 = voted2 >= werewolfCount
		var endVote2 int
		db.Get(&endVote2, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
			game.ID, game.Round, ActionWerewolfApplyKill2)
		wolfEndVoted2 = endVote2 > 0
	}

	return WerewolfNightData{
		WerewolfVoteCounts: werewolfVoteCounts,
		VotersByTarget:     votersByTarget,
		PassVoters:         passVoters,
		CurrentVotePlayer:  currentVotePlayer,
		WolfCubDoubleKill:  wolfCubDoubleKill,
		CurrentVotePlayer2: currentVotePlayer2,
		AllWolvesActed:     allWolvesActed,
		AllWolvesActed2:    allWolvesActed2,
		WolfEndVoted:       wolfEndVoted,
		WolfEndVoted2:      wolfEndVoted2,
	}
}

func handleWSWerewolfVote(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfVote: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_vote_only"))
		return
	}

	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfVote: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}

	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_werewolves_vote"))
		return
	}

	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_vote"))
		return
	}

	var endVoteCount int
	h.db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfApplyKill)
	if endVoteCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_vote_locked"))
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
		h.sendErrorToast(client.playerID, T(lang, "err_cannot_target_dead"))
		return
	}

	// voting the same target again retracts the vote
	var existingTarget sql.NullInt64
	h.db.Get(&existingTarget, `SELECT target_player_id FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfSelectKill)
	if existingTarget.Valid && existingTarget.Int64 == targetID {
		_, err = h.db.Exec(`DELETE FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
			game.ID, game.Round, client.playerID, ActionWerewolfSelectKill)
		if err != nil {
			h.logError("handleWSWerewolfVote: db.Exec delete vote", err)
			h.sendErrorToast(client.playerID, T(lang, "err_failed_clear_vote"))
			return
		}
		h.logf("Werewolf %d (%s) unselected vote for player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
		h.triggerBroadcast()
		return
	}

	description := fmt.Sprintf("Night %d: %s voted to kill %s", game.Round, voter.Name, target.Name)
	dKey, dArgs := "hist_wolf_vote", histArgs(game.Round, voter.Name, target.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
VALUES (?, ?, 'night', ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
DO UPDATE SET target_player_id = ?, description = ?, description_key = ?, description_args = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfSelectKill, targetID, VisibilityTeamWerewolf, description, dKey, dArgs, targetID, description, dKey, dArgs)
	if err != nil {
		h.logError("handleWSWerewolfVote: db.Exec insert vote", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_record_vote"))
		return
	}

	h.logf("Werewolf %d (%s) voted to kill player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSWerewolfVote", "Werewolf '%s' voted to kill '%s'", voter.Name, target.Name)
	LogDBState(h.db, "after werewolf vote")

	h.triggerBroadcast()
}

func handleWSWerewolfVote2(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfVote2: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_vote_only"))
		return
	}

	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfVote2: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}

	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_werewolves_vote"))
		return
	}

	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_vote"))
		return
	}

	if game.Round <= 1 {
		h.sendErrorToast(client.playerID, T(lang, "err_wolfcub_not_active"))
		return
	}
	var wolfCubDeathCount int
	h.db.Get(&wolfCubDeathCount, `
SELECT COUNT(*) FROM game_action ga
JOIN game_player gp ON ga.target_player_id = gp.player_id AND gp.game_id = ga.game_id
JOIN role r ON gp.role_id = r.rowid
WHERE ga.game_id = ? AND ga.round = ?
AND ga.action_type IN (?, ?, ?, ?)
AND r.name = 'Wolf Cub'`,
		game.ID, game.Round-1, ActionWerewolfSelectKill, ActionDayApplyKill, ActionHunterApplyKill, ActionWitchApplyKill)
	if wolfCubDeathCount == 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_wolfcub_not_active"))
		return
	}

	var endVote2Count int
	h.db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfApplyKill2)
	if endVote2Count > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_vote2_locked"))
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
		h.sendErrorToast(client.playerID, T(lang, "err_cannot_target_dead"))
		return
	}

	description2 := fmt.Sprintf("Night %d: %s voted to kill %s (Wolf Cub revenge)", game.Round, voter.Name, target.Name)
	dKey2, dArgs2 := "hist_wolf_vote_cub", histArgs(game.Round, voter.Name, target.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
VALUES (?, ?, 'night', ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
DO UPDATE SET target_player_id = ?, description = ?, description_key = ?, description_args = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfSelectKill2, targetID, VisibilityTeamWerewolf, description2, dKey2, dArgs2, targetID, description2, dKey2, dArgs2)
	if err != nil {
		h.logError("handleWSWerewolfVote2: db.Exec insert vote2", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_record_vote2"))
		return
	}

	h.logf("Werewolf %d (%s) voted second kill: player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSWerewolfVote2", "Werewolf '%s' second kill vote: '%s'", voter.Name, target.Name)
	LogDBState(h.db, "after werewolf vote2")

	h.triggerBroadcast()
}

func handleWSWerewolfPass(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfPass: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_vote_only"))
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfPass: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_werewolves_vote"))
		return
	}
	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_vote"))
		return
	}
	var endVoteCount int
	h.db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfApplyKill)
	if endVoteCount > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_vote_locked"))
		return
	}
	passDesc := fmt.Sprintf("Night %d: %s passed", game.Round, voter.Name)
	passKey, passArgs := "hist_wolf_pass", histArgs(game.Round, voter.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
VALUES (?, ?, 'night', ?, ?, NULL, ?, ?, ?, ?)
ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
DO UPDATE SET target_player_id = NULL, description = ?, description_key = ?, description_args = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfSelectKill, VisibilityTeamWerewolf, passDesc, passKey, passArgs, passDesc, passKey, passArgs)
	if err != nil {
		h.logError("handleWSWerewolfPass: db.Exec", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_record_pass"))
		return
	}
	h.logf("Werewolf %d (%s) passed the kill vote", client.playerID, voter.Name)
	h.triggerBroadcast()
}

func handleWSWerewolfPass2(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfPass2: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_vote_only"))
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfPass2: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_werewolves_vote"))
		return
	}
	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, T(lang, "err_dead_cannot_vote"))
		return
	}
	var endVote2Count int
	h.db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfApplyKill2)
	if endVote2Count > 0 {
		h.sendErrorToast(client.playerID, T(lang, "err_vote2_locked"))
		return
	}
	passDesc := fmt.Sprintf("Night %d: %s passed (second kill)", game.Round, voter.Name)
	passKey2, passArgs2 := "hist_wolf_pass_2", histArgs(game.Round, voter.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description, description_key, description_args)
VALUES (?, ?, 'night', ?, ?, NULL, ?, ?, ?, ?)
ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
DO UPDATE SET target_player_id = NULL, description = ?, description_key = ?, description_args = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfSelectKill2, VisibilityTeamWerewolf, passDesc, passKey2, passArgs2, passDesc, passKey2, passArgs2)
	if err != nil {
		h.logError("handleWSWerewolfPass2: db.Exec", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_record_pass"))
		return
	}
	h.logf("Werewolf %d (%s) passed the second kill vote", client.playerID, voter.Name)
	h.triggerBroadcast()
}

func handleWSWerewolfEndVote(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfEndVote: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_vote_only"))
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfEndVote: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_werewolves_end_vote"))
		return
	}

	var werewolves []Player
	h.db.Select(&werewolves, `
SELECT g.rowid as id, g.player_id as player_id, p.name as name
FROM game_player g
JOIN player p ON g.player_id = p.rowid
JOIN role r ON g.role_id = r.rowid
WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

	var totalActed int
	h.db.Get(&totalActed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfSelectKill)

	if totalActed < len(werewolves) {
		h.sendErrorToast(client.playerID, T(lang, "err_werewolves_not_done", totalActed, len(werewolves)))
		return
	}

	_, err = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, NULL, ?, '')`,
		game.ID, game.Round, client.playerID, ActionWerewolfApplyKill, VisibilityTeamWerewolf)
	if err != nil {
		h.logError("handleWSWerewolfEndVote: record end vote", err)
	}

	h.logf("Werewolf %d (%s) ended the kill vote", client.playerID, voter.Name)
	if players, err := getPlayersByGameId(h.db, game.ID); err == nil {
		for _, p := range players {
			h.sendToPlayer(p.PlayerID, []byte(renderToast(h.templates, h.logf, "info", T(h.getPlayerLang(p.PlayerID), "toast_wolves_chosen"))))
		}
	}
	h.maybeSpeakStory(game.ID, T(h.storytellerLang, "tts_wolves_chosen"))
	h.resolveWerewolfVotes(game)
}

func handleWSWerewolfEndVote2(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfEndVote2: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, T(lang, "err_night_vote_only"))
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfEndVote2: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_not_in_game"))
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, T(lang, "err_only_werewolves_end_vote"))
		return
	}

	var werewolves []Player
	h.db.Select(&werewolves, `
SELECT g.rowid as id, g.player_id as player_id, p.name as name
FROM game_player g
JOIN player p ON g.player_id = p.rowid
JOIN role r ON g.role_id = r.rowid
WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

	var totalActed2 int
	h.db.Get(&totalActed2, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfSelectKill2)

	if totalActed2 < len(werewolves) {
		h.sendErrorToast(client.playerID, T(lang, "err_werewolves_not_done_second", totalActed2, len(werewolves)))
		return
	}

	_, err = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, NULL, ?, '')`,
		game.ID, game.Round, client.playerID, ActionWerewolfApplyKill2, VisibilityTeamWerewolf)
	if err != nil {
		h.logError("handleWSWerewolfEndVote2: record end vote 2", err)
	}

	h.logf("Werewolf %d (%s) ended the second kill vote", client.playerID, voter.Name)
	h.resolveWerewolfVotes(game)
}
