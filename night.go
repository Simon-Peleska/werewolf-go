package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
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
	IsAlive               bool
	Players               []Player
	AliveTargets          []Player
	IsWerewolf            bool
	Werewolves            []Player
	Votes                 []WerewolfVote
	CurrentVote           int64 // 0 means no vote
	WolfCubDoubleKill     bool  // werewolves must kill two this night
	CurrentVote2          int64 // this werewolf's second vote (0 = none)
	NightNumber           int
	IsSeer                bool
	HasInvestigated       bool
	SeerSelectedID        int64 // Pending investigation target this night (0 = none selected)
	SeerResults           []SeerResult
	IsDoctor              bool
	HasProtected          bool
	DoctorProtecting      string // Name of player being protected this night
	IsGuard               bool
	GuardHasProtected     bool
	GuardProtecting       string   // Name of player being protected this night
	GuardTargets          []Player // Alive targets excluding self and last night's target
	IsWitch               bool
	WitchVictim           string // Name of werewolf majority target (empty if no majority)
	WitchVictimID         int64  // ID of victim (0 if none)
	WitchVictim2          string // Name of Wolf Cub second kill target (empty if not set)
	WitchVictimID2        int64  // ID of second kill target (0 if none)
	HealPotionUsed        bool   // Committed heal used in ANY prior round (permanent)
	PoisonPotionUsed      bool   // Committed poison used in ANY prior round (permanent)
	WitchSelectedHealID   int64  // Pending heal target this night (0 = none selected)
	WitchSelectedPoisonID int64  // Pending poison target this night (0 = none selected)
	WitchHealedThisNight  bool   // Committed heal was applied this night (after Apply)
	WitchHealedName       string // Name of who the witch healed this night
	WitchKilledThisNight  bool   // Committed poison was applied this night (after Apply)
	WitchKilledTarget     string // Name of poison target if used tonight
	WitchDoneThisNight    bool   // True after witch_apply submitted
	IsMason               bool
	Masons                []Player // Other alive Masons (excluding self)
	IsCupid               bool
	CupidLinked           bool
	CupidChosen1ID        int64          // 0 if not chosen yet
	CupidChosen1          string         // name of first lover
	CupidChosen2ID        int64          // 0 if not chosen yet
	CupidChosen2          string         // name of second lover
	IsLover               bool           // is this player one of the two lovers?
	LoverName             string         // name of their partner
	AllWolvesActed        bool           // all werewolves have voted or passed (first kill)
	AllWolvesActed2       bool           // all werewolves have voted or passed (Wolf Cub second kill)
	WolfEndVoted          bool           // End Vote pressed for first kill this round
	WolfEndVoted2         bool           // End Vote pressed for Wolf Cub second kill
	SeerFoundWerewolves   map[int64]bool // player IDs the current player (as Seer) confirmed as wolves
	ShowSurvey            bool           // player has finished their role action, show survey
	HasSubmittedSurvey    bool           // this player already pressed Continue
	SurveyCount           int            // how many alive players have submitted the survey
	AliveCount            int            // total alive players (for "waiting for N more" display)
	SurveyTargets         []Player       // alive players for the suspect dropdown
	// Card display fields
	PlayerName      string
	RoleName        string
	RoleDescription string
	RoleTeam        string
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

	// Reject if End Vote already pressed (vote is locked in)
	var endVoteCount int
	db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote)
	if endVoteCount > 0 {
		sendErrorToast(client.playerID, "The vote has already been locked in")
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
	description := fmt.Sprintf("Night %d: %s voted to kill %s", game.Round, voter.Name, target.Name)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = ?, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill, targetID, VisibilityTeamWerewolf, description, targetID, description)
	if err != nil {
		logError("handleWSWerewolfVote: db.Exec insert vote", err)
		sendErrorToast(client.playerID, "Failed to record vote")
		return
	}

	log.Printf("Werewolf %d (%s) voted to kill player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSWerewolfVote", "Werewolf '%s' voted to kill '%s'", voter.Name, target.Name)
	LogDBState("after werewolf vote")

	broadcastGameUpdate()
}

func handleWSWerewolfVote2(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSWerewolfVote2: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}

	voter, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSWerewolfVote2: getPlayerInGame", err)
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

	// Validate that Wolf Cub double kill is actually active this night
	if game.Round <= 1 {
		sendErrorToast(client.playerID, "Wolf Cub double kill not active")
		return
	}
	var wolfCubDeathCount int
	db.Get(&wolfCubDeathCount, `
		SELECT COUNT(*) FROM game_action ga
		JOIN game_player gp ON ga.target_player_id = gp.player_id AND gp.game_id = ga.game_id
		JOIN role r ON gp.role_id = r.rowid
		WHERE ga.game_id = ? AND ga.round = ?
		AND ga.action_type IN ('werewolf_kill', 'elimination', 'hunter_revenge', 'witch_kill')
		AND r.name = 'Wolf Cub'`,
		game.ID, game.Round-1)
	if wolfCubDeathCount == 0 {
		sendErrorToast(client.playerID, "Wolf Cub double kill not active")
		return
	}

	// Reject if End Vote 2 already pressed
	var endVote2Count int
	db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote2)
	if endVote2Count > 0 {
		sendErrorToast(client.playerID, "The second vote has already been locked in")
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
		sendErrorToast(client.playerID, "Cannot target a dead player")
		return
	}

	description2 := fmt.Sprintf("Night %d: %s voted to kill %s (Wolf Cub revenge)", game.Round, voter.Name, target.Name)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = ?, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill2, targetID, VisibilityTeamWerewolf, description2, targetID, description2)
	if err != nil {
		logError("handleWSWerewolfVote2: db.Exec insert vote2", err)
		sendErrorToast(client.playerID, "Failed to record second vote")
		return
	}

	log.Printf("Werewolf %d (%s) voted second kill: player %d (%s)", client.playerID, voter.Name, targetID, target.Name)
	DebugLog("handleWSWerewolfVote2", "Werewolf '%s' second kill vote: '%s'", voter.Name, target.Name)
	LogDBState("after werewolf vote2")

	broadcastGameUpdate()
}

