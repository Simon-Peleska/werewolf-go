package main

import (
	"log"
	"strconv"
)

// DayVote represents a player's vote during the day
type DayVote struct {
	VoterName  string
	TargetName string
}

// NightVictim represents a player killed during the night
type NightVictim struct {
	Name string `db:"name"`
	Role string `db:"role"`
}

// DayData holds all data needed to render the day phase
type DayData struct {
	Players             []Player
	AliveTargets        []Player
	NightNumber         int
	NightVictims        []NightVictim // All players killed last night
	Votes               []DayVote
	CurrentVote         int64 // 0 means no vote
	IsAlive             bool
	HunterRevengeNeeded bool     // Night victim was a Hunter who hasn't shot yet
	HunterRevengeDone   bool     // Hunter has taken their shot
	HunterName          string   // Name of the dead Hunter
	HunterVictim        string   // Who the Hunter shot (after revenge)
	HunterVictimRole    string   // Role of Hunter's target
	IsTheHunter         bool     // Is this player the dead Hunter needing to shoot?
	HunterTargets       []Player // Alive targets for the Hunter to pick from
}

func handleWSDayVote(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSDayVote: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "day" {
		sendErrorToast(client.playerID, "Voting only allowed during day phase")
		return
	}

	// Check that the player is alive
	voter, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSDayVote: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if !voter.IsAlive {
		sendErrorToast(client.playerID, "Dead players cannot vote")
		return
	}

	// Parse target player ID
	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		sendErrorToast(client.playerID, "Invalid target")
		return
	}

	// Check that the target is valid (alive)
	target, err := getPlayerInGame(game.ID, targetID)
	if err != nil {
		sendErrorToast(client.playerID, "Target not found")
		return
	}

	if !target.IsAlive {
		sendErrorToast(client.playerID, "Cannot vote for a dead player")
		return
	}

	// Record or update the vote
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility)
		VALUES (?, ?, 'day', ?, ?, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = ?`,
		game.ID, game.NightNumber, client.playerID, ActionDayVote, targetID, VisibilityPublic, targetID)
	if err != nil {
		logError("handleWSDayVote: db.Exec insert vote", err)
		sendErrorToast(client.playerID, "Failed to record vote")
		return
	}

	log.Printf("Player %d (%s) voted to eliminate player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSDayVote", "Player '%s' voted to eliminate '%s'", voter.Name, target.Name)
	LogDBState("after day vote")

	// Check if all alive players have voted and resolve if so
	resolveDayVotes(game)
}

// resolveDayVotes checks if all alive players have voted and resolves the elimination
func resolveDayVotes(game *Game) {
	// Get all living players
	var alivePlayers []Player
	err := db.Select(&alivePlayers, `
		SELECT g.rowid as id, g.player_id as player_id, p.name as name
		FROM game_player g
		JOIN player p ON g.player_id = p.rowid
		WHERE g.game_id = ? AND g.is_alive = 1`, game.ID)
	if err != nil {
		logError("resolveDayVotes: get alive players", err)
		return
	}

	// Get all day votes for this round
	voteCounts, totalVotes, err := getVoteCounts(game.ID, game.NightNumber, "day", ActionDayVote)
	if err != nil {
		logError("resolveDayVotes: getVoteCounts", err)
		return
	}

	log.Printf("Day vote check: %d alive players, %d votes", len(alivePlayers), totalVotes)

	// Check if all alive players have voted
	if totalVotes < len(alivePlayers) {
		log.Printf("Not all players have voted yet (%d/%d)", totalVotes, len(alivePlayers))
		broadcastGameUpdate()
		return
	}

	// Find the target with the most votes
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

	// Check for majority (more than half of alive players)
	majority := len(alivePlayers)/2 + 1
	if maxVotes < majority || isTie {
		log.Printf("No majority reached (need %d, max is %d, tie: %v) - no elimination", majority, maxVotes, isTie)
		// No elimination, transition to night
		transitionToNight(game)
		return
	}

	// Eliminate the player
	_, err = db.Exec("UPDATE game_player SET is_alive = 0 WHERE game_id = ? AND player_id = ?", game.ID, eliminatedID)
	if err != nil {
		logError("resolveDayVotes: eliminate player", err)
		return
	}

	// Record the elimination action
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility)
		VALUES (?, ?, 'day', ?, ?, ?, ?)`,
		game.ID, game.NightNumber, eliminatedID, ActionElimination, eliminatedID, VisibilityPublic)
	if err != nil {
		logError("resolveDayVotes: record elimination", err)
	}

	var eliminatedName string
	db.Get(&eliminatedName, "SELECT name FROM player WHERE rowid = ?", eliminatedID)
	log.Printf("Village eliminated %s (player ID %d)", eliminatedName, eliminatedID)
	DebugLog("resolveDayVotes", "Village eliminated '%s'", eliminatedName)

	// Check if eliminated player is a Hunter — they get a revenge shot before game continues
	var eliminatedRole string
	db.Get(&eliminatedRole, `
		SELECT r.name FROM game_player g JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.player_id = ?`, game.ID, eliminatedID)
	if eliminatedRole == "Hunter" {
		log.Printf("Hunter '%s' was eliminated — waiting for revenge shot before transitioning", eliminatedName)
		LogDBState("after hunter elimination - waiting for revenge")
		broadcastGameUpdate()
		return
	}

	// Check win conditions
	if checkWinConditions(game) {
		return // Game ended
	}

	// Transition to night
	transitionToNight(game)
}

