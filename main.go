package main

import (
	"bytes"
	"database/sql"
	"embed"
	"encoding/json"
	"flag"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var templates *template.Template
var db *sqlx.DB
var devMode bool

// logError logs an error with context and dumps the database in dev mode
func logError(context string, err error) {
	log.Printf("ERROR [%s]: %v", context, err)
	if devMode {
		rows, _ := db.Query(".dump")
		log.Printf("DB dump: %v", rows)
	}
}

// broadcastGameUpdate sends the current game state to all connected clients
func broadcastGameUpdate() {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("broadcastGameUpdate: getOrCreateCurrentGame", err)
		return
	}

	players, err := getPlayersByGameId(game.ID)
	if err != nil {
		logError("broadcastGameUpdate: getPlayersByGameId", err)
		return
	}

	DebugLog("broadcastGameUpdate", "Broadcasting to %d players in game %d (status: %s)", len(players), game.ID, game.Status)

	for _, p := range players {
		// Send game component
		buf, err := getGameComponent(p.PlayerID, game)
		if err != nil {
			logError("broadcastGameUpdate: getGameComponent", err)
			continue
		}
		hub.sendToPlayer(p.PlayerID, buf.Bytes())

		// Send character info
		var charBuf bytes.Buffer

		data := SidebarData{
			Player:              &p,
			Players:             players,
			Game:                game,
			LoverPartnerID:      getLoverPartner(game.ID, p.PlayerID),
			SeerFoundWerewolves: getSeerFoundWerewolves(game.ID, p.PlayerID),
		}

		templates.ExecuteTemplate(&charBuf, "sidebar.html", data)
		hub.sendToPlayer(p.PlayerID, charBuf.Bytes())

		historyBuf, err := getGameHistory(p.PlayerID, game)
		if err != nil {
			logError("broadcastGameHistory: getGameHistory", err)
			continue
		}
		hub.sendToPlayer(p.PlayerID, historyBuf.Bytes())
	}
}

// getOrCreateCurrentGame returns the current waiting game, or creates one if none exists
func getOrCreateCurrentGame() (*Game, error) {
	var game Game
	err := db.Get(&game, "SELECT rowid as id, status, round FROM game ORDER BY id DESC LIMIT 1")
	if err == sql.ErrNoRows {
		result, err := db.Exec("INSERT INTO game (status, round) VALUES ('lobby', 0)")
		if err != nil {
			return nil, err
		}
		gameID, _ := result.LastInsertId()
		game = Game{ID: gameID, Status: "lobby", Round: 0}
		log.Printf("Created new game: id=%d, status='lobby'", gameID)
		DebugLog("getOrCreateCurrentGame", "Created new game %d", gameID)
		LogDBState("after new game created")
	} else if err != nil {
		return nil, err
	}
	return &game, nil
}

type GameData struct {
	Player        *Player
	Players       []Player
	Game          *Game
	IsInGame      bool
	GameComponent string
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	playerID, err := getPlayerIdFromSession(r)
	loggedIn := err == nil && playerID > 0

	if loggedIn {
		var playerName string
		db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)
		DebugLog("handleIndex", "Page accessed by logged-in player '%s' (ID: %d)", playerName, playerID)
	} else {
		DebugLog("handleIndex", "Page accessed by anonymous visitor")
	}

	templates.ExecuteTemplate(w, "index.html", loggedIn)
}