func handleWSWerewolfPass(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSWerewolfPass: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSWerewolfPass: getPlayerInGame", err)
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
	var endVoteCount int
	db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote)
	if endVoteCount > 0 {
		sendErrorToast(client.playerID, "The vote has already been locked in")
		return
	}
	passDesc := fmt.Sprintf("Night %d: %s passed", game.Round, voter.Name)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, NULL, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = NULL, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill, VisibilityTeamWerewolf, passDesc, passDesc)
	if err != nil {
		logError("handleWSWerewolfPass: db.Exec", err)
		sendErrorToast(client.playerID, "Failed to record pass")
		return
	}
	log.Printf("Werewolf %d (%s) passed the kill vote", client.playerID, voter.Name)
	broadcastGameUpdate()
}

func handleWSWerewolfPass2(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSWerewolfPass2: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSWerewolfPass2: getPlayerInGame", err)
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
	var endVote2Count int
	db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote2)
	if endVote2Count > 0 {
		sendErrorToast(client.playerID, "The second vote has already been locked in")
		return
	}
	passDesc := fmt.Sprintf("Night %d: %s passed (second kill)", game.Round, voter.Name)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, NULL, ?, ?)
		ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
		DO UPDATE SET target_player_id = NULL, description = ?`,
		game.ID, game.Round, client.playerID, ActionWerewolfKill2, VisibilityTeamWerewolf, passDesc, passDesc)
	if err != nil {
		logError("handleWSWerewolfPass2: db.Exec", err)
		sendErrorToast(client.playerID, "Failed to record pass")
		return
	}
	log.Printf("Werewolf %d (%s) passed the second kill vote", client.playerID, voter.Name)
	broadcastGameUpdate()
}

func handleWSWerewolfEndVote(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSWerewolfEndVote: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSWerewolfEndVote: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if voter.Team != "werewolf" {
		sendErrorToast(client.playerID, "Only werewolves can end the vote")
		return
	}

	// Check all alive werewolves have acted
	var werewolves []Player
	db.Select(&werewolves, `
		SELECT g.rowid as id, g.player_id as player_id, p.name as name
		FROM game_player g
		JOIN player p ON g.player_id = p.rowid
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

	var totalActed int
	db.Get(&totalActed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfKill)

	if totalActed < len(werewolves) {
		sendErrorToast(client.playerID, fmt.Sprintf("Not all werewolves have voted yet (%d/%d)", totalActed, len(werewolves)))
		return
	}

	// Record End Vote (INSERT OR IGNORE — idempotent)
	_, err = db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, NULL, ?, '')`,
		game.ID, game.Round, client.playerID, ActionWerewolfEndVote, VisibilityTeamWerewolf)
	if err != nil {
		logError("handleWSWerewolfEndVote: record end vote", err)
	}

	log.Printf("Werewolf %d (%s) ended the kill vote", client.playerID, voter.Name)
	broadcastSoundToast("info", "🐺 The werewolves have made their choice...")
	resolveWerewolfVotes(game)
}

func handleWSWerewolfEndVote2(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSWerewolfEndVote2: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		sendErrorToast(client.playerID, "Voting only allowed during night phase")
		return
	}
	voter, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSWerewolfEndVote2: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if voter.Team != "werewolf" {
		sendErrorToast(client.playerID, "Only werewolves can end the vote")
		return
	}

	var werewolves []Player
	db.Select(&werewolves, `
		SELECT g.rowid as id, g.player_id as player_id, p.name as name
		FROM game_player g
		JOIN player p ON g.player_id = p.rowid
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

	var totalActed2 int
	db.Get(&totalActed2, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfKill2)

	if totalActed2 < len(werewolves) {
		sendErrorToast(client.playerID, fmt.Sprintf("Not all werewolves have voted for the second kill yet (%d/%d)", totalActed2, len(werewolves)))
		return
	}

	_, err = db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, NULL, ?, '')`,
		game.ID, game.Round, client.playerID, ActionWerewolfEndVote2, VisibilityTeamWerewolf)
	if err != nil {
		logError("handleWSWerewolfEndVote2: record end vote 2", err)
	}

	log.Printf("Werewolf %d (%s) ended the second kill vote", client.playerID, voter.Name)
	resolveWerewolfVotes(game)
}

// handleWSSeerSelect toggles the seer's pending investigation selection.
// Clicking the same player again deselects; clicking a different player replaces the selection.
func handleWSSeerSelect(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSSeerSelect: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		sendErrorToast(client.playerID, "Can only act during night phase")
		return
	}
	investigator, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSSeerSelect: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if investigator.RoleName != "Seer" {
		sendErrorToast(client.playerID, "Only the Seer can select an investigation target")
		return
	}
	if !investigator.IsAlive {
		sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}
	// Don't allow re-selection if already investigated
	var existingCount int
	db.Get(&existingCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerInvestigate)
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
	if err != nil || !target.IsAlive {
		sendErrorToast(client.playerID, "Invalid target")
		return
	}

	var existing GameAction
	selectErr := db.Get(&existing, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerSelect)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// Same player clicked again → deselect
		db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionSeerSelect)
		log.Printf("Seer '%s' deselected investigation target", investigator.Name)
	} else {
		// Select (or replace prior selection)
		db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionSeerSelect, targetID, VisibilityActor)
		log.Printf("Seer '%s' selected investigation target %d", investigator.Name, targetID)
	}

	broadcastGameUpdate()
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
		game.ID, game.Round, client.playerID, ActionSeerInvestigate)
	if existingCount > 0 {
		sendErrorToast(client.playerID, "You have already investigated this night")
		return
	}

	// Read the pending selection from DB
	var selectAction GameAction
	if err := db.Get(&selectAction, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerSelect); err != nil || selectAction.TargetPlayerID == nil {
		sendErrorToast(client.playerID, "Select a player to investigate first")
		return
	}
	targetID := *selectAction.TargetPlayerID

	target, err := getPlayerInGame(game.ID, targetID)
	if err != nil {
		sendErrorToast(client.playerID, "Target not found")
		return
	}

	if !target.IsAlive {
		sendErrorToast(client.playerID, "Cannot investigate a dead player")
		return
	}

	// Remove the pending selection
	db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionSeerSelect)

	result := "not a werewolf"
	if target.Team == "werewolf" {
		result = "a werewolf"
	}
	seerDesc := fmt.Sprintf("Night %d: You investigated %s — they are %s", game.Round, target.Name, result)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionSeerInvestigate, targetID, VisibilityActor, seerDesc)
	if err != nil {
		logError("handleWSSeerInvestigate: db.Exec insert investigation", err)
		sendErrorToast(client.playerID, "Failed to record investigation")
		return
	}

	toastMsg := fmt.Sprintf("🔮 %s is not a werewolf.", target.Name)
	if target.Team == "werewolf" {
		toastMsg = fmt.Sprintf("🔮 %s is a werewolf!", target.Name)
	}
	hub.sendToPlayer(client.playerID, []byte(renderToast("info", toastMsg)))

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
		game.ID, game.Round, client.playerID, ActionDoctorProtect)
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

	doctorDesc := fmt.Sprintf("Night %d: You protected %s", game.Round, target.Name)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionDoctorProtect, targetID, VisibilityActor, doctorDesc)
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
		game.ID, game.Round, client.playerID, ActionGuardProtect)
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
	if game.Round > 1 {
		var lastTargetID int64
		err := db.Get(&lastTargetID, `
			SELECT target_player_id FROM game_action
			WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
			game.ID, game.Round-1, client.playerID, ActionGuardProtect)
		if err == nil && lastTargetID == targetID {
			sendErrorToast(client.playerID, "Cannot protect the same player two nights in a row")
			return
		}
	}

	guardDesc := fmt.Sprintf("Night %d: You protected %s", game.Round, target.Name)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionGuardProtect, targetID, VisibilityActor, guardDesc)
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

