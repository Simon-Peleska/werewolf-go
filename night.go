package main

import (
	"log"
	"strconv"
)

// WerewolfVote represents a werewolf's vote during the night
type WerewolfVote struct {
	VoterName  string
	TargetName string
}

// SeerResult represents a seer's investigation result
type SeerResult struct {
	Round      int
	TargetName string
	IsWerewolf bool
}

// NightData holds all data needed to render the night phase
type NightData struct {
	Players           []Player
	AliveTargets      []Player
	IsWerewolf        bool
	Werewolves        []Player
	Votes             []WerewolfVote
	CurrentVote       int64 // 0 means no vote
	NightNumber       int
	IsSeer            bool
	HasInvestigated   bool
	SeerResults       []SeerResult
	IsDoctor          bool
	HasProtected      bool
	DoctorProtecting  string // Name of player being protected this night
	IsGuard           bool
	GuardHasProtected bool
	GuardProtecting   string   // Name of player being protected this night
	GuardTargets      []Player // Alive targets excluding self and last night's target
}

func handleWSWerewolfVote(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSWerewolfVote: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}

	// Check that the player is a werewolf
	voter, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSWerewolfVote: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if voter.Team != "werewolf" {
		sendErrorToast(client.playerID, "Only werewolves can vote at night")
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
		sendErrorToast(client.playerID, "Cannot target a dead player")
		return
	}

	// Record or update the vote
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility)
		VALUES (?, ?, 'night', ?, ?, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = ?`,
		game.ID, game.NightNumber, client.playerID, ActionWerewolfKill, targetID, VisibilityTeamWerewolf, targetID)
	if err != nil {
		logError("handleWSWerewolfVote: db.Exec insert vote", err)
		sendErrorToast(client.playerID, "Failed to record vote")
		return
	}

	log.Printf("Werewolf %d (%s) voted to kill player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSWerewolfVote", "Werewolf '%s' voted to kill '%s'", voter.Name, target.Name)
	LogDBState("after werewolf vote")

	// Check if all werewolves have voted and resolve if so
	resolveWerewolfVotes(game)
}

func handleWSSeerInvestigate(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSSeerInvestigate: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		sendErrorToast(client.playerID, "Can only investigate during night phase")
		return
	}

	investigator, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSSeerInvestigate: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if investigator.RoleName != "Seer" {
		sendErrorToast(client.playerID, "Only the Seer can investigate")
		return
	}

	if !investigator.IsAlive {
		sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}

	// Check if already investigated this night
	var existingCount int
	db.Get(&existingCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.NightNumber, client.playerID, ActionSeerInvestigate)
	if existingCount > 0 {
		sendErrorToast(client.playerID, "You have already investigated this night")
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
		sendErrorToast(client.playerID, "Cannot investigate a dead player")
		return
	}

	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility)
		VALUES (?, ?, 'night', ?, ?, ?, ?)`,
		game.ID, game.NightNumber, client.playerID, ActionSeerInvestigate, targetID, VisibilityActor)
	if err != nil {
		logError("handleWSSeerInvestigate: db.Exec insert investigation", err)
		sendErrorToast(client.playerID, "Failed to record investigation")
		return
	}

	log.Printf("Seer '%s' investigated '%s' (team: %s)", investigator.Name, target.Name, target.Team)
	DebugLog("handleWSSeerInvestigate", "Seer '%s' investigated '%s' (team: %s)", investigator.Name, target.Name, target.Team)
	LogDBState("after seer investigation")

	resolveWerewolfVotes(game)
}

