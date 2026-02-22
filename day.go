package main

import (
	"fmt"
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
	IsLover             bool     // Is this player one of the two lovers?
	LoverName           string   // Name of their partner
	AllActed            bool     // All alive players have voted or passed this round
	HasVoted            bool     // This player has a day_vote record (including pass)
}

// applyHeartbreaks checks if any of the given killed players have a living lover.
// Kills any living lovers and records public heartbreak actions, then recurses for chains
// (multiple Cupids can create chained heartbreaks across multiple lover pairs).
// Returns all player IDs killed by heartbreak in this chain.
func applyHeartbreaks(game *Game, phase string, killedIDs []int64) []int64 {
	var allHeartbroken []int64
	toProcess := killedIDs
	for len(toProcess) > 0 {
		var nextRound []int64
		for _, killed := range toProcess {
			partnerID := getLoverPartner(game.ID, killed)
			if partnerID == 0 {
				continue
			}
			var isAlive bool
			db.Get(&isAlive, `SELECT is_alive FROM game_player WHERE game_id = ? AND player_id = ?`, game.ID, partnerID)
			if !isAlive {
				continue
			}
			_, err := db.Exec("UPDATE game_player SET is_alive = 0 WHERE game_id = ? AND player_id = ?", game.ID, partnerID)
			if err != nil {
				logError("applyHeartbreaks: kill partner", err)
				continue
			}
			// Record public heartbreak action: actor=trigger person, target=heartbreak victim
			var killedName, partnerName string
			db.Get(&killedName, "SELECT name FROM player WHERE rowid = ?", killed)
			db.Get(&partnerName, "SELECT name FROM player WHERE rowid = ?", partnerID)
			phaseLabel := "Night"
			if phase == "day" {
				phaseLabel = "Day"
			}
			heartbreakDesc := fmt.Sprintf("%s %d: %s died of heartbreak after their lover %s was killed", phaseLabel, game.Round, partnerName, killedName)
			_, _ = db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				game.ID, game.Round, phase, killed, ActionLoverHeartbreak, partnerID, VisibilityPublic, heartbreakDesc)
			log.Printf("Heartbreak: '%s' died after their lover '%s' was killed", partnerName, killedName)
			DebugLog("applyHeartbreaks", "'%s' died from heartbreak (lover '%s' was killed)", partnerName, killedName)
			nextRound = append(nextRound, partnerID)
			allHeartbroken = append(allHeartbroken, partnerID)
		}
		toProcess = nextRound
	}
	return allHeartbroken
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
	dayVoteDesc := fmt.Sprintf("Day %d: %s voted to eliminate %s", game.Round, voter.Name, target.Name)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'day', ?, ?, ?, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = ?, description = ?`,
		game.ID, game.Round, client.playerID, ActionDayVote, targetID, VisibilityPublic, dayVoteDesc, targetID, dayVoteDesc)
	if err != nil {
		logError("handleWSDayVote: db.Exec insert vote", err)
		sendErrorToast(client.playerID, "Failed to record vote")
		return
	}

	log.Printf("Player %d (%s) voted to eliminate player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSDayVote", "Player '%s' voted to eliminate '%s'", voter.Name, target.Name)
	LogDBState("after day vote")

	broadcastGameUpdate()
}

func handleWSDayPass(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSDayPass: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "day" {
		sendErrorToast(client.playerID, "Voting only allowed during day phase")
		return
	}

	voter, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSDayPass: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if !voter.IsAlive {
		sendErrorToast(client.playerID, "Dead players cannot vote")
		return
	}

	// Record pass as a day_vote with NULL target
	passDesc := fmt.Sprintf("Day %d: %s passed", game.Round, voter.Name)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'day', ?, ?, NULL, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = NULL, description = ?`,
		game.ID, game.Round, client.playerID, ActionDayVote, VisibilityPublic, passDesc, passDesc)
	if err != nil {
		logError("handleWSDayPass: db.Exec", err)
		sendErrorToast(client.playerID, "Failed to record pass")
		return
	}

	log.Printf("Player %d (%s) passed the day vote", client.playerID, voter.Name)
	broadcastGameUpdate()
}