// handleWSCupidChoose handles Cupid's lover selection on Night 1.
// Picks are staged (replaceable) until Cupid explicitly confirms via handleWSCupidLink.
func handleWSCupidChoose(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSCupidChoose: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" || game.Round != 1 {
		sendErrorToast(client.playerID, "Cupid can only act on Night 1")
		return
	}

	cupid, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSCupidChoose: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if cupid.RoleName != "Cupid" || !cupid.IsAlive {
		sendErrorToast(client.playerID, "Only the living Cupid can link lovers")
		return
	}

	// Reject if already finalized
	var finalized int
	db.Get(&finalized, `SELECT COUNT(*) FROM game_lovers WHERE game_id = ?`, game.ID)
	if finalized > 0 {
		sendErrorToast(client.playerID, "You have already linked the lovers")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		sendErrorToast(client.playerID, "Invalid target")
		return
	}
	target, err := getPlayerInGame(game.ID, targetID)
	if err != nil || !target.IsAlive {
		sendErrorToast(client.playerID, "Invalid target")
		return
	}

	// Check if step 1 is already done (first lover stored in game_action)
	var firstLoverID int64
	db.Get(&firstLoverID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
		game.ID, client.playerID, ActionCupidLink)

	if firstLoverID == 0 {
		// Step 1: record first lover choice (replaceable until finalized); empty description = not shown in history
		_, err = db.Exec(`
			INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
			VALUES (?, 1, 'night', ?, ?, ?, ?, '')
			ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
			DO UPDATE SET target_player_id = ?, description = ''`,
			game.ID, client.playerID, ActionCupidLink, targetID, VisibilityActor, targetID)
		if err != nil {
			logError("handleWSCupidChoose: insert step1", err)
			sendErrorToast(client.playerID, "Failed to record choice")
			return
		}
		log.Printf("Cupid '%s' chose first lover: '%s'", cupid.Name, target.Name)
		broadcastGameUpdate()
	} else {
		// Step 2: stage second lover — must differ from first lover
		if firstLoverID == targetID {
			sendErrorToast(client.playerID, "The two lovers must be different players")
			return
		}
		_, err = db.Exec(`
			INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
			VALUES (?, 1, 'night', ?, ?, ?, ?, '')
			ON CONFLICT(game_id, round, phase, actor_player_id, action_type)
			DO UPDATE SET target_player_id = ?, description = ''`,
			game.ID, client.playerID, ActionCupidLink2, targetID, VisibilityActor, targetID)
		if err != nil {
			logError("handleWSCupidChoose: insert step2", err)
			sendErrorToast(client.playerID, "Failed to record choice")
			return
		}
		log.Printf("Cupid '%s' chose second lover: '%s'", cupid.Name, target.Name)
		broadcastGameUpdate()
	}
}

