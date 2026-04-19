package main

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

// WerewolfVote represents a werewolf's vote during the night.
type WerewolfVote struct {
	VoterName  string
	TargetName string
}

// WerewolfNightData holds night-phase display data for werewolf-team players.
type WerewolfNightData struct {
	Votes              []WerewolfVote
	WerewolfVoteCounts map[int64]int
	CurrentVotePlayer  *Player // this wolf's current vote target (nil = none/pass)
	WolfCubDoubleKill  bool
	CurrentVotePlayer2 *Player // this wolf's second-kill vote (nil = none)
	AllWolvesActed     bool
	AllWolvesActed2    bool
	WolfEndVoted       bool
	WolfEndVoted2      bool
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
		game.ID, game.Round, ActionWerewolfKill)
	werewolfVoteCounts := map[int64]int{}
	for _, r := range countRows {
		werewolfVoteCounts[r.TargetPlayerID] = r.Count
	}

	var actions []GameAction
	db.Select(&actions, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfKill)

	var votes []WerewolfVote
	var currentVotePlayer *Player
	for _, action := range actions {
		var voterName, targetName string
		db.Get(&voterName, "SELECT name FROM player WHERE rowid = ?", action.ActorPlayerID)
		if action.TargetPlayerID != nil {
			db.Get(&targetName, "SELECT name FROM player WHERE rowid = ?", *action.TargetPlayerID)
			if action.ActorPlayerID == playerID {
				currentVotePlayer = getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated)
			}
		}
		votes = append(votes, WerewolfVote{VoterName: voterName, TargetName: targetName})
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
AND ga.action_type IN ('werewolf_kill', 'elimination', 'hunter_revenge', 'witch_kill')
AND r.name = 'Wolf Cub'`,
			game.ID, game.Round-1)
		wolfCubDoubleKill = wolfCubDeathCount > 0

		if wolfCubDoubleKill {
			var vote2Action GameAction
			err := db.Get(&vote2Action, `
SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
FROM game_action
WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionWerewolfKill2)
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
		game.ID, game.Round, ActionWerewolfKill)
	allWolvesActed := voted1 >= werewolfCount

	var endVote1 int
	db.Get(&endVote1, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote)
	wolfEndVoted := endVote1 > 0

	var allWolvesActed2, wolfEndVoted2 bool
	if wolfCubDoubleKill {
		var voted2 int
		db.Get(&voted2, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
			game.ID, game.Round, ActionWerewolfKill2)
		allWolvesActed2 = voted2 >= werewolfCount
		var endVote2 int
		db.Get(&endVote2, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
			game.ID, game.Round, ActionWerewolfEndVote2)
		wolfEndVoted2 = endVote2 > 0
	}

	return WerewolfNightData{
		Votes:              votes,
		WerewolfVoteCounts: werewolfVoteCounts,
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
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfVote: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}

	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfVote: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can vote at night")
		return
	}

	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot vote")
		return
	}

	var endVoteCount int
	h.db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote)
	if endVoteCount > 0 {
		h.sendErrorToast(client.playerID, "The vote has already been locked in")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, "Invalid target")
		return
	}

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil {
		h.sendErrorToast(client.playerID, "Target not found")
		return
	}

	if !target.IsAlive {
		h.sendErrorToast(client.playerID, "Cannot target a dead player")
		return
	}

	// Toggle: if already voted for this target, unselect (delete the vote)
	var existingTarget sql.NullInt64
	h.db.Get(&existingTarget, `SELECT target_player_id FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill)
	if existingTarget.Valid && existingTarget.Int64 == targetID {
		_, err = h.db.Exec(`DELETE FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
			game.ID, game.Round, client.playerID, ActionWerewolfKill)
		if err != nil {
			h.logError("handleWSWerewolfVote: db.Exec delete vote", err)
			h.sendErrorToast(client.playerID, "Failed to clear vote")
			return
		}
		h.logf("Werewolf %d (%s) unselected vote for player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
		h.triggerBroadcast()
		return
	}

	description := fmt.Sprintf("Night %d: %s voted to kill %s", game.Round, voter.Name, target.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
VALUES (?, ?, 'night', ?, ?, ?, ?, ?)
ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
DO UPDATE SET target_player_id = ?, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill, targetID, VisibilityTeamWerewolf, description, targetID, description)
	if err != nil {
		h.logError("handleWSWerewolfVote: db.Exec insert vote", err)
		h.sendErrorToast(client.playerID, "Failed to record vote")
		return
	}

	h.logf("Werewolf %d (%s) voted to kill player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSWerewolfVote", "Werewolf '%s' voted to kill '%s'", voter.Name, target.Name)
	LogDBState(h.db, "after werewolf vote")

	h.triggerBroadcast()
}