func handleGame(w http.ResponseWriter, r *http.Request) {
	playerID, err := getPlayerIdFromSession(r)
	if err != nil {
		DebugLog("handleGame", "Redirecting anonymous visitor to index")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleGame: getOrCreateCurrentGame", err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}

	// Get player info from player table
	var player Player
	err = db.Get(&player, "SELECT rowid as id, name, secret_code FROM player WHERE rowid = ?", playerID)
	if err != nil {
		logError("handleGame: db.Get player", err)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	player.PlayerID = playerID
	DebugLog("handleGame", "Player '%s' (ID: %d) accessing game page, game %d status: '%s'", player.Name, playerID, game.ID, game.Status)

	// Check if player is in the game
	isInGame := false
	var gamePlayer Player
	err = db.Get(&gamePlayer, `SELECT g.rowid as id,
			r.rowid as role_id,
			r.name as role_name,
			r.description as role_description,
			r.team as team,
			g.is_alive as is_alive,
			is_observer as is_observer
		FROM game_player g
			JOIN role r on g.role_id = r.rowid
		WHERE g.game_id = ? AND g.player_id = ?`, game.ID, playerID)
	if err == nil {
		isInGame = true
		player.RoleId = gamePlayer.RoleId
		player.RoleName = gamePlayer.RoleName
		player.RoleDescription = gamePlayer.RoleDescription
		player.Team = gamePlayer.Team
		player.IsAlive = gamePlayer.IsAlive
		player.IsObserver = gamePlayer.IsObserver
	}

	// Get all players in the game
	players, err := getPlayersByGameId(game.ID)
	if err != nil {
		logError("handleGame: getPlayersByGameId", err)
	}

	buf, err := getGameComponent(playerID, game)
	if err != nil {
		logError("handleGame: getGameComponent", err)
	}

	data := GameData{
		Player:        &player,
		Players:       players,
		Game:          game,
		IsInGame:      isInGame,
		GameComponent: buf.String(),
	}

	templates.ExecuteTemplate(w, "game.html", data)
}

type SidebarData struct {
	Player              *Player
	Players             []Player
	Game                *Game
	LoverPartnerID      int64          // player_id of the viewer's lover, 0 if not a lover
	SeerFoundWerewolves map[int64]bool // player_ids the seer has confirmed as werewolves
}

// getSeerFoundWerewolves returns the set of player IDs that playerID (as Seer) has
// investigated and confirmed to be werewolves. Returns an empty map for non-seers.
func getSeerFoundWerewolves(gameID, playerID int64) map[int64]bool {
	found := map[int64]bool{}
	var targets []int64
	db.Select(&targets, `
		SELECT ga.target_player_id
		FROM game_action ga
		JOIN game_player gp ON gp.game_id = ga.game_id AND gp.player_id = ga.target_player_id
		JOIN role r ON r.rowid = gp.role_id
		WHERE ga.game_id = ? AND ga.actor_player_id = ? AND ga.action_type = ? AND r.team = 'werewolf'`,
		gameID, playerID, ActionSeerInvestigate)
	for _, t := range targets {
		found[t] = true
	}
	return found
}

func handleSidebarInfo(w http.ResponseWriter, r *http.Request) {
	playerID, err := getPlayerIdFromSession(r)
	if err != nil {
		DebugLog("handleGame", "Redirecting anonymous visitor to index")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleGame: getOrCreateCurrentGame", err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}

	// Get player info from player table
	var player Player
	err = db.Get(&player, "SELECT rowid as id, name, secret_code FROM player WHERE rowid = ?", playerID)
	if err != nil {
		logError("handleGame: db.Get player", err)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	player.PlayerID = playerID
	DebugLog("handleGame", "Player '%s' (ID: %d) accessing game page, game %d status: '%s'", player.Name, playerID, game.ID, game.Status)

	// Get all players in the game
	players, err := getPlayersByGameId(game.ID)
	if err != nil {
		logError("handleGame: getPlayersByGameId", err)
	}

	data := SidebarData{
		Player:              &player,
		Players:             players,
		Game:                game,
		LoverPartnerID:      getLoverPartner(game.ID, playerID),
		SeerFoundWerewolves: getSeerFoundWerewolves(game.ID, playerID),
	}

	templates.ExecuteTemplate(w, "sidebar.html", data)
}

func handleGameComponent(w http.ResponseWriter, r *http.Request) {
	playerID, err := getPlayerIdFromSession(r)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleGameComponent: getOrCreateCurrentGame", err)
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	buf, err := getGameComponent(playerID, game)
	if err != nil {
		logError("handleGameComponent: getGameComponent", err)
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	buf.WriteTo(w)
}

func handleGameHistory(w http.ResponseWriter, r *http.Request) {
	playerID, err := getPlayerIdFromSession(r)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleGameComponent: getOrCreateCurrentGame", err)
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	buf, err := getGameHistory(playerID, game)
	if err != nil {
		logError("handleGameHistory: getGameHistory", err)
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	buf.WriteTo(w)
}

func getGameHistory(playerID int64, game *Game) (*bytes.Buffer, error) {
	viewer, err := getPlayerInGame(game.ID, playerID)
	if err != nil {
		if err == sql.ErrNoRows {
			// Player not yet added to the game (race between HTTP request and WS connect).
			// Return empty history rather than an error.
			var buf bytes.Buffer
			if err := templates.ExecuteTemplate(&buf, "history.html", []string(nil)); err != nil {
				return nil, err
			}
			return &buf, nil
		}
		return nil, err
	}

	type historyRow struct {
		Description   string `db:"description"`
		Visibility    string `db:"visibility"`
		ActorPlayerID int64  `db:"actor_player_id"`
		Round         int    `db:"round"`
		Phase         string `db:"phase"`
	}

	var rows []historyRow
	db.Select(&rows, `
		SELECT description, visibility, actor_player_id, round, phase
		FROM game_action
		WHERE game_id = ? AND description != ''
		ORDER BY rowid ASC`, game.ID)

	var descriptions []string
	for _, row := range rows {
		action := GameAction{
			ActorPlayerID: row.ActorPlayerID,
			Visibility:    row.Visibility,
			Round:         row.Round,
			Phase:         row.Phase,
		}
		if canSeeAction(action, viewer, game.Round, game.Status) {
			descriptions = append(descriptions, row.Description)
		}
	}

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "history.html", descriptions); err != nil {
		return nil, err
	}

	return &buf, nil
}

func disableCaching(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Cache-Control", "no-cache")

		next.ServeHTTP(w, r)
	})
}

func handleWSMessage(client *Client, message []byte) {
	// Log incoming WebSocket message
	var playerName string
	db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", client.playerID)

	var msg WSMessage
	err := json.Unmarshal(message, &msg)
	if err != nil {
		log.Printf("WebSocket unmarshal error for player %d: %v", client.playerID, err)
		return
	}

	LogWSMessage("IN", playerName, msg.Action)

	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSMessage: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	// Route action to the appropriate handler based on action type and game status
	switch msg.Action {
	case "update_role":
		handleWSUpdateRole(client, msg)
	case "start_game":
		handleWSStartGame(client)
	case "werewolf_vote":
		handleWSWerewolfVote(client, msg)
	case "werewolf_vote_2":
		handleWSWerewolfVote2(client, msg)
	case "werewolf_pass":
		handleWSWerewolfPass(client, msg)
	case "werewolf_pass_2":
		handleWSWerewolfPass2(client, msg)
	case "werewolf_end_vote":
		handleWSWerewolfEndVote(client, msg)
	case "werewolf_end_vote_2":
		handleWSWerewolfEndVote2(client, msg)
	case "seer_investigate":
		handleWSSeerInvestigate(client, msg)
	case "doctor_protect":
		handleWSDoctorProtect(client, msg)
	case "guard_protect":
		handleWSGuardProtect(client, msg)
	case "day_vote":
		handleWSDayVote(client, msg)
	case "day_pass":
		handleWSDayPass(client, msg)
	case "day_end_vote":
		handleWSDayEndVote(client, msg)
	case "hunter_revenge":
		handleWSHunterRevenge(client, msg)
	case "witch_heal":
		handleWSWitchHeal(client, msg)
	case "witch_kill":
		handleWSWitchKill(client, msg)
	case "witch_pass":
		handleWSWitchPass(client, msg)
	case "cupid_choose":
		handleWSCupidChoose(client, msg)
	case "night_survey":
		handleWSNightSurvey(client, msg)
	case "new_game":
		handleWSNewGame(client)
	default:
		log.Printf("Unknown action: %s for player %d (%s) in game %d (status: %s)", msg.Action, client.playerID, playerName, game.ID, game.Status)
	}
}

// getGameComponent returns the HTML buffer for the current game state
func getGameComponent(playerID int64, game *Game) (*bytes.Buffer, error) {
	var buf bytes.Buffer

	players, err := getPlayersByGameId(game.ID)
	if err != nil {
		logError("getGameComponent: getPlayersByGameId", err)
		return nil, err
	}

	if game.Status == "lobby" {
		// Get role configuration
		var roleConfigs []GameRoleConfig
		db.Select(&roleConfigs, "SELECT rowid as id, game_id, role_id, count FROM game_role_config WHERE game_id = ?", game.ID)

		roleConfigMap := make(map[int64]int)
		totalRoles := 0
		for _, rc := range roleConfigs {
			roleConfigMap[rc.RoleID] = rc.Count
			totalRoles += rc.Count
		}

		var roleConfigDisplay []RoleConfigDisplay

		roles, err := getRoles()
		if err != nil {
			logError("getGameComponent: getRoles", err)
			return nil, err
		}

		for _, role := range roles {
			count := roleConfigMap[role.ID]
			roleConfigDisplay = append(roleConfigDisplay, RoleConfigDisplay{
				Role:  role,
				Count: count,
			})
		}

		data := LobbyData{
			Players:     players,
			RoleConfigs: roleConfigDisplay,
			TotalRoles:  totalRoles,
			PlayerCount: len(players),
			CanStart:    totalRoles > 0 && totalRoles == len(players),
			GameID:      game.ID,
			GameStatus:  game.Status,
		}

		if err := templates.ExecuteTemplate(&buf, "lobby_content.html", data); err != nil {
			logError("getGameComponent: ExecuteTemplate lobby_content", err)
			return nil, err
		}
	} else if game.Status == "night" {
		// Get the current player's info
		currentPlayer, err := getPlayerInGame(game.ID, playerID)
		if err != nil {
			logError("getGameComponent: getPlayerInGame", err)
			return nil, err
		}
		isAlive := currentPlayer.IsAlive

		isWerewolf := currentPlayer.Team == "werewolf"

		// Get werewolves (for werewolf players to see their team)
		var werewolves []Player
		if isWerewolf {
			for _, p := range players {
				if p.Team == "werewolf" && p.IsAlive {
					werewolves = append(werewolves, p)
				}
			}
		}

		// Get alive players as targets
		var aliveTargets []Player
		for _, p := range players {
			if p.IsAlive {
				aliveTargets = append(aliveTargets, p)
			}
		}

		// Get current votes for this night (werewolves only)
		var votes []WerewolfVote
		var currentVote int64

		if isWerewolf {
			var actions []GameAction
			db.Select(&actions, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`,
				game.ID, game.Round, ActionWerewolfKill)

			for _, action := range actions {
				var voterName, targetName string
				db.Get(&voterName, "SELECT name FROM player WHERE rowid = ?", action.ActorPlayerID)
				if action.TargetPlayerID != nil {
					db.Get(&targetName, "SELECT name FROM player WHERE rowid = ?", *action.TargetPlayerID)
					if action.ActorPlayerID == playerID {
						currentVote = *action.TargetPlayerID
					}
				}
				votes = append(votes, WerewolfVote{VoterName: voterName, TargetName: targetName})
			}
		}

		// Populate seer-specific data
		isSeer := currentPlayer.RoleName == "Seer"
		var hasInvestigated bool
		var seerResults []SeerResult

		if isSeer {
			// Only show the current night's investigation result
			var action GameAction
			err := db.Get(&action, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionSeerInvestigate)
			if err == nil && action.TargetPlayerID != nil {
				hasInvestigated = true
				var targetName string
				var targetTeam string
				db.Get(&targetName, "SELECT name FROM player WHERE rowid = ?", *action.TargetPlayerID)
				db.Get(&targetTeam, `
					SELECT r.team FROM game_player g JOIN role r ON g.role_id = r.rowid
					WHERE g.game_id = ? AND g.player_id = ?`,
					game.ID, *action.TargetPlayerID)
				seerResults = []SeerResult{{
					Round:      action.Round,
					TargetName: targetName,
					IsWerewolf: targetTeam == "werewolf",
				}}
			}
		}

		// Populate doctor-specific data
		isDoctor := currentPlayer.RoleName == "Doctor"
		var hasProtected bool
		var doctorProtecting string

		if isDoctor {
			var action GameAction
			err := db.Get(&action, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionDoctorProtect)
			if err == nil && action.TargetPlayerID != nil {
				hasProtected = true
				db.Get(&doctorProtecting, "SELECT name FROM player WHERE rowid = ?", *action.TargetPlayerID)
			}
		}

		// Populate guard-specific data
		isGuard := currentPlayer.RoleName == "Guard"
		var guardHasProtected bool
		var guardProtecting string
		var guardTargets []Player

		if isGuard {
			var action GameAction
			err := db.Get(&action, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionGuardProtect)
			if err == nil && action.TargetPlayerID != nil {
				guardHasProtected = true
				db.Get(&guardProtecting, "SELECT name FROM player WHERE rowid = ?", *action.TargetPlayerID)
			}

			// Build guard targets: alive players excluding self and last night's target
			var lastProtectedID int64
			if game.Round > 1 {
				var lastAction GameAction
				err := db.Get(&lastAction, `
					SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
					FROM game_action
					WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
					game.ID, game.Round-1, playerID, ActionGuardProtect)
				if err == nil && lastAction.TargetPlayerID != nil {
					lastProtectedID = *lastAction.TargetPlayerID
				}
			}
			for _, t := range aliveTargets {
				if t.PlayerID == playerID {
					continue // Cannot protect self
				}
				if lastProtectedID != 0 && t.PlayerID == lastProtectedID {
					continue // Cannot protect same player as last night
				}
				guardTargets = append(guardTargets, t)
			}
		}

		// Populate witch-specific data
		isWitch := currentPlayer.RoleName == "Witch"
		var healPotionUsed, poisonPotionUsed bool
		var witchHealedThisNight, witchKilledThisNight, witchDoneThisNight bool
		var witchKilledTarget string
		var witchVictim string
		var witchVictimID int64
		var witchVictim2 string
		var witchVictimID2 int64
		var witchHealedName string

		if isWitch {
			// Permanent potion usage (across all rounds)
			var healUsed, poisonUsed int
			db.Get(&healUsed, `
				SELECT COUNT(*) FROM game_action
				WHERE game_id = ? AND actor_player_id = ? AND action_type = ?`,
				game.ID, playerID, ActionWitchHeal)
			db.Get(&poisonUsed, `
				SELECT COUNT(*) FROM game_action
				WHERE game_id = ? AND actor_player_id = ? AND action_type = ?`,
				game.ID, playerID, ActionWitchKill)
			healPotionUsed = healUsed > 0
			poisonPotionUsed = poisonUsed > 0

			// This-night actions — get heal action to know who was healed
			var healAction GameAction
			if err := db.Get(&healAction, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night'
				AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionWitchHeal); err == nil {
				witchHealedThisNight = true
				if healAction.TargetPlayerID != nil {
					db.Get(&witchHealedName, "SELECT name FROM player WHERE rowid = ?", *healAction.TargetPlayerID)
				}
			}

			var killedAction GameAction
			err := db.Get(&killedAction, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night'
				AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionWitchKill)
			if err == nil && killedAction.TargetPlayerID != nil {
				witchKilledThisNight = true
				db.Get(&witchKilledTarget, "SELECT name FROM player WHERE rowid = ?", *killedAction.TargetPlayerID)
			}

			var doneCount int
			db.Get(&doneCount, `
				SELECT COUNT(*) FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night'
				AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionWitchPass)
			witchDoneThisNight = doneCount > 0

			// Find werewolf majority victim1
			type voteCount struct {
				TargetPlayerID int64  `db:"target_player_id"`
				TargetName     string `db:"target_name"`
				Count          int    `db:"count"`
			}
			var wvotes []voteCount
			db.Select(&wvotes, `
				SELECT ga.target_player_id, p.name as target_name, COUNT(*) as count
				FROM game_action ga
				JOIN player p ON ga.target_player_id = p.rowid
				WHERE ga.game_id = ? AND ga.round = ? AND ga.phase = 'night' AND ga.action_type = ?
				GROUP BY ga.target_player_id
				ORDER BY count DESC`,
				game.ID, game.Round, ActionWerewolfKill)

			var totalWerewolves int
			db.Get(&totalWerewolves, `
				SELECT COUNT(*) FROM game_player gp
				JOIN role r ON gp.role_id = r.rowid
				WHERE gp.game_id = ? AND gp.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

			if len(wvotes) > 0 && totalWerewolves > 0 {
				majority := totalWerewolves/2 + 1
				if wvotes[0].Count >= majority {
					witchVictim = wvotes[0].TargetName
					witchVictimID = wvotes[0].TargetPlayerID
				}
			}

			// Find Wolf Cub second kill victim2 (if active this night)
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
					var wvotes2 []voteCount
					db.Select(&wvotes2, `
						SELECT ga.target_player_id, p.name as target_name, COUNT(*) as count
						FROM game_action ga
						JOIN player p ON ga.target_player_id = p.rowid
						WHERE ga.game_id = ? AND ga.round = ? AND ga.phase = 'night' AND ga.action_type = ?
						GROUP BY ga.target_player_id
						ORDER BY count DESC`,
						game.ID, game.Round, ActionWerewolfKill2)
					majority := totalWerewolves/2 + 1
					if len(wvotes2) > 0 && wvotes2[0].Count >= majority {
						witchVictim2 = wvotes2[0].TargetName
						witchVictimID2 = wvotes2[0].TargetPlayerID
					}
				}
			}
		}

		isMason := currentPlayer.RoleName == "Mason"
		var masons []Player
		if isMason {
			for _, p := range players {
				if p.RoleName == "Mason" && p.IsAlive && p.PlayerID != currentPlayer.PlayerID {
					masons = append(masons, p)
				}
			}
		}

		// Populate Cupid-specific data
		isCupid := currentPlayer.RoleName == "Cupid" && game.Round == 1
		var cupidChosen1ID int64
		var cupidChosen1, cupidChosen2 string
		var cupidChosen2ID int64
		if isCupid {
			var finalized int
			db.Get(&finalized, `SELECT COUNT(*) FROM game_lovers WHERE game_id = ?`, game.ID)
			if finalized > 0 {
				// Both chosen — read from game_lovers
				db.Get(&cupidChosen1ID, `SELECT player1_id FROM game_lovers WHERE game_id = ? LIMIT 1`, game.ID)
				db.Get(&cupidChosen2ID, `SELECT player2_id FROM game_lovers WHERE game_id = ? LIMIT 1`, game.ID)
				db.Get(&cupidChosen1, "SELECT name FROM player WHERE rowid = ?", cupidChosen1ID)
				db.Get(&cupidChosen2, "SELECT name FROM player WHERE rowid = ?", cupidChosen2ID)
			} else {
				// Check if step 1 is done (stored in game_action)
				db.Get(&cupidChosen1ID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
					game.ID, playerID, ActionCupidLink)
				if cupidChosen1ID != 0 {
					db.Get(&cupidChosen1, "SELECT name FROM player WHERE rowid = ?", cupidChosen1ID)
				}
			}
		}

		// Lover info: any player who is in a finalized lover pair sees their partner
		loverPartnerID := getLoverPartner(game.ID, currentPlayer.PlayerID)
		isLover := loverPartnerID != 0
		var loverName string
		if isLover {
			db.Get(&loverName, "SELECT name FROM player WHERE rowid = ?", loverPartnerID)
		}

		// Check if Wolf Cub double kill is active this night
		wolfCubDoubleKill := false
		var currentVote2 int64
		if isWerewolf && game.Round > 1 {
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
					WHERE game_id = ? AND round = ? AND phase = 'night'
					AND actor_player_id = ? AND action_type = ?`,
					game.ID, game.Round, playerID, ActionWerewolfKill2)
				if err == nil && vote2Action.TargetPlayerID != nil {
					currentVote2 = *vote2Action.TargetPlayerID
				}
			}
		}

		// Populate End Vote / all-wolves-acted state
		var allWolvesActed, allWolvesActed2, wolfEndVoted, wolfEndVoted2 bool
		if isWerewolf {
			var werewolfCount int
			db.Get(&werewolfCount, `SELECT COUNT(*) FROM game_player gp JOIN role r ON gp.role_id = r.rowid WHERE gp.game_id = ? AND gp.is_alive = 1 AND r.team = 'werewolf'`, game.ID)
			var voted1 int
			db.Get(&voted1, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`, game.ID, game.Round, ActionWerewolfKill)
			allWolvesActed = voted1 >= werewolfCount

			var endVote1 int
			db.Get(&endVote1, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`, game.ID, game.Round, ActionWerewolfEndVote)
			wolfEndVoted = endVote1 > 0

			if wolfCubDoubleKill {
				var voted2 int
				db.Get(&voted2, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`, game.ID, game.Round, ActionWerewolfKill2)
				allWolvesActed2 = voted2 >= werewolfCount
				var endVote2 int
				db.Get(&endVote2, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ?`, game.ID, game.Round, ActionWerewolfEndVote2)
				wolfEndVoted2 = endVote2 > 0
			}
		}

		data := NightData{
			IsAlive:              isAlive,
			Players:              players,
			AliveTargets:         aliveTargets,
			IsWerewolf:           isWerewolf,
			Werewolves:           werewolves,
			Votes:                votes,
			CurrentVote:          currentVote,
			WolfCubDoubleKill:    wolfCubDoubleKill,
			CurrentVote2:         currentVote2,
			NightNumber:          game.Round,
			IsSeer:               isSeer,
			HasInvestigated:      hasInvestigated,
			SeerResults:          seerResults,
			IsDoctor:             isDoctor,
			HasProtected:         hasProtected,
			DoctorProtecting:     doctorProtecting,
			IsGuard:              isGuard,
			GuardHasProtected:    guardHasProtected,
			GuardProtecting:      guardProtecting,
			GuardTargets:         guardTargets,
			IsWitch:              isWitch,
			WitchVictim:          witchVictim,
			WitchVictimID:        witchVictimID,
			WitchVictim2:         witchVictim2,
			WitchVictimID2:       witchVictimID2,
			HealPotionUsed:       healPotionUsed,
			PoisonPotionUsed:     poisonPotionUsed,
			WitchHealedThisNight: witchHealedThisNight,
			WitchHealedName:      witchHealedName,
			WitchKilledThisNight: witchKilledThisNight,
			WitchKilledTarget:    witchKilledTarget,
			WitchDoneThisNight:   witchDoneThisNight,
			IsMason:              isMason,
			Masons:               masons,
			IsCupid:              isCupid,
			CupidChosen1ID:       cupidChosen1ID,
			CupidChosen1:         cupidChosen1,
			CupidChosen2ID:       cupidChosen2ID,
			CupidChosen2:         cupidChosen2,
			IsLover:              isLover,
			LoverName:            loverName,
			AllWolvesActed:       allWolvesActed,
			AllWolvesActed2:      allWolvesActed2,
			WolfEndVoted:         wolfEndVoted,
			WolfEndVoted2:        wolfEndVoted2,
		}

		// Survey: show once player has completed their night role action
		if isAlive && playerDoneWithNightAction(game.ID, game.Round, currentPlayer) {
			data.ShowSurvey = true
			var submitted int
			db.Get(&submitted, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=? AND actor_player_id=?`,
				game.ID, game.Round, ActionNightSurvey, currentPlayer.PlayerID)
			data.HasSubmittedSurvey = submitted > 0
			db.Get(&data.SurveyCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
				game.ID, game.Round, ActionNightSurvey)
			data.AliveCount = len(aliveTargets)
			data.SurveyTargets = aliveTargets
		}

		if err := templates.ExecuteTemplate(&buf, "night_content.html", data); err != nil {
			logError("getGameComponent: ExecuteTemplate night_content", err)
			return nil, err
		}
	} else if game.Status == "day" {
		// Get the current player's info
		currentPlayer, err := getPlayerInGame(game.ID, playerID)
		if err != nil {
			logError("getGameComponent: getPlayerInGame for day", err)
			return nil, err
		}

		// Find all players killed last night (werewolf kill, Wolf Cub second kill, witch poison, heartbreak)
		// Only includes players who are actually dead (protected players are excluded)
		var nightVictims []NightVictim
		db.Select(&nightVictims, `
			SELECT DISTINCT p.name as name, r.name as role
			FROM game_action ga
			JOIN player p ON ga.target_player_id = p.rowid
			JOIN game_player gp ON ga.target_player_id = gp.player_id AND gp.game_id = ga.game_id
			JOIN role r ON gp.role_id = r.rowid
			WHERE ga.game_id = ? AND ga.round = ? AND ga.phase = 'night'
			AND ga.action_type IN (?, ?, ?, ?)
			AND gp.is_alive = 0`,
			game.ID, game.Round, ActionWerewolfKill, ActionWerewolfKill2, ActionWitchKill, ActionLoverHeartbreak)

		// Get alive players as targets
		var aliveTargets []Player
		for _, p := range players {
			if p.IsAlive {
				aliveTargets = append(aliveTargets, p)
			}
		}

		// Populate Hunter revenge data — check for any dead Hunter who hasn't taken their revenge shot yet
		var hunterRevengeNeeded, hunterRevengeDone bool
		var hunterName, hunterVictim, hunterVictimRole string
		var isTheHunter bool
		var hunterTargets []Player

		// Step 1: Find a dead Hunter who hasn't taken revenge yet (pending — takes priority)
		for _, p := range players {
			if p.IsAlive || p.RoleName != "Hunter" {
				continue
			}
			var revengeCount int
			db.Get(&revengeCount, `
				SELECT COUNT(*) FROM game_action
				WHERE game_id = ? AND actor_player_id = ? AND action_type = ?`,
				game.ID, p.PlayerID, ActionHunterRevenge)
			if revengeCount == 0 {
				hunterRevengeNeeded = true
				hunterName = p.Name
				isTheHunter = (p.PlayerID == playerID)
				hunterTargets = aliveTargets
				break
			}
		}

		// Step 2: If no pending Hunter, check if any revenge happened this round (show result)
		if !hunterRevengeNeeded {
			var revengeAction GameAction
			err = db.Get(&revengeAction, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND action_type = ?
				ORDER BY rowid DESC LIMIT 1`,
				game.ID, game.Round, ActionHunterRevenge)
			if err == nil && revengeAction.TargetPlayerID != nil {
				hunterRevengeNeeded = true
				hunterRevengeDone = true
				db.Get(&hunterName, "SELECT name FROM player WHERE rowid = ?", revengeAction.ActorPlayerID)
				db.Get(&hunterVictim, "SELECT name FROM player WHERE rowid = ?", *revengeAction.TargetPlayerID)
				db.Get(&hunterVictimRole, `
					SELECT r.name FROM game_player g JOIN role r ON g.role_id = r.rowid
					WHERE g.game_id = ? AND g.player_id = ?`, game.ID, *revengeAction.TargetPlayerID)
				isTheHunter = (revengeAction.ActorPlayerID == playerID)
			}
		}

		// Get current votes for this day
		var votes []DayVote
		var currentVote int64

		var actions []GameAction
		db.Select(&actions, `
			SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
			FROM game_action
			WHERE game_id = ? AND round = ? AND phase = 'day' AND action_type = ?`,
			game.ID, game.Round, ActionDayVote)

		for _, action := range actions {
			var voterName, targetName string
			db.Get(&voterName, "SELECT name FROM player WHERE rowid = ?", action.ActorPlayerID)
			if action.TargetPlayerID != nil {
				db.Get(&targetName, "SELECT name FROM player WHERE rowid = ?", *action.TargetPlayerID)
				if action.ActorPlayerID == playerID {
					currentVote = *action.TargetPlayerID
				}
			}
			votes = append(votes, DayVote{VoterName: voterName, TargetName: targetName})
		}

		dayLoverPartnerID := getLoverPartner(game.ID, currentPlayer.PlayerID)
		isDayLover := dayLoverPartnerID != 0
		var dayLoverName string
		if isDayLover {
			db.Get(&dayLoverName, "SELECT name FROM player WHERE rowid = ?", dayLoverPartnerID)
		}

		// All-acted and has-voted checks for End Vote button
		var totalDayActed int
		db.Get(&totalDayActed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'day' AND action_type = ?`,
			game.ID, game.Round, ActionDayVote)
		var playerActed int
		db.Get(&playerActed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'day' AND action_type = ? AND actor_player_id = ?`,
			game.ID, game.Round, ActionDayVote, playerID)

		data := DayData{
			Players:             players,
			AliveTargets:        aliveTargets,
			NightNumber:         game.Round,
			NightVictims:        nightVictims,
			Votes:               votes,
			CurrentVote:         currentVote,
			IsAlive:             currentPlayer.IsAlive,
			HunterRevengeNeeded: hunterRevengeNeeded,
			HunterRevengeDone:   hunterRevengeDone,
			HunterName:          hunterName,
			HunterVictim:        hunterVictim,
			HunterVictimRole:    hunterVictimRole,
			IsTheHunter:         isTheHunter,
			HunterTargets:       hunterTargets,
			IsLover:             isDayLover,
			LoverName:           dayLoverName,
			AllActed:            totalDayActed >= len(aliveTargets),
			HasVoted:            playerActed > 0,
		}

		if err := templates.ExecuteTemplate(&buf, "day_content.html", data); err != nil {
			logError("getGameComponent: ExecuteTemplate day_content", err)
			return nil, err
		}
	} else if game.Status == "finished" {
		// Determine winner from game_action log (endGame stores the winner via status only,
		// so we infer from alive players — or "lovers" if a lover pair is the last two)
		var werewolfCount int
		db.Get(&werewolfCount, `
			SELECT COUNT(*) FROM game_player g
			JOIN role r ON g.role_id = r.rowid
			WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

		winner := "villagers"
		if werewolfCount > 0 {
			// Check if the alive players are a lover pair (lovers win)
			var alivePlayers []Player
			db.Select(&alivePlayers, `SELECT g.player_id as player_id FROM game_player g WHERE g.game_id = ? AND g.is_alive = 1`, game.ID)
			if len(alivePlayers) == 2 && getLoverPartner(game.ID, alivePlayers[0].PlayerID) == alivePlayers[1].PlayerID {
				winner = "lovers"
			} else {
				winner = "werewolves"
			}
		}

		data := FinishedData{
			Players: players,
			Winner:  winner,
		}

		if err := templates.ExecuteTemplate(&buf, "finished_content.html", data); err != nil {
			logError("getGameComponent: ExecuteTemplate finished_content", err)
			return nil, err
		}
	}

	return &buf, nil
}

func main() {
	dbPathFlag := flag.String("db", "file::memory:?cache=shared", "database path")
	flag.BoolVar(&devMode, "dev", false, "enable development mode (verbose logging, db dumps on error)")
	flag.Parse()

	// Set up logging to both stdout and file
	logFile, err := os.OpenFile("werewolf.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatal("Failed to open log file:", err)
	}
	defer logFile.Close()
	log.SetOutput(io.MultiWriter(os.Stdout, logFile))

	// Initialize application logger from environment variables
	logger, err := NewAppLoggerFromEnv()
	if err != nil {
		log.Fatal("Failed to initialize logger:", err)
	}
	appLogger = logger
	defer CloseAppLogger()

	if appLogger.IsEnabled() {
		log.Println("Extended logging enabled")
	}

	db, err = sqlx.Connect("sqlite3", *dbPathFlag)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	if err := initDB(); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	LogDBState("after initDB")

	initStoryteller()

	funcMap := template.FuncMap{
		"subtract": func(a, b int) int { return a - b },
	}
	templates, err = template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		log.Fatal("Failed to parse templates:", err)
	}

	// Start WebSocket hub
	go hub.run()

	// Wrap handlers with compression, caching control, and optional logging
	wrapHandler := func(pattern string, handler http.HandlerFunc) {
		var h http.Handler = handler
		// h = compress(h)
		h = disableCaching(h)
		if appLogger != nil && appLogger.logRequests {
			http.Handle(pattern, &LoggingHandler{Handler: h, Logger: appLogger})
		} else {
			http.Handle(pattern, h)
		}
	}

	wrapHandler("/", handleIndex)
	wrapHandler("/signup", handleSignup)
	wrapHandler("/login", handleLogin)
	wrapHandler("/logout", handleLogout)
	wrapHandler("/game", handleGame)
	wrapHandler("/ws", handleWebSocket)
	wrapHandler("/game/component", handleGameComponent)
	wrapHandler("/game/history", handleGameHistory)
	wrapHandler("/game/sidebar", handleSidebarInfo)

	// Serve static files with compression for text-based files (CSS, JS, SVG)
	// Binary formats like images will be served without compression
	staticHandler := http.FileServer(http.FS(staticFS))
	http.Handle("/static/", staticHandler)

	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