// handleWSCupidLink finalizes Cupid's staged lover choices on Night 1.
func handleWSCupidLink(client *Client) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSCupidLink: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" || game.Round != 1 {
		sendErrorToast(client.playerID, "Cupid can only act on Night 1")
		return
	}

	cupid, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSCupidLink: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}

	if cupid.RoleName != "Cupid" || !cupid.IsAlive {
		sendErrorToast(client.playerID, "Only the living Cupid can link lovers")
		return
	}

	var finalized int
	db.Get(&finalized, `SELECT COUNT(*) FROM game_lovers WHERE game_id = ?`, game.ID)
	if finalized > 0 {
		sendErrorToast(client.playerID, "You have already linked the lovers")
		return
	}

	var firstLoverID int64
	var secondLoverID int64
	db.Get(&firstLoverID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
		game.ID, client.playerID, ActionCupidLink)
	db.Get(&secondLoverID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
		game.ID, client.playerID, ActionCupidLink2)

	if firstLoverID == 0 || secondLoverID == 0 {
		sendErrorToast(client.playerID, "Choose two lovers before linking them")
		return
	}
	if firstLoverID == secondLoverID {
		sendErrorToast(client.playerID, "The two lovers must be different players")
		return
	}

	var first Player
	var second Player
	first, err = getPlayerInGame(game.ID, firstLoverID)
	if err != nil || !first.IsAlive {
		sendErrorToast(client.playerID, "First lover is invalid")
		return
	}
	second, err = getPlayerInGame(game.ID, secondLoverID)
	if err != nil || !second.IsAlive {
		sendErrorToast(client.playerID, "Second lover is invalid")
		return
	}

	_, err = db.Exec(`INSERT OR IGNORE INTO game_lovers (game_id, player1_id, player2_id) VALUES (?, ?, ?)`,
		game.ID, firstLoverID, secondLoverID)
	if err != nil {
		logError("handleWSCupidLink: insert lovers row1", err)
		sendErrorToast(client.playerID, "Failed to link lovers")
		return
	}
	_, err = db.Exec(`INSERT OR IGNORE INTO game_lovers (game_id, player1_id, player2_id) VALUES (?, ?, ?)`,
		game.ID, secondLoverID, firstLoverID)
	if err != nil {
		logError("handleWSCupidLink: insert lovers row2", err)
		sendErrorToast(client.playerID, "Failed to link lovers")
		return
	}

	desc1 := fmt.Sprintf("Night 1: Your lover is %s", second.Name)
	desc2 := fmt.Sprintf("Night 1: Your lover is %s", first.Name)
	_, _ = db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, 1, 'night', ?, ?, ?, ?, ?)`,
		game.ID, firstLoverID, ActionCupidLink, secondLoverID, VisibilityActor, desc1)
	_, _ = db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, 1, 'night', ?, ?, ?, ?, ?)`,
		game.ID, secondLoverID, ActionCupidLink, firstLoverID, VisibilityActor, desc2)

	// Clear Cupid's staged picks once finalized.
	_, _ = db.Exec(`DELETE FROM game_action WHERE game_id = ? AND round = 1 AND phase = 'night' AND actor_player_id = ? AND action_type IN (?, ?)`,
		game.ID, client.playerID, ActionCupidLink, ActionCupidLink2)

	hub.sendToPlayer(firstLoverID, []byte(renderToast("info", fmt.Sprintf("💞 Cupid has linked you! Your lover is %s.", second.Name))))
	hub.sendToPlayer(secondLoverID, []byte(renderToast("info", fmt.Sprintf("💞 Cupid has linked you! Your lover is %s.", first.Name))))

	log.Printf("Cupid '%s' linked lovers: '%s' and '%s'", cupid.Name, first.Name, second.Name)
	DebugLog("handleWSCupidLink", "Cupid '%s' linked '%s' and '%s'", cupid.Name, first.Name, second.Name)
	LogDBState("after cupid links lovers")
	resolveWerewolfVotes(game)
}

// handleWSWitchSelectHeal toggles the witch's pending heal selection.
// Clicking the same player again deselects; clicking a different player replaces the selection.
func handleWSWitchSelectHeal(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSWitchSelectHeal: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		sendErrorToast(client.playerID, "Can only act during night phase")
		return
	}
	witch, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSWitchSelectHeal: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if witch.RoleName != "Witch" {
		sendErrorToast(client.playerID, "Only the Witch can select a heal target")
		return
	}
	if !witch.IsAlive {
		sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}
	// Reject if already applied this night
	var appliedCount int
	db.Get(&appliedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchApply)
	if appliedCount > 0 {
		sendErrorToast(client.playerID, "You have already submitted your actions for this night")
		return
	}
	// Reject if heal potion already committed in a prior round
	var healUsed int
	db.Get(&healUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionWitchHeal)
	if healUsed > 0 {
		sendErrorToast(client.playerID, "Your heal potion has already been used")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		sendErrorToast(client.playerID, "Invalid target")
		return
	}

	// Toggle: if same player already selected, deselect
	var existing GameAction
	selectErr := db.Get(&existing, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectHeal)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// Same player clicked again → deselect
		db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionWitchSelectHeal)
		log.Printf("Witch '%s' deselected heal target", witch.Name)
	} else {
		// Select (or replace prior selection)
		db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionWitchSelectHeal, targetID, VisibilityActor)
		log.Printf("Witch '%s' selected heal target %d", witch.Name, targetID)
	}

	broadcastGameUpdate()
}

// handleWSWitchSelectPoison toggles the witch's pending poison selection.
func handleWSWitchSelectPoison(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSWitchSelectPoison: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		sendErrorToast(client.playerID, "Can only act during night phase")
		return
	}
	witch, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSWitchSelectPoison: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if witch.RoleName != "Witch" {
		sendErrorToast(client.playerID, "Only the Witch can select a poison target")
		return
	}
	if !witch.IsAlive {
		sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}
	// Reject if already applied this night
	var appliedCount int
	db.Get(&appliedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchApply)
	if appliedCount > 0 {
		sendErrorToast(client.playerID, "You have already submitted your actions for this night")
		return
	}
	// Reject if poison potion already committed in a prior round
	var poisonUsed int
	db.Get(&poisonUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
		game.ID, client.playerID, ActionWitchKill)
	if poisonUsed > 0 {
		sendErrorToast(client.playerID, "Your poison potion has already been used")
		return
	}

	targetID, err := strconv.ParseInt(msg.TargetPlayerID, 10, 64)
	if err != nil {
		sendErrorToast(client.playerID, "Invalid target")
		return
	}

	// Toggle: if same player already selected, deselect
	var existing GameAction
	selectErr := db.Get(&existing, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectPoison)
	if selectErr == nil && existing.TargetPlayerID != nil && *existing.TargetPlayerID == targetID {
		// Same player clicked again → deselect
		db.Exec(`DELETE FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			game.ID, game.Round, client.playerID, ActionWitchSelectPoison)
		log.Printf("Witch '%s' deselected poison target", witch.Name)
	} else {
		// Select (or replace prior selection)
		db.Exec(`INSERT OR REPLACE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, client.playerID, ActionWitchSelectPoison, targetID, VisibilityActor)
		log.Printf("Witch '%s' selected poison target %d", witch.Name, targetID)
	}

	broadcastGameUpdate()
}