func handleWSWerewolfVote2(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfVote2: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}

	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfVote2: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can vote at night")
		return
	}

	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot vote")
		return
	}

	if game.Round <= 1 {
		h.sendErrorToast(client.playerID, "Wolf Cub double kill not active")
		return
	}
	var wolfCubDeathCount int
	h.db.Get(&wolfCubDeathCount, `
SELECT COUNT(*) FROM game_action ga
JOIN game_player gp ON ga.target_player_id = gp.player_id AND gp.game_id = ga.game_id
JOIN role r ON gp.role_id = r.rowid
WHERE ga.game_id = ? AND ga.round = ?
AND ga.action_type IN ('werewolf_kill', 'elimination', 'hunter_revenge', 'witch_kill')
AND r.name = 'Wolf Cub'`,
		game.ID, game.Round-1)
	if wolfCubDeathCount == 0 {
		h.sendErrorToast(client.playerID, "Wolf Cub double kill not active")
		return
	}

	var endVote2Count int
	h.db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote2)
	if endVote2Count > 0 {
		h.sendErrorToast(client.playerID, "The second vote has already been locked in")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		h.sendErrorToast(client.playerID, "Invalid target")
		return
	}

	target, err := getPlayerInGame(h.db, game.ID, targetID)
	if err != nil {
		h.sendErrorToast(client.playerID, "Target not found")
		return
	}

	if !target.IsAlive {
		h.sendErrorToast(client.playerID, "Cannot target a dead player")
		return
	}

	description2 := fmt.Sprintf("Night %d: %s voted to kill %s (Wolf Cub revenge)", game.Round, voter.Name, target.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
VALUES (?, ?, 'night', ?, ?, ?, ?, ?)
ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
DO UPDATE SET target_player_id = ?, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill2, targetID, VisibilityTeamWerewolf, description2, targetID, description2)
	if err != nil {
		h.logError("handleWSWerewolfVote2: db.Exec insert vote2", err)
		h.sendErrorToast(client.playerID, "Failed to record second vote")
		return
	}

	h.logf("Werewolf %d (%s) voted second kill: player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSWerewolfVote2", "Werewolf '%s' second kill vote: '%s'", voter.Name, target.Name)
	LogDBState(h.db, "after werewolf vote2")

	h.triggerBroadcast()
}

func handleWSWerewolfPass(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfPass: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfPass: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can vote at night")
		return
	}
	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot vote")
		return
	}
	var endVoteCount int
	h.db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote)
	if endVoteCount > 0 {
		h.sendErrorToast(client.playerID, "The vote has already been locked in")
		return
	}
	passDesc := fmt.Sprintf("Night %d: %s passed", game.Round, voter.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
VALUES (?, ?, 'night', ?, ?, NULL, ?, ?)
ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
DO UPDATE SET target_player_id = NULL, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill, VisibilityTeamWerewolf, passDesc, passDesc)
	if err != nil {
		h.logError("handleWSWerewolfPass: db.Exec", err)
		h.sendErrorToast(client.playerID, "Failed to record pass")
		return
	}
	h.logf("Werewolf %d (%s) passed the kill vote", client.playerID, voter.Name)
	h.triggerBroadcast()
}

func handleWSWerewolfPass2(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfPass2: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfPass2: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can vote at night")
		return
	}
	if !voter.IsAlive {
		h.sendErrorToast(client.playerID, "Dead players cannot vote")
		return
	}
	var endVote2Count int
	h.db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote2)
	if endVote2Count > 0 {
		h.sendErrorToast(client.playerID, "The second vote has already been locked in")
		return
	}
	passDesc := fmt.Sprintf("Night %d: %s passed (second kill)", game.Round, voter.Name)
	_, err = h.db.Exec(`
INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
VALUES (?, ?, 'night', ?, ?, NULL, ?, ?)
ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
DO UPDATE SET target_player_id = NULL, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill2, VisibilityTeamWerewolf, passDesc, passDesc)
	if err != nil {
		h.logError("handleWSWerewolfPass2: db.Exec", err)
		h.sendErrorToast(client.playerID, "Failed to record pass")
		return
	}
	h.logf("Werewolf %d (%s) passed the second kill vote", client.playerID, voter.Name)
	h.triggerBroadcast()
}

func handleWSWerewolfEndVote(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfEndVote: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfEndVote: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can end the vote")
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
		game.ID, game.Round, ActionWerewolfKill)

	if totalActed < len(werewolves) {
		h.sendErrorToast(client.playerID, fmt.Sprintf("Not all werewolves have voted yet (%d/%d)", totalActed, len(werewolves)))
		return
	}

	_, err = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, NULL, ?, '')`,
		game.ID, game.Round, client.playerID, ActionWerewolfEndVote, VisibilityTeamWerewolf)
	if err != nil {
		h.logError("handleWSWerewolfEndVote: record end vote", err)
	}

	h.logf("Werewolf %d (%s) ended the kill vote", client.playerID, voter.Name)
	html := renderToast(h.templates, h.logf, "info", "🐺 The werewolves have made their choice...")
	if html != "" {
		h.broadcast <- []byte(html)
	}
	h.maybeSpeakStory(game.ID, "The werewolves have made their choice. Silence falls over the village.")
	h.resolveWerewolfVotes(game)
}

func handleWSWerewolfEndVote2(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSWerewolfEndVote2: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		h.sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(h.db, game.ID, client.playerID)
	if err != nil {
		h.logError("handleWSWerewolfEndVote2: getPlayerInGame", err)
		h.sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if voter.Team != "werewolf" {
		h.sendErrorToast(client.playerID, "Only werewolves can end the vote")
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
		game.ID, game.Round, ActionWerewolfKill2)

	if totalActed2 < len(werewolves) {
		h.sendErrorToast(client.playerID, fmt.Sprintf("Not all werewolves have voted for the second kill yet (%d/%d)", totalActed2, len(werewolves)))
		return
	}

	_, err = h.db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, NULL, ?, '')`,
		game.ID, game.Round, client.playerID, ActionWerewolfEndVote2, VisibilityTeamWerewolf)
	if err != nil {
		h.logError("handleWSWerewolfEndVote2: record end vote 2", err)
	}

	h.logf("Werewolf %d (%s) ended the second kill vote", client.playerID, voter.Name)
	h.resolveWerewolfVotes(game)
}