func handleWSDoctorProtect(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSDoctorProtect: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		sendErrorToast(client.playerID, "Can only protect during night phase")
		return
	}

	doctor, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSDoctorProtect: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if doctor.RoleName != "Doctor" {
		sendErrorToast(client.playerID, "Only the Doctor can protect players")
		return
	}

	if !doctor.IsAlive {
		sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}

	// One protection per night
	var existingCount int
	db.Get(&existingCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.NightNumber, client.playerID, ActionDoctorProtect)
	if existingCount > 0 {
		sendErrorToast(client.playerID, "You have already protected someone this night")
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
		sendErrorToast(client.playerID, "Cannot protect a dead player")
		return
	}

	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility)
		VALUES (?, ?, 'night', ?, ?, ?, ?)`,
		game.ID, game.NightNumber, client.playerID, ActionDoctorProtect, targetID, VisibilityActor)
	if err != nil {
		logError("handleWSDoctorProtect: db.Exec insert protection", err)
		sendErrorToast(client.playerID, "Failed to record protection")
		return
	}

	log.Printf("Doctor '%s' is protecting '%s'", doctor.Name, target.Name)
	DebugLog("handleWSDoctorProtect", "Doctor '%s' protecting '%s'", doctor.Name, target.Name)
	LogDBState("after doctor protect")

	resolveWerewolfVotes(game)
}

func handleWSGuardProtect(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSGuardProtect: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		sendErrorToast(client.playerID, "Can only protect during night phase")
		return
	}

	guard, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSGuardProtect: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if guard.RoleName != "Guard" {
		sendErrorToast(client.playerID, "Only the Guard can protect players")
		return
	}

	if !guard.IsAlive {
		sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}

	// One protection per night
	var existingCount int
	db.Get(&existingCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
		game.ID, game.NightNumber, client.playerID, ActionGuardProtect)
	if existingCount > 0 {
		sendErrorToast(client.playerID, "You have already protected someone this night")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		sendErrorToast(client.playerID, "Invalid target")
		return
	}

	// Guard cannot protect themselves
	if targetID == client.playerID {
		sendErrorToast(client.playerID, "Guard cannot protect themselves")
		return
	}

	target, err := getPlayerInGame(game.ID, targetID)
	if err != nil {
		sendErrorToast(client.playerID, "Target not found")
		return
	}

	if !target.IsAlive {
		sendErrorToast(client.playerID, "Cannot protect a dead player")
		return
	}

	// Guard cannot protect the same player as last night
	if game.NightNumber > 1 {
		var lastTargetID int64
		err := db.Get(&lastTargetID, `
			SELECT target_player_id FROM game_action
			WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
			game.ID, game.NightNumber-1, client.playerID, ActionGuardProtect)
		if err == nil && lastTargetID == targetID {
			sendErrorToast(client.playerID, "Cannot protect the same player two nights in a row")
			return
		}
	}

	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility)
		VALUES (?, ?, 'night', ?, ?, ?, ?)`,
		game.ID, game.NightNumber, client.playerID, ActionGuardProtect, targetID, VisibilityActor)
	if err != nil {
		logError("handleWSGuardProtect: db.Exec insert protection", err)
		sendErrorToast(client.playerID, "Failed to record protection")
		return
	}

	log.Printf("Guard '%s' is protecting '%s'", guard.Name, target.Name)
	DebugLog("handleWSGuardProtect", "Guard '%s' protecting '%s'", guard.Name, target.Name)
	LogDBState("after guard protect")

	resolveWerewolfVotes(game)
}

// resolveWerewolfVotes checks if all werewolves have voted and resolves the kill
func resolveWerewolfVotes(game *Game) {
	// Get all living werewolves
	var werewolves []Player
	err := db.Select(&werewolves, `
		SELECT g.rowid as id, g.player_id as player_id, p.name as name
		FROM game_player g
		JOIN player p ON g.player_id = p.rowid
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)
	if err != nil {
		logError("resolveWerewolfVotes: get werewolves", err)
		return
	}

	// Get all werewolf votes for this night
	var votes []GameAction
	err = db.Select(&votes, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.NightNumber, ActionWerewolfKill)
	if err != nil {
		logError("resolveWerewolfVotes: get votes", err)
		return
	}

	log.Printf("Werewolf vote check: %d werewolves, %d votes", len(werewolves), len(votes))

	// Check if all werewolves have voted
	if len(votes) < len(werewolves) {
		log.Printf("Not all werewolves have voted yet (%d/%d)", len(votes), len(werewolves))
		broadcastGameUpdate()
		return
	}

	// Count votes for each target
	voteCounts := make(map[int64]int)
	for _, v := range votes {
		if v.TargetPlayerID != nil {
			voteCounts[*v.TargetPlayerID]++
		}
	}

	// Find the target with the most votes
	var maxVotes int
	var victim int64
	for targetID, count := range voteCounts {
		if count > maxVotes {
			maxVotes = count
			victim = targetID
		}
	}

	// Check for majority (more than half of werewolves)
	majority := len(werewolves)/2 + 1
	if maxVotes < majority {
		log.Printf("No majority reached yet (need %d, max is %d)", majority, maxVotes)
		broadcastGameUpdate()
		return
	}

	// Check if all alive Seers have investigated before resolving the night
	var aliveSeerCount int
	db.Get(&aliveSeerCount, `
		SELECT COUNT(*) FROM game_player g
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.name = 'Seer'`, game.ID)

	var seerInvestigateCount int
	db.Get(&seerInvestigateCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.NightNumber, ActionSeerInvestigate)

	if seerInvestigateCount < aliveSeerCount {
		log.Printf("Waiting for seers to investigate (%d/%d)", seerInvestigateCount, aliveSeerCount)
		broadcastGameUpdate()
		return
	}

	// Check if all alive Doctors have protected before resolving the night
	var aliveDoctorCount int
	db.Get(&aliveDoctorCount, `
		SELECT COUNT(*) FROM game_player g
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.name = 'Doctor'`, game.ID)

	var doctorProtectCount int
	db.Get(&doctorProtectCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.NightNumber, ActionDoctorProtect)

	if doctorProtectCount < aliveDoctorCount {
		log.Printf("Waiting for doctors to protect (%d/%d)", doctorProtectCount, aliveDoctorCount)
		broadcastGameUpdate()
		return
	}

	// Check if all alive Guards have protected before resolving the night
	var aliveGuardCount int
	db.Get(&aliveGuardCount, `
		SELECT COUNT(*) FROM game_player g
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.name = 'Guard'`, game.ID)

	var guardProtectCount int
	db.Get(&guardProtectCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.NightNumber, ActionGuardProtect)

	if guardProtectCount < aliveGuardCount {
		log.Printf("Waiting for guards to protect (%d/%d)", guardProtectCount, aliveGuardCount)
		broadcastGameUpdate()
		return
	}

	// Check if the victim is protected by any Doctor
	var protectionCount int
	db.Get(&protectionCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ? AND target_player_id = ?`,
		game.ID, game.NightNumber, ActionDoctorProtect, victim)

	// Check if the victim is protected by any Guard
	var guardProtectionCount int
	db.Get(&guardProtectionCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ? AND target_player_id = ?`,
		game.ID, game.NightNumber, ActionGuardProtect, victim)

	if protectionCount > 0 || guardProtectionCount > 0 {
		var victimName string
		db.Get(&victimName, "SELECT name FROM player WHERE rowid = ?", victim)
		if protectionCount > 0 {
			log.Printf("Doctor saved %s (player ID %d) from werewolf attack", victimName, victim)
			DebugLog("resolveWerewolfVotes", "Doctor saved '%s', no kill this night", victimName)
		}
		if guardProtectionCount > 0 {
			log.Printf("Guard saved %s (player ID %d) from werewolf attack", victimName, victim)
			DebugLog("resolveWerewolfVotes", "Guard saved '%s', no kill this night", victimName)
		}

		_, err = db.Exec("UPDATE game SET status = 'day' WHERE rowid = ?", game.ID)
		if err != nil {
			logError("resolveWerewolfVotes: transition to day (no kill)", err)
			return
		}
		log.Printf("Night %d ended (protection save), transitioning to day phase", game.NightNumber)
		LogDBState("after protection save")
		broadcastGameUpdate()
		return
	}

	// Kill the victim
	_, err = db.Exec("UPDATE game_player SET is_alive = 0 WHERE game_id = ? AND player_id = ?", game.ID, victim)
	if err != nil {
		logError("resolveWerewolfVotes: kill victim", err)
		return
	}

	var victimName string
	db.Get(&victimName, "SELECT name FROM player WHERE rowid = ?", victim)
	log.Printf("Werewolves killed %s (player ID %d)", victimName, victim)
	DebugLog("resolveWerewolfVotes", "Werewolves killed '%s'", victimName)

	// Transition to day phase
	_, err = db.Exec("UPDATE game SET status = 'day' WHERE rowid = ?", game.ID)
	if err != nil {
		logError("resolveWerewolfVotes: transition to day", err)
		return
	}

	log.Printf("Night %d ended, transitioning to day phase", game.NightNumber)
	DebugLog("resolveWerewolfVotes", "Night %d ended, transitioning to day", game.NightNumber)
	LogDBState("after night resolution")

	broadcastGameUpdate()
}