// handleWSWitchApply commits the witch's pending selections and ends her night turn.
func handleWSWitchApply(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSWitchApply: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}
	if game.Status != "night" {
		sendErrorToast(client.playerID, "Can only act during night phase")
		return
	}
	witch, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil {
		logError("handleWSWitchApply: getPlayerInGame", err)
		sendErrorToast(client.playerID, "You are not in this game")
		return
	}
	if witch.RoleName != "Witch" {
		sendErrorToast(client.playerID, "Only the Witch can apply actions")
		return
	}
	if !witch.IsAlive {
		sendErrorToast(client.playerID, "Dead players cannot act")
		return
	}
	// Idempotency guard
	var appliedCount int
	db.Get(&appliedCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchApply)
	if appliedCount > 0 {
		sendErrorToast(client.playerID, "You have already submitted your actions for this night")
		return
	}

	// Commit pending heal selection if present
	var healAction GameAction
	if err := db.Get(&healAction, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectHeal); err == nil && healAction.TargetPlayerID != nil {

		targetID := *healAction.TargetPlayerID
		// Final validation: heal potion not used globally
		var healUsed int
		db.Get(&healUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
			game.ID, client.playerID, ActionWitchHeal)
		if healUsed > 0 {
			sendErrorToast(client.playerID, "Your heal potion has already been used")
			return
		}
		// Cannot heal self
		if targetID == client.playerID {
			sendErrorToast(client.playerID, "You cannot heal yourself")
			return
		}
		// Heal target must be a wolf victim (End Vote must have been pressed)
		var endVoteCount int
		db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
			game.ID, game.Round, ActionWerewolfEndVote)
		if endVoteCount == 0 {
			sendErrorToast(client.playerID, "Werewolves have not locked in their vote yet")
			return
		}
		var isVictim int
		db.Get(&isVictim, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type IN (?,?) AND target_player_id=?`,
			game.ID, game.Round, ActionWerewolfKill, ActionWerewolfKill2, targetID)
		if isVictim == 0 {
			sendErrorToast(client.playerID, "You can only heal a werewolf target")
			return
		}
		var targetName string
		db.Get(&targetName, "SELECT name FROM player WHERE rowid = ?", targetID)
		witchHealDesc := fmt.Sprintf("Night %d: You saved %s with your heal potion", game.Round, targetName)
		_, err = db.Exec(`
			INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
			VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
			game.ID, game.Round, client.playerID, ActionWitchHeal, targetID, VisibilityActor, witchHealDesc)
		if err != nil {
			logError("handleWSWitchApply: commit heal", err)
			sendErrorToast(client.playerID, "Failed to commit heal")
			return
		}
		log.Printf("Witch '%s' committed heal on player %d (%s)", witch.Name, targetID, targetName)
	}

	// Commit pending poison selection if present
	var poisonAction GameAction
	if err := db.Get(&poisonAction, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
		game.ID, game.Round, client.playerID, ActionWitchSelectPoison); err == nil && poisonAction.TargetPlayerID != nil {

		targetID := *poisonAction.TargetPlayerID
		// Final validation: poison potion not used globally
		var poisonUsed int
		db.Get(&poisonUsed, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND actor_player_id=? AND action_type=?`,
			game.ID, client.playerID, ActionWitchKill)
		if poisonUsed > 0 {
			sendErrorToast(client.playerID, "Your poison potion has already been used")
			return
		}
		target, err := getPlayerInGame(game.ID, targetID)
		if err != nil || !target.IsAlive {
			sendErrorToast(client.playerID, "Poison target is no longer valid")
			return
		}
		witchKillDesc := fmt.Sprintf("Night %d: You poisoned %s", game.Round, target.Name)
		_, err = db.Exec(`
			INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description)
			VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
			game.ID, game.Round, client.playerID, ActionWitchKill, targetID, VisibilityActor, witchKillDesc)
		if err != nil {
			logError("handleWSWitchApply: commit poison", err)
			sendErrorToast(client.playerID, "Failed to commit poison")
			return
		}
		log.Printf("Witch '%s' committed poison on player %d (%s)", witch.Name, targetID, target.Name)
	}

	// Record that the witch has finished her night turn
	witchApplyDesc := fmt.Sprintf("Night %d: Witch %s confirmed her actions", game.Round, witch.Name)
	_, err = db.Exec(`
		INSERT INTO game_action (game_id, round, phase, actor_player_id, action_type, visibility, description)
		VALUES (?, ?, 'night', ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionWitchApply, VisibilityActor, witchApplyDesc)
	if err != nil {
		logError("handleWSWitchApply: insert apply", err)
		sendErrorToast(client.playerID, "Failed to record witch action")
		return
	}

	log.Printf("Witch '%s' applied actions for night %d", witch.Name, game.Round)
	DebugLog("handleWSWitchApply", "Witch '%s' applied", witch.Name)
	LogDBState("after witch apply")

	resolveWerewolfVotes(game)
}

// playerDoneWithNightAction returns true if the given player has completed their night role action
// and the survey should be shown to them.
func playerDoneWithNightAction(gameID int64, round int, player Player) bool {
	switch player.RoleName {
	case "Villager", "Mason", "Hunter":
		return true // no night action
	case "Seer":
		var c int
		db.Get(&c, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			gameID, round, player.PlayerID, ActionSeerInvestigate)
		return c > 0
	case "Doctor":
		var c int
		db.Get(&c, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			gameID, round, player.PlayerID, ActionDoctorProtect)
		return c > 0
	case "Guard":
		var c int
		db.Get(&c, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			gameID, round, player.PlayerID, ActionGuardProtect)
		return c > 0
	case "Witch":
		var c int
		db.Get(&c, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
			gameID, round, player.PlayerID, ActionWitchApply)
		return c > 0
	case "Werewolf", "Wolf Cub":
		// Survey available after End Vote is pressed (any wolf)
		var c int
		db.Get(&c, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
			gameID, round, ActionWerewolfEndVote)
		if c == 0 {
			return false
		}
		// If Wolf Cub double kill is active this round, also require End Vote 2
		if round > 1 {
			var wolfCubDeathCount int
			db.Get(&wolfCubDeathCount, `
				SELECT COUNT(*) FROM game_action ga
				JOIN game_player gp ON ga.target_player_id = gp.player_id AND gp.game_id = ga.game_id
				JOIN role r ON gp.role_id = r.rowid
				WHERE ga.game_id = ? AND ga.round = ?
				AND ga.action_type IN ('werewolf_kill', 'elimination', 'hunter_revenge', 'witch_kill')
				AND r.name = 'Wolf Cub'`,
				gameID, round-1)
			if wolfCubDeathCount > 0 {
				var c2 int
				db.Get(&c2, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
					gameID, round, ActionWerewolfEndVote2)
				return c2 > 0
			}
		}
		return true
	case "Cupid":
		if round > 1 {
			return true
		}
		var loverCount int
		db.Get(&loverCount, `SELECT COUNT(*) FROM game_lovers WHERE game_id=?`, gameID)
		return loverCount > 0
	default:
		return true
	}
}

// handleWSNightSurvey records a player's night survey answers.
// Once all alive players have submitted, the game transitions to day.
func handleWSNightSurvey(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSNightSurvey: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "night" {
		sendErrorToast(client.playerID, "Survey only available during night phase")
		return
	}

	player, err := getPlayerInGame(game.ID, client.playerID)
	if err != nil || !player.IsAlive {
		sendErrorToast(client.playerID, "You must be alive to submit the survey")
		return
	}

	// Idempotent: ignore if already submitted
	var existing int
	db.Get(&existing, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=? AND actor_player_id=?`,
		game.ID, game.Round, ActionNightSurvey, client.playerID)
	if existing > 0 {
		return
	}

	// Build description from non-empty fields
	suspectID, _ := strconv.ParseInt(msg.SuspectPlayerID, 10, 64)
	deathTheory := strings.TrimSpace(msg.DeathTheory)
	notes := strings.TrimSpace(msg.Notes)

	var parts []string
	if suspectID > 0 {
		var suspectName string
		db.Get(&suspectName, "SELECT name FROM player WHERE rowid=?", suspectID)
		if suspectName != "" {
			parts = append(parts, "Suspects: "+suspectName)
		}
	}
	if deathTheory != "" {
		parts = append(parts, "Theory: "+deathTheory)
	}
	if notes != "" {
		parts = append(parts, "Notes: "+notes)
	}

	var description string
	if len(parts) > 0 {
		description = fmt.Sprintf("Night %d: %s — %s", game.Round, player.Name, strings.Join(parts, " | "))
	}
	// If all empty, description stays "" (counts as submitted, hidden from history)

	_, err = db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?)`,
		game.ID, game.Round, client.playerID, ActionNightSurvey, VisibilityResolved, description)
	if err != nil {
		logError("handleWSNightSurvey: insert survey", err)
		sendErrorToast(client.playerID, "Failed to record survey")
		return
	}

	log.Printf("Survey submitted by '%s' (game %d round %d)", player.Name, game.ID, game.Round)

	// Transition to day if all alive players have now submitted
	var aliveCount int
	db.Get(&aliveCount, `SELECT COUNT(*) FROM game_player WHERE game_id=? AND is_alive=1`, game.ID)
	var surveyCount int
	db.Get(&surveyCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
		game.ID, game.Round, ActionNightSurvey)

	log.Printf("Night survey progress: %d/%d", surveyCount, aliveCount)

	if surveyCount >= aliveCount {
		// Apply all pending night kills (description='' marks them as pending)
		type pendingKill struct {
			ID             int64 `db:"id"`
			TargetPlayerID int64 `db:"target_player_id"`
		}
		var pendingKills []pendingKill
		db.Select(&pendingKills, `SELECT rowid as id, target_player_id FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=? AND description=''`,
			game.ID, game.Round, ActionNightKill)

		var nightKills []int64
		for _, pk := range pendingKills {
			if _, err = db.Exec("UPDATE game_player SET is_alive=0 WHERE game_id=? AND player_id=?", game.ID, pk.TargetPlayerID); err != nil {
				logError("handleWSNightSurvey: apply kill", err)
				continue
			}
			var name, roleName string
			db.Get(&name, "SELECT name FROM player WHERE rowid=?", pk.TargetPlayerID)
			db.Get(&roleName, `SELECT r.name FROM game_player gp JOIN role r ON gp.role_id=r.rowid WHERE gp.game_id=? AND gp.player_id=?`, game.ID, pk.TargetPlayerID)
			desc := fmt.Sprintf("Night %d: %s (%s) was found dead", game.Round, name, roleName)
			db.Exec(`UPDATE game_action SET description=? WHERE rowid=?`, desc, pk.ID)
			nightKills = append(nightKills, pk.TargetPlayerID)
			log.Printf("Applied pending night kill: %s (%s)", name, roleName)
		}

		// Transition to day, then apply heartbreaks and check win conditions
		if _, err = db.Exec("UPDATE game SET status='day' WHERE rowid=?", game.ID); err != nil {
			logError("handleWSNightSurvey: transition to day", err)
			return
		}
		applyHeartbreaks(game, "night", nightKills)

		log.Printf("Night %d ended (all surveys submitted), transitioning to day", game.Round)
		LogDBState("after all surveys submitted and kills applied")

		if checkWinConditions(game) {
			return
		}
		if len(nightKills) > 0 {
			maybeGenerateStory(game.ID, game.Round, "night", nightKills[0])
		}
	}

	broadcastGameUpdate()
}

// recordPublicDeath inserts a public history entry when a player dies at night.
func recordPublicDeath(game *Game, playerID int64) {
	var name string
	db.Get(&name, "SELECT name FROM player WHERE rowid = ?", playerID)
	var roleName string
	db.Get(&roleName, `SELECT r.name FROM game_player gp JOIN role r ON gp.role_id = r.rowid WHERE gp.game_id = ? AND gp.player_id = ?`, game.ID, playerID)
	desc := fmt.Sprintf("Night %d: %s (%s) was found dead", game.Round, name, roleName)
	_, err := db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, ?)`,
		game.ID, game.Round, playerID, ActionNightKill, playerID, VisibilityPublic, desc)
	if err != nil {
		logError("recordPublicDeath: insert death record", err)
	} else {
		log.Printf("Recorded public death: %s", desc)
	}
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
		game.ID, game.Round, ActionWerewolfKill)
	if err != nil {
		logError("resolveWerewolfVotes: get votes", err)
		return
	}

	log.Printf("Werewolf vote check: %d werewolves, %d votes", len(werewolves), len(votes))

	// Check if all werewolves have voted or passed
	if len(votes) < len(werewolves) {
		log.Printf("Not all werewolves have voted yet (%d/%d)", len(votes), len(werewolves))
		broadcastGameUpdate()
		return
	}

	// Check if End Vote has been pressed — don't resolve until one wolf locks it in
	var endVoteCount int
	db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
		game.ID, game.Round, ActionWerewolfEndVote)
	if endVoteCount == 0 {
		log.Printf("Werewolves have all voted but End Vote not pressed yet")
		broadcastGameUpdate()
		return
	}

	// Count votes for each target (NULL = pass, excluded from counts)
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
	// If no majority (all passed or split), victim = 0 (no kill) — proceed to check other night roles
	majority := len(werewolves)/2 + 1
	if maxVotes < majority {
		log.Printf("No majority reached (need %d, max is %d) — no kill this night", majority, maxVotes)
		victim = 0
	}

	// Check if Wolf Cub died last round → double kill required
	wolfCubDoubleKill := false
	var victim2 int64
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
	}

	if wolfCubDoubleKill {
		var votes2 []GameAction
		db.Select(&votes2, `
			SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
			FROM game_action
			WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
			game.ID, game.Round, ActionWerewolfKill2)

		if len(votes2) < len(werewolves) {
			log.Printf("Wolf Cub double kill: waiting for second votes (%d/%d)", len(votes2), len(werewolves))
			broadcastGameUpdate()
			return
		}

		var endVote2Count int
		db.Get(&endVote2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
			game.ID, game.Round, ActionWerewolfEndVote2)
		if endVote2Count == 0 {
			log.Printf("Wolf Cub double kill: End Vote 2 not pressed yet")
			broadcastGameUpdate()
			return
		}

		voteCounts2 := make(map[int64]int)
		for _, v := range votes2 {
			if v.TargetPlayerID != nil {
				voteCounts2[*v.TargetPlayerID]++
			}
		}
		var maxVotes2 int
		for targetID, count := range voteCounts2 {
			if count > maxVotes2 {
				maxVotes2 = count
				victim2 = targetID
			}
		}
		if maxVotes2 < majority {
			log.Printf("Wolf Cub double kill: no majority for second victim (need %d, max is %d) — no second kill", majority, maxVotes2)
			victim2 = 0
		}
	}

	// Night 1: check Cupid has linked lovers before resolving
	if game.Round == 1 {
		var aliveCupidCount int
		db.Get(&aliveCupidCount, `
			SELECT COUNT(*) FROM game_player g
			JOIN role r ON g.role_id = r.rowid
			WHERE g.game_id = ? AND g.is_alive = 1 AND r.name = 'Cupid'`, game.ID)
		if aliveCupidCount > 0 {
			var loverCount int
			db.Get(&loverCount, `SELECT COUNT(*) FROM game_lovers WHERE game_id = ?`, game.ID)
			if loverCount == 0 {
				log.Printf("Waiting for Cupid to link lovers")
				broadcastGameUpdate()
				return
			}
		}
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
		game.ID, game.Round, ActionSeerInvestigate)

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
		game.ID, game.Round, ActionDoctorProtect)

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
		game.ID, game.Round, ActionGuardProtect)

	if guardProtectCount < aliveGuardCount {
		log.Printf("Waiting for guards to protect (%d/%d)", guardProtectCount, aliveGuardCount)
		broadcastGameUpdate()
		return
	}

	// Check if all alive Witches have passed before resolving the night
	var aliveWitchCount int
	db.Get(&aliveWitchCount, `
		SELECT COUNT(*) FROM game_player g
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.name = 'Witch'`, game.ID)

	if aliveWitchCount > 0 {
		var witchApplyCount int
		db.Get(&witchApplyCount, `
			SELECT COUNT(*) FROM game_action
			WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
			game.ID, game.Round, ActionWitchApply)

		if witchApplyCount < aliveWitchCount {
			log.Printf("Waiting for witch to apply (%d/%d)", witchApplyCount, aliveWitchCount)
			broadcastGameUpdate()
			return
		}
	}

	// No kill this night (wolves passed or no majority) — record pending kills for wolf cub and witch
	if victim == 0 {
		log.Printf("No werewolf kill this night (wolves passed or no majority)")
		// Wolf Cub second kill (pending — applied when surveys complete)
		if wolfCubDoubleKill && victim2 != 0 {
			var protect2Count int
			db.Get(&protect2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type IN (?, ?, ?) AND target_player_id = ?`,
				game.ID, game.Round, ActionDoctorProtect, ActionGuardProtect, ActionWitchHeal, victim2)
			var victim2Name string
			db.Get(&victim2Name, "SELECT name FROM player WHERE rowid = ?", victim2)
			if protect2Count > 0 {
				log.Printf("Protection saved %s from Wolf Cub double kill", victim2Name)
			} else {
				log.Printf("Wolf Cub double kill pending: %s", victim2Name)
				db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
					game.ID, game.Round, victim2, ActionNightKill, victim2, VisibilityPublic)
			}
		}
		// Witch poison (pending — applied when surveys complete)
		var witchKillActionNoVictim GameAction
		if wkErr := db.Get(&witchKillActionNoVictim, `SELECT * FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`, game.ID, game.Round, ActionWitchKill); wkErr == nil && witchKillActionNoVictim.TargetPlayerID != nil {
			var poisonName string
			db.Get(&poisonName, "SELECT name FROM player WHERE rowid = ?", *witchKillActionNoVictim.TargetPlayerID)
			log.Printf("Witch poison pending: %s", poisonName)
			db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
				game.ID, game.Round, *witchKillActionNoVictim.TargetPlayerID, ActionNightKill, *witchKillActionNoVictim.TargetPlayerID, VisibilityPublic)
		}
		log.Printf("Night %d: no wolf kill, waiting for surveys", game.Round)
		broadcastGameUpdate()
		return
	}

	// Check if the victim is protected by any Doctor
	var protectionCount int
	db.Get(&protectionCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ? AND target_player_id = ?`,
		game.ID, game.Round, ActionDoctorProtect, victim)

	// Check if the victim is protected by any Guard
	var guardProtectionCount int
	db.Get(&guardProtectionCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ? AND target_player_id = ?`,
		game.ID, game.Round, ActionGuardProtect, victim)

	// Check if the victim is healed by the Witch (target-specific)
	var witchHealCount int
	db.Get(&witchHealCount, `
		SELECT COUNT(*) FROM game_action
		WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ? AND target_player_id = ?`,
		game.ID, game.Round, ActionWitchHeal, victim)

	if protectionCount > 0 || guardProtectionCount > 0 || witchHealCount > 0 {
		var victimName string
		db.Get(&victimName, "SELECT name FROM player WHERE rowid = ?", victim)
		if protectionCount > 0 {
			log.Printf("Doctor saved %s (player ID %d) from werewolf attack", victimName, victim)
		}
		if guardProtectionCount > 0 {
			log.Printf("Guard saved %s (player ID %d) from werewolf attack", victimName, victim)
		}
		if witchHealCount > 0 {
			log.Printf("Witch saved %s (player ID %d) from werewolf attack", victimName, victim)
		}

		// Wolf Cub second kill may still land even if main victim is protected
		if wolfCubDoubleKill && victim2 != 0 {
			var protect2Count int
			db.Get(&protect2Count, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type IN (?, ?, ?) AND target_player_id = ?`,
				game.ID, game.Round, ActionDoctorProtect, ActionGuardProtect, ActionWitchHeal, victim2)
			var victim2Name string
			db.Get(&victim2Name, "SELECT name FROM player WHERE rowid = ?", victim2)
			if protect2Count > 0 {
				log.Printf("Protection saved %s from Wolf Cub double kill", victim2Name)
			} else {
				log.Printf("Wolf Cub double kill pending: %s", victim2Name)
				db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
					game.ID, game.Round, victim2, ActionNightKill, victim2, VisibilityPublic)
			}
		}
		// Witch poison is separate from the main wolf kill
		var witchKillActionP2 GameAction
		if wkErr := db.Get(&witchKillActionP2, `SELECT * FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`, game.ID, game.Round, ActionWitchKill); wkErr == nil && witchKillActionP2.TargetPlayerID != nil {
			var poisonName string
			db.Get(&poisonName, "SELECT name FROM player WHERE rowid = ?", *witchKillActionP2.TargetPlayerID)
			log.Printf("Witch poison pending: %s", poisonName)
			db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
				game.ID, game.Round, *witchKillActionP2.TargetPlayerID, ActionNightKill, *witchKillActionP2.TargetPlayerID, VisibilityPublic)
		}

		log.Printf("Night %d: main victim protected, waiting for surveys", game.Round)
		LogDBState("after protection save")
		broadcastGameUpdate()
		return
	}

	// Insert pending kill for main wolf victim (description='' hides from history until surveys complete)
	var victimName string
	db.Get(&victimName, "SELECT name FROM player WHERE rowid = ?", victim)
	log.Printf("Werewolf kill pending: %s (player ID %d)", victimName, victim)
	DebugLog("resolveWerewolfVotes", "Werewolf kill pending: '%s', waiting for surveys", victimName)
	db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
		game.ID, game.Round, victim, ActionNightKill, victim, VisibilityPublic)

	// Witch poison (pending — applied when surveys complete)
	var witchKillAction GameAction
	if err := db.Get(&witchKillAction, `SELECT * FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`, game.ID, game.Round, ActionWitchKill); err == nil && witchKillAction.TargetPlayerID != nil {
		var poisonVictimName string
		db.Get(&poisonVictimName, "SELECT name FROM player WHERE rowid = ?", *witchKillAction.TargetPlayerID)
		log.Printf("Witch poison pending: %s (player ID %d)", poisonVictimName, *witchKillAction.TargetPlayerID)
		db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
			game.ID, game.Round, *witchKillAction.TargetPlayerID, ActionNightKill, *witchKillAction.TargetPlayerID, VisibilityPublic)
	}

	// Wolf Cub second kill (pending — applied when surveys complete)
	if wolfCubDoubleKill && victim2 != 0 && victim2 != victim {
		var protect2Count int
		db.Get(&protect2Count, `
			SELECT COUNT(*) FROM game_action
			WHERE game_id = ? AND round = ? AND phase = 'night'
			AND action_type IN (?, ?, ?) AND target_player_id = ?`,
			game.ID, game.Round, ActionDoctorProtect, ActionGuardProtect, ActionWitchHeal, victim2)
		var victim2Name string
		db.Get(&victim2Name, "SELECT name FROM player WHERE rowid = ?", victim2)
		if protect2Count > 0 {
			log.Printf("Protection saved %s (player ID %d) from Wolf Cub double kill", victim2Name, victim2)
		} else {
			log.Printf("Wolf Cub double kill pending: %s (player ID %d)", victim2Name, victim2)
			db.Exec(`INSERT OR IGNORE INTO game_action (game_id, round, phase, actor_player_id, action_type, target_player_id, visibility, description) VALUES (?, ?, 'night', ?, ?, ?, ?, '')`,
				game.ID, game.Round, victim2, ActionNightKill, victim2, VisibilityPublic)
		}
	}

	log.Printf("Night %d: kills pending, waiting for surveys", game.Round)
	LogDBState("after pending night kills recorded")
	broadcastGameUpdate()
}