func handleWSDayEndVote(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSDayEndVote: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "day" {
		sendErrorToast(client.playerID, "Voting only allowed during day phase")
		return
	}

	voter, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSDayEndVote: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if !voter.IsAlive {
		sendErrorToast(client.playerID, "Dead players cannot end the vote")
		return
	}

	// Check all alive players have acted
	var alivePlayers []Player
	db.Select(&alivePlayers, `
		SELECT g.rowid as id, g.player_id as player_id, p.name as name
		FROM game_player g
		JOIN player p ON g.player_id = p.rowid
		WHERE g.game_id = ? AND g.is_alive = 1`, game.ID)

	var totalActed int
	db.Get(&totalActed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'day' AND action_type = ?`,
		game.ID, game.Round, ActionDayVote)

	if totalActed < len(alivePlayers) {
		sendErrorToast(client.playerID, fmt.Sprintf("Not all players have voted yet (%d/%d)", totalActed, len(alivePlayers)))
		return
	}

	log.Printf("Day End Vote triggered by player %d (%s)", client.playerID, voter.Name)
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
	voteCounts, totalVotes, err := getVoteCounts(game.ID, game.Round, "day", ActionDayVote)
	if err != nil {
		logError("resolveDayVotes: getVoteCounts", err)
		return
	}

	log.Printf("Day vote check: %d alive players, %d votes", len(alivePlayers), totalVotes)

	// Check if majority passed — if so, skip elimination
	realVoteCount := 0
	for _, c := range voteCounts {
		realVoteCount += c
	}
	passCount := totalVotes - realVoteCount
	if passCount > len(alivePlayers)/2 {
		log.Printf("Majority passed (%d/%d) — no elimination this day", passCount, len(alivePlayers))
		transitionToNight(game)
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

	var eliminatedName, eliminatedRole string
	db.Get(&eliminatedName, "SELECT name FROM player WHERE rowid = ?", eliminatedID)
	db.Get(&eliminatedRole, `SELECT r.name FROM game_player g JOIN role r ON g.role_id = r.rowid WHERE g.game_id = ? AND g.player_id = ?`, game.ID, eliminatedID)

	// Record the elimination action
	eliminationDesc := fmt.Sprintf("Day %d: %s (%s) was eliminated by the village", game.Round, eliminatedName, eliminatedRole)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'day', ?, ?, ?, ?, ?)`,
		game.ID, game.Round, eliminatedID, ActionElimination, eliminatedID, VisibilityPublic, eliminationDesc)
	if err != nil {
		logError("resolveDayVotes: record elimination", err)
	}
	log.Printf("Village eliminated %s (player ID %d)", eliminatedName, eliminatedID)
	DebugLog("resolveDayVotes", "Village eliminated '%s'", eliminatedName)

	// Apply heartbreak from day elimination — chains across multiple lover pairs
	heartbroken := applyHeartbreaks(game, "day", []int64{eliminatedID})

	// Check if any of the dead (eliminated + heartbroken) are Hunters needing revenge
	for _, deadID := range append([]int64{eliminatedID}, heartbroken...) {
		var deadRole string
		db.Get(&deadRole, `SELECT r.name FROM game_player g JOIN role r ON g.role_id = r.rowid WHERE g.game_id = ? AND g.player_id = ?`, game.ID, deadID)
		if deadRole == "Hunter" {
			var deadName string
			db.Get(&deadName, "SELECT name FROM player WHERE rowid = ?", deadID)
			log.Printf("Hunter '%s' was eliminated — waiting for revenge shot before transitioning", deadName)
			LogDBState("after hunter elimination - waiting for revenge")
			broadcastGameUpdate()
			return
		}
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
		game.ID, game.Round, client.playerID, ActionHunterRevenge)
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
	hunterRevengeDesc := fmt.Sprintf("Day %d: Hunter %s shot %s", game.Round, hunter.Name, target.Name)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'day', ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionHunterRevenge, targetID, VisibilityPublic, hunterRevengeDesc)
	if err != nil {
		logError("handleWSHunterRevenge: record action", err)
	}

	log.Printf("Hunter '%s' took revenge on '%s'", hunter.Name, target.Name)
	DebugLog("handleWSHunterRevenge", "Hunter '%s' shot '%s'", hunter.Name, target.Name)
	LogDBState("after hunter revenge")

	// Apply heartbreak from Hunter's shot — chains across multiple lover pairs
	heartbroken := applyHeartbreaks(game, "day", []int64{targetID})

	// Check if the target (or any heartbreak victim) is also a Hunter
	for _, deadID := range append([]int64{targetID}, heartbroken...) {
		var deadRole string
		db.Get(&deadRole, `SELECT r.name FROM game_player g JOIN role r ON g.role_id = r.rowid WHERE g.game_id = ? AND g.player_id = ?`, game.ID, deadID)
		if deadRole == "Hunter" {
			var deadName string
			db.Get(&deadName, "SELECT name FROM player WHERE rowid = ?", deadID)
			log.Printf("Hunter '%s' was killed — entering chained revenge", deadName)
			broadcastGameUpdate()
			return
		}
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
		game.ID, game.Round, ActionElimination)

	if dayEliminationCount > 0 {
		// Chain started from day elimination — transition to night
		transitionToNight(game)
	} else {
		// Chain started from night kill — stay in day for voting
		broadcastGameUpdate()
	}
}