func handleWSHunterRevenge(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSHunterRevenge: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "day" {
		sendErrorToast(client.playerID, "Hunter revenge not active")
		return
	}

	hunter, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSHunterRevenge: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if hunter.RoleName != "Hunter" {
		sendErrorToast(client.playerID, "Only the Hunter can take a revenge shot")
		return
	}

	if hunter.IsAlive {
		sendErrorToast(client.playerID, "Hunter revenge is only available when eliminated")
		return
	}

	// Check if this Hunter already took their revenge shot
	var revengeCount int
	db.Get(&revengeCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.NightNumber, client.playerID, ActionHunterRevenge)
	if revengeCount > 0 {
		sendErrorToast(client.playerID, "You have already taken your revenge shot")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		sendErrorToast(client.playerID, "Invalid target")
		return
	}

	target, err := getPlayerInGame(game.ID, targetID)
	if err != nil {
		sendErrorToast(client.playerID, "Target not found")
		return
	}

	if !target.IsAlive {
		sendErrorToast(client.playerID, "Cannot shoot a dead player")
		return
	}

	// Kill the target
	_, err = db.Exec("UPDATE game_player SET is_alive = 0 WHERE game_id = ? AND player_id = ?", game.ID, targetID)
	if err != nil {
		logError("handleWSHunterRevenge: kill target", err)
		sendErrorToast(client.playerID, "Failed to kill target")
		return
	}

	// Record the revenge action (public visibility)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility)
		VALUES (?, ?, 'day', ?, ?, ?, ?)`,
		game.ID, game.NightNumber, client.playerID, ActionHunterRevenge, targetID, VisibilityPublic)
	if err != nil {
		logError("handleWSHunterRevenge: record action", err)
	}

	log.Printf("Hunter '%s' took revenge on '%s'", hunter.Name, target.Name)
	DebugLog("handleWSHunterRevenge", "Hunter '%s' shot '%s'", hunter.Name, target.Name)
	LogDBState("after hunter revenge")

	// Check if the target is also a Hunter — they get to take their shot too
	if target.RoleName == "Hunter" {
		log.Printf("Hunter '%s' was killed by another Hunter's revenge — entering chained revenge", target.Name)
		broadcastGameUpdate()
		return
	}

	// Check win conditions
	if checkWinConditions(game) {
		return // Game ended
	}

	// Check if a day elimination happened this round (the chain started from a day vote)
	var dayEliminationCount int
	db.Get(&dayEliminationCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'day' AND action_type = ?`,
		game.ID, game.NightNumber, ActionElimination)

	if dayEliminationCount > 0 {
		// Chain started from day elimination — transition to night
		transitionToNight(game)
	} else {
		// Chain started from night kill — stay in day for voting
		broadcastGameUpdate()
	}
}
