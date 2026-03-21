package main

import (
	"bytes"
	"database/sql"
	"embed"
	"encoding/json"
	"flag"
	"sync"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var devMode bool

// App holds per-server resources to enable full isolation between tests.
type App struct {
	db           *sqlx.DB
	templates    *template.Template
	hubs         map[string]*Hub
	hubsMu       sync.RWMutex
	storyteller  Storyteller
	narrator     Narrator
	endingPrompt string
	logf         func(format string, args ...any) // log.Printf in prod, t.Logf in tests
}

func (app *App) getOrCreateHub(gameName string) *Hub {
	app.hubsMu.RLock()
	h, ok := app.hubs[gameName]
	app.hubsMu.RUnlock()
	if ok {
		return h
	}
	app.hubsMu.Lock()
	defer app.hubsMu.Unlock()
	if h, ok = app.hubs[gameName]; ok {
		return h
	}
	h = newHub(app.db, app.templates, app.storyteller, app.narrator, gameName)
	h.endingPrompt = app.endingPrompt
	go h.run()
	app.hubs[gameName] = h
	return h
}

type GameData struct {
	Player        *Player
	Players       []Player
	Game          *Game
	GameName      string
	IsInGame      bool
	GameComponent template.HTML
	SidebarHTML   template.HTML
	HistoryHTML   template.HTML
	Theme         string
}

// gameTheme returns the correct CSS data-theme for the initial page render.
// Day phase and villagers-win finished state use "light"; everything else is "dark".
func gameTheme(db *sqlx.DB, game *Game) string {
	if game.Status == "day" {
		return "light"
	}
	if game.Status == "finished" {
		var werewolfCount int
		db.Get(&werewolfCount, `
			SELECT COUNT(*) FROM game_player g
			JOIN role r ON g.role_id = r.rowid
			WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)
		if werewolfCount == 0 {
			return "light"
		}
	}
	return "dark"
}

func (app *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	playerID, err := getPlayerIdFromSession(app.db, r)
	loggedIn := err == nil && playerID > 0

	if loggedIn {
		var playerName string
		app.db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)
		DebugLog("handleIndex", "Page accessed by logged-in player '%s' (ID: %d)", playerName, playerID)
	} else {
		DebugLog("handleIndex", "Page accessed by anonymous visitor")
	}

	app.templates.ExecuteTemplate(w, "index.html", loggedIn)
}

func (app *App) handleGame(w http.ResponseWriter, r *http.Request) {
	playerID, err := getPlayerIdFromSession(app.db, r)
	if err != nil {
		DebugLog("handleGame", "Redirecting anonymous visitor to index")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	gameName := r.PathValue("name")
	hub := app.getOrCreateHub(gameName)

	game, err := getOrCreateGameByName(app.db, gameName)
	if err != nil {
		hub.logError("handleGame: getOrCreateGameByName", err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}

	// Get player info from player table
	var player Player
	err = app.db.Get(&player, "SELECT rowid as id, name, secret_code FROM player WHERE rowid = ?", playerID)
	if err != nil {
		hub.logError("handleGame: db.Get player", err)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	player.PlayerID = playerID
	DebugLog("handleGame", "Player '%s' (ID: %d) accessing game page, game %d status: '%s'", player.Name, playerID, game.ID, game.Status)

	// In lobby: add this player to the game now so the inline sidebar includes them immediately,
	// without waiting for the WebSocket to register. Trigger a broadcast so already-connected
	// clients (other players) see the new player. INSERT OR IGNORE is a no-op if already present.
	if game.Status == "lobby" {
		result, _ := app.db.Exec("INSERT OR IGNORE INTO game_player (game_id, player_id) VALUES (?, ?)", game.ID, playerID)
		if rows, _ := result.RowsAffected(); rows > 0 {
			hub.triggerBroadcast()
		}
	}

	// Check if player is in the game
	isInGame := false
	var gamePlayer Player
	err = app.db.Get(&gamePlayer, `SELECT g.rowid as id,
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
	players, err := getPlayersByGameId(app.db, game.ID)
	if err != nil {
		hub.logError("handleGame: getPlayersByGameId", err)
	}

	buf, err := getGameComponent(hub, playerID, game)
	if err != nil {
		hub.logError("handleGame: getGameComponent", err)
	}

	// Build sidebar HTML inline so the page is fully rendered before WebSocket connects.
	seerInvestigated := getSeerInvestigated(app.db, game.ID, playerID)
	visiblePlayers := applyCardVisibility(player, selfFirstPlayers(players, playerID), seerInvestigated)
	sidebarData := SidebarData{
		Player:         &player,
		Players:        visiblePlayers,
		Game:           game,
		LoverPartnerID: getLoverPartner(app.db, game.ID, playerID),
	}
	var sidebarBuf bytes.Buffer
	app.templates.ExecuteTemplate(&sidebarBuf, "sidebar.html", sidebarData)

	historyBuf, _ := getGameHistory(app.db, app.templates, playerID, game)

	data := GameData{
		Player:        &player,
		Players:       players,
		Game:          game,
		GameName:      gameName,
		IsInGame:      isInGame,
		GameComponent: template.HTML(buf.String()),
		SidebarHTML:   template.HTML(sidebarBuf.String()),
		HistoryHTML:   template.HTML(historyBuf.String()),
		Theme:         gameTheme(app.db, game),
	}

	app.templates.ExecuteTemplate(w, "game.html", data)
}

type SidebarData struct {
	Player         *Player
	Players        []Player
	Game           *Game
	LoverPartnerID int64 // player_id of the viewer's lover, 0 if not a lover
}

// selfFirstPlayers returns players sorted so the player with selfPlayerID is first.
func selfFirstPlayers(players []Player, selfPlayerID int64) []Player {
	out := make([]Player, 0, len(players))
	for _, p := range players {
		if p.PlayerID == selfPlayerID {
			out = append([]Player{p}, out...)
		} else {
			out = append(out, p)
		}
	}
	return out
}

// getSeerInvestigated returns a map of player_id → team ("werewolf" or "villager") for all
// players that playerID (as Seer) has investigated. Returns an empty map for non-seers.
func getSeerInvestigated(db *sqlx.DB, gameID, playerID int64) map[int64]string {
	type row struct {
		TargetID int64  `db:"target_player_id"`
		Team     string `db:"team"`
	}
	var rows []row
	db.Select(&rows, `
		SELECT ga.target_player_id, r.team
		FROM game_action ga
		JOIN game_player gp ON gp.game_id = ga.game_id AND gp.player_id = ga.target_player_id
		JOIN role r ON r.rowid = gp.role_id
		WHERE ga.game_id = ? AND ga.actor_player_id = ? AND ga.action_type = ?`,
		gameID, playerID, ActionSeerInvestigate)
	out := make(map[int64]string, len(rows))
	for _, r := range rows {
		out[r.TargetID] = r.Team
	}
	return out
}

// getVisiblePlayer fetches a player from the DB and applies canonical card visibility rules.
// Returns nil if the player cannot be found.
func getVisiblePlayer(db *sqlx.DB, gameID, targetPlayerID int64, viewer Player, seerInvestigated map[int64]string) *Player {
	p, err := getPlayerInGame(db, gameID, targetPlayerID)
	if err != nil {
		return nil
	}
	result := applyCardVisibility(viewer, []Player{p}, seerInvestigated)
	return &result[0]
}

// applyCardVisibility returns a copy of targets with role/team fields adjusted to show only
// what the viewer should see. This is the canonical visibility rule applied in all contexts.
//
// Rules (in priority order):
//  1. Dead → full role + team revealed
//  2. Self → full role + team visible
//  3. Viewer is Mason AND target is Mason → full role + team visible (masons know each other)
//  4. Viewer is werewolf AND target is werewolf → team only ("Werewolf"), no exact role
//  5. Seer has investigated this target → team only ("Werewolf" or "Villager"), no exact role
//  6. Otherwise → "Unknown"
func applyCardVisibility(viewer Player, targets []Player, seerInvestigated map[int64]string) []Player {
	out := make([]Player, len(targets))
	for i, t := range targets {
		p := t
		isSelf := viewer.PlayerID == t.PlayerID
		isMasonPair := viewer.RoleId == "mason" && t.RoleId == "mason"
		isWolfPair := viewer.Team == "werewolf" && t.Team == "werewolf"
		switch {
		case !t.IsAlive, isSelf, isMasonPair:
			// full role + team — keep as-is
		case isWolfPair:
			p.RoleName = "Werewolf"
			p.RoleDescription = ""
			p.Team = "werewolf"
		default:
			if team, ok := seerInvestigated[t.PlayerID]; ok {
				if team == "werewolf" {
					p.RoleName = "Werewolf"
				} else {
					p.RoleName = "Villager"
				}
				p.RoleDescription = ""
				p.Team = team
			} else {
				p.RoleName = "Unknown"
				p.RoleDescription = ""
				p.Team = "unknown"
			}
		}
		out[i] = p
	}
	return out
}

func getGameHistory(db *sqlx.DB, tmpl *template.Template, playerID int64, game *Game) (*bytes.Buffer, error) {
	viewer, err := getPlayerInGame(db, game.ID, playerID)
	if err != nil {
		if err == sql.ErrNoRows {
			// Player not yet added to the game (race between HTTP request and WS connect).
			// Return empty history rather than an error.
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "history.html", []string(nil)); err != nil {
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
	if err := tmpl.ExecuteTemplate(&buf, "history.html", descriptions); err != nil {
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
	client.hub.db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", client.playerID)

	var msg WSMessage
	err := json.Unmarshal(message, &msg)
	if err != nil {
		client.hub.logf("WebSocket unmarshal error for player %d: %v", client.playerID, err)
		return
	}

	LogWSMessage("IN", playerName, msg.Action)

	game, err := client.hub.getGame()
	if err != nil {
		client.hub.logError("handleWSMessage: getGame", err)
		client.hub.sendErrorToast(client.playerID, "Failed to get game")
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
	case "seer_select":
		handleWSSeerSelect(client, msg)
	case "seer_investigate":
		handleWSSeerInvestigate(client, msg)
	case "doctor_select":
		handleWSDoctorSelect(client, msg)
	case "doctor_protect":
		handleWSDoctorProtect(client, msg)
	case "guard_select":
		handleWSGuardSelect(client, msg)
	case "guard_protect":
		handleWSGuardProtect(client, msg)
	case "day_vote":
		handleWSDayVote(client, msg)
	case "day_pass":
		handleWSDayPass(client, msg)
	case "day_end_vote":
		handleWSDayEndVote(client, msg)
	case "hunter_select":
		handleWSHunterSelect(client, msg)
	case "hunter_revenge":
		handleWSHunterRevenge(client, msg)
	case "witch_select_heal":
		handleWSWitchSelectHeal(client, msg)
	case "witch_select_poison":
		handleWSWitchSelectPoison(client, msg)
	case "witch_apply":
		handleWSWitchApply(client, msg)
	case "cupid_choose":
		handleWSCupidChoose(client, msg)
	case "cupid_link":
		handleWSCupidLink(client)
	case "night_survey_suspect":
		handleWSNightSurveySuspect(client, msg)
	case "night_survey":
		handleWSNightSurvey(client, msg)
	case "new_game":
		client.hub.handleWSNewGame(client)
	default:
		client.hub.logf("Unknown action: %s for player %d (%s) in game %d (status: %s)", msg.Action, client.playerID, playerName, game.ID, game.Status)
	}
}

// getGameComponent returns the HTML buffer for the current game state
func getGameComponent(h *Hub, playerID int64, game *Game) (*bytes.Buffer, error) {
	db := h.db
	tmpl := h.templates
	var buf bytes.Buffer

	players, err := getPlayersByGameId(db, game.ID)
	if err != nil {
		h.logError("getGameComponent: getPlayersByGameId", err)
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

		roles, err := getRoles(db)
		if err != nil {
			h.logError("getGameComponent: getRoles", err)
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

		if err := tmpl.ExecuteTemplate(&buf, "lobby_content.html", data); err != nil {
			h.logError("getGameComponent: ExecuteTemplate lobby_content", err)
			return nil, err
		}
	} else if game.Status == "night" {
		// Get the current player's info
		player, err := getPlayerInGame(db, game.ID, playerID)
		if err != nil {
			h.logError("getGameComponent: getPlayerInGame", err)
			return nil, err
		}
		isAlive := player.IsAlive

		isWerewolf := player.Team == "werewolf"

		// Apply canonical card visibility rules. All player lists use the result.
		seerInvestigated := getSeerInvestigated(db, game.ID, playerID)
		visiblePlayers := applyCardVisibility(player, players, seerInvestigated)

		// Get alive players as targets (visibility pre-applied)
		var aliveTargets []Player
		for _, p := range visiblePlayers {
			if p.IsAlive {
				aliveTargets = append(aliveTargets, p)
			}
		}

		// Get current votes for this night (werewolves only)
		var votes []WerewolfVote
		werewolfVoteCounts := map[int64]int{}
		var currentVotePlayer *Player

		if isWerewolf {
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
			for _, r := range countRows {
				werewolfVoteCounts[r.TargetPlayerID] = r.Count
			}

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
						currentVotePlayer = getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated)
					}
				}
				votes = append(votes, WerewolfVote{VoterName: voterName, TargetName: targetName})
			}
		}

		// Populate seer-specific data
		isSeer := player.RoleName == "Seer"
		var hasInvestigated bool
		var seerSelectedPlayer *Player

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
				seerSelectedPlayer = getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated)
			} else if db.Get(&action, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
				game.ID, game.Round, playerID, ActionSeerSelect) == nil && action.TargetPlayerID != nil {
				seerSelectedPlayer = getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated)
			}
		}

		// Populate doctor-specific data
		isDoctor := player.RoleName == "Doctor"
		var hasProtected bool
		var doctorSelectedPlayer, doctorProtectingPlayer *Player

		if isDoctor {
			var action GameAction
			err := db.Get(&action, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionDoctorProtect)
			if err == nil && action.TargetPlayerID != nil {
				hasProtected = true
				doctorProtectingPlayer = getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated)
			} else {
				// Check for pending selection
				var selectAction GameAction
				if db.Get(&selectAction, `
					SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
					FROM game_action
					WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
					game.ID, game.Round, playerID, ActionDoctorSelect) == nil && selectAction.TargetPlayerID != nil {
					doctorSelectedPlayer = getVisiblePlayer(db, game.ID, *selectAction.TargetPlayerID, player, seerInvestigated)
				}
			}
		}

		// Populate guard-specific data
		isGuard := player.RoleName == "Guard"
		var guardHasProtected bool
		var guardSelectedPlayer, guardProtectingPlayer *Player
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
				guardProtectingPlayer = getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated)
			} else {
				// Check for pending selection
				var selectAction GameAction
				if db.Get(&selectAction, `
					SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
					FROM game_action
					WHERE game_id=? AND round=? AND phase='night' AND actor_player_id=? AND action_type=?`,
					game.ID, game.Round, playerID, ActionGuardSelect) == nil && selectAction.TargetPlayerID != nil {
					guardSelectedPlayer = getVisiblePlayer(db, game.ID, *selectAction.TargetPlayerID, player, seerInvestigated)
				}
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
		isWitch := player.RoleName == "Witch"
		var healPotionUsed, poisonPotionUsed bool
		var witchHealedThisNight, witchKilledThisNight, witchDoneThisNight bool
		var witchVictimID, witchVictimID2 int64
		var witchHealedPlayer, witchKilledPlayer *Player
		var witchVictimPlayer, witchVictimPlayer2 *Player
		var witchSelectedHealPlayer, witchSelectedPoisonPlayer *Player

		if isWitch {
			// Permanent potion usage (across all rounds — checks committed heal/kill)
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

			// Pending selections for this night (before Apply)
			var selectHealAction GameAction
			if err := db.Get(&selectHealAction, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night'
				AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionWitchSelectHeal); err == nil && selectHealAction.TargetPlayerID != nil {
				witchSelectedHealPlayer = getVisiblePlayer(db, game.ID, *selectHealAction.TargetPlayerID, player, seerInvestigated)
			}
			var selectPoisonAction GameAction
			if err := db.Get(&selectPoisonAction, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night'
				AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionWitchSelectPoison); err == nil && selectPoisonAction.TargetPlayerID != nil {
				witchSelectedPoisonPlayer = getVisiblePlayer(db, game.ID, *selectPoisonAction.TargetPlayerID, player, seerInvestigated)
			}

			// Committed actions this night (visible after Apply)
			var healAction GameAction
			if err := db.Get(&healAction, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night'
				AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionWitchHeal); err == nil {
				witchHealedThisNight = true
				if healAction.TargetPlayerID != nil {
					witchHealedPlayer = getVisiblePlayer(db, game.ID, *healAction.TargetPlayerID, player, seerInvestigated)
				}
			}
			var killedAction GameAction
			if err := db.Get(&killedAction, `
				SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
				FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night'
				AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionWitchKill); err == nil && killedAction.TargetPlayerID != nil {
				witchKilledThisNight = true
				witchKilledPlayer = getVisiblePlayer(db, game.ID, *killedAction.TargetPlayerID, player, seerInvestigated)
			}

			var doneCount int
			db.Get(&doneCount, `
				SELECT COUNT(*) FROM game_action
				WHERE game_id = ? AND round = ? AND phase = 'night'
				AND actor_player_id = ? AND action_type = ?`,
				game.ID, game.Round, playerID, ActionWitchApply)
			witchDoneThisNight = doneCount > 0

			// Witch only sees victims after werewolves have locked in their vote (End Vote pressed)
			var endVoteCount int
			db.Get(&endVoteCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
				game.ID, game.Round, ActionWerewolfEndVote)

			if endVoteCount > 0 {
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
						witchVictimID = wvotes[0].TargetPlayerID
						witchVictimPlayer = getVisiblePlayer(db, game.ID, witchVictimID, player, seerInvestigated)
					}
				}

				// Find Wolf Cub second kill victim2 — only after End Vote 2 is also pressed
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
								SELECT ga.target_player_id, p.name as target_name, COUNT(*) as count
								FROM game_action ga
								JOIN player p ON ga.target_player_id = p.rowid
								WHERE ga.game_id = ? AND ga.round = ? AND ga.phase = 'night' AND ga.action_type = ?
								GROUP BY ga.target_player_id
								ORDER BY count DESC`,
								game.ID, game.Round, ActionWerewolfKill2)
							majority := totalWerewolves/2 + 1
							if len(wvotes2) > 0 && wvotes2[0].Count >= majority {
								witchVictimID2 = wvotes2[0].TargetPlayerID
								witchVictimPlayer2 = getVisiblePlayer(db, game.ID, witchVictimID2, player, seerInvestigated)
							}
						}
					}
				}
			}
		}

		isMason := player.RoleName == "Mason"
		var masons []Player
		if isMason {
			for _, p := range players {
				if p.RoleName == "Mason" && p.IsAlive && p.PlayerID != player.PlayerID {
					masons = append(masons, p)
				}
			}
		}

		// Populate Cupid-specific data
		isCupid := player.RoleName == "Cupid" && game.Round == 1
		cupidLinked := false
		var cupidChosen1Player, cupidChosen2Player *Player
		if isCupid {
			var cupidChosen1ID, cupidChosen2ID int64
			var finalized int
			db.Get(&finalized, `SELECT COUNT(*) FROM game_lovers WHERE game_id = ?`, game.ID)
			if finalized > 0 {
				cupidLinked = true
				db.Get(&cupidChosen1ID, `SELECT player1_id FROM game_lovers WHERE game_id = ? LIMIT 1`, game.ID)
				db.Get(&cupidChosen2ID, `SELECT player2_id FROM game_lovers WHERE game_id = ? LIMIT 1`, game.ID)
			} else {
				db.Get(&cupidChosen1ID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
					game.ID, playerID, ActionCupidLink)
				db.Get(&cupidChosen2ID, `SELECT COALESCE(target_player_id, 0) FROM game_action WHERE game_id = ? AND round = 1 AND actor_player_id = ? AND action_type = ?`,
					game.ID, playerID, ActionCupidLink2)
			}
			if cupidChosen1ID != 0 {
				cupidChosen1Player = getVisiblePlayer(db, game.ID, cupidChosen1ID, player, seerInvestigated)
			}
			if cupidChosen2ID != 0 {
				cupidChosen2Player = getVisiblePlayer(db, game.ID, cupidChosen2ID, player, seerInvestigated)
			}
		}

		// Check if Wolf Cub double kill is active this night
		wolfCubDoubleKill := false
		var currentVotePlayer2 *Player
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
					currentVotePlayer2 = getVisiblePlayer(db, game.ID, *vote2Action.TargetPlayerID, player, seerInvestigated)
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
			Player:                    &player,
			AliveTargets:              aliveTargets,
			Votes:                     votes,
			WerewolfVoteCounts:        werewolfVoteCounts,
			CurrentVotePlayer:         currentVotePlayer,
			WolfCubDoubleKill:         wolfCubDoubleKill,
			CurrentVotePlayer2:        currentVotePlayer2,
			NightNumber:               game.Round,
			HasInvestigated:           hasInvestigated,
			SeerSelectedPlayer:        seerSelectedPlayer,
			HasProtected:              hasProtected,
			DoctorSelectedPlayer:      doctorSelectedPlayer,
			DoctorProtectingPlayer:    doctorProtectingPlayer,
			GuardHasProtected:         guardHasProtected,
			GuardSelectedPlayer:       guardSelectedPlayer,
			GuardProtectingPlayer:     guardProtectingPlayer,
			GuardTargets:              guardTargets,
			WitchVictimPlayer:         witchVictimPlayer,
			WitchVictimPlayer2:        witchVictimPlayer2,
			HealPotionUsed:            healPotionUsed,
			PoisonPotionUsed:          poisonPotionUsed,
			WitchSelectedHealPlayer:   witchSelectedHealPlayer,
			WitchSelectedPoisonPlayer: witchSelectedPoisonPlayer,
			WitchHealedThisNight:      witchHealedThisNight,
			WitchHealedPlayer:         witchHealedPlayer,
			WitchKilledThisNight:      witchKilledThisNight,
			WitchKilledPlayer:         witchKilledPlayer,
			WitchDoneThisNight:        witchDoneThisNight,
			Masons:                    masons,
			CupidLinked:               cupidLinked,
			CupidChosen1Player:        cupidChosen1Player,
			CupidChosen2Player:        cupidChosen2Player,
			AllWolvesActed:            allWolvesActed,
			AllWolvesActed2:           allWolvesActed2,
			WolfEndVoted:              wolfEndVoted,
			WolfEndVoted2:             wolfEndVoted2,
		}

		// Survey: show once player has completed their night role action
		if isAlive && playerDoneWithNightAction(db, game.ID, game.Round, player) {
			data.ShowSurvey = true
			var submitted int
			db.Get(&submitted, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=? AND actor_player_id=?`,
				game.ID, game.Round, ActionNightSurvey, player.PlayerID)
			data.HasSubmittedSurvey = submitted > 0
			db.Get(&data.SurveyCount, `SELECT COUNT(*) FROM game_action WHERE game_id=? AND round=? AND phase='night' AND action_type=?`,
				game.ID, game.Round, ActionNightSurvey)
			data.AliveCount = len(aliveTargets)
			data.SurveyTargets = aliveTargets
			var suspectPlayer Player
			if err := db.Get(&suspectPlayer, `
				SELECT gp.rowid as id, g.rowid as game_id, p.rowid as player_id, p.name, p.secret_code,
				       r.rowid as role_id, r.name as role_name, r.description as role_description, r.team,
				       gp.is_alive, gp.is_observer, IFNULL(l.player2_id, 0) as lover
				FROM game_action ga
				JOIN game_player gp ON gp.player_id = ga.target_player_id AND gp.game_id = ga.game_id
				JOIN player p ON p.rowid = gp.player_id
				JOIN game g ON g.rowid = gp.game_id
				JOIN role r ON r.rowid = gp.role_id
				LEFT JOIN game_lovers l ON l.player1_id = p.rowid
				WHERE ga.game_id=? AND ga.round=? AND ga.actor_player_id=? AND ga.action_type=?`,
				game.ID, game.Round, player.PlayerID, ActionNightSurveySuspect); err == nil {
				data.SurveySelectedSuspect = &suspectPlayer
			}
		}

		if err := tmpl.ExecuteTemplate(&buf, "night_content.html", data); err != nil {
			h.logError("getGameComponent: ExecuteTemplate night_content", err)
			return nil, err
		}
	} else if game.Status == "day" {
		// Get the current player's info
		player, err := getPlayerInGame(db, game.ID, playerID)
		if err != nil {
			h.logError("getGameComponent: getPlayerInGame for day", err)
			return nil, err
		}

		// Find all players killed last night (werewolf kill, Wolf Cub second kill, witch poison, heartbreak)
		// Only includes players who are actually dead (protected players are excluded)
		var nightVictims []Player
		db.Select(&nightVictims, `
			SELECT DISTINCT gp.rowid as id,
				g.rowid as game_id,
				p.rowid as player_id,
				p.name as name,
				p.secret_code as secret_code,
				r.rowid as role_id,
				r.name as role_name,
				r.description as role_description,
				r.team as team,
				gp.is_alive as is_alive,
				gp.is_observer as is_observer,
				IFNULL(l.player2_id, 0) as lover
			FROM game_player gp
				JOIN player p on gp.player_id = p.rowid
			    JOIN game_action ga ON ga.target_player_id = p.rowid
				JOIN game g on gp.game_id = g.rowid
				JOIN role r on gp.role_id = r.rowid
				LEFT JOIN game_lovers l on l.player1_id = p.rowid
			WHERE ga.game_id = ? AND ga.round = ? AND ga.phase = 'night'
			    AND ga.action_type IN (?, ?, ?, ?)
			    AND gp.is_alive = 0`,
			game.ID, game.Round, ActionWerewolfKill, ActionWerewolfKill2, ActionWitchKill, ActionLoverHeartbreak)

		// Apply visibility rules to all players from this viewer's perspective
		seerInvestigated := getSeerInvestigated(db, game.ID, playerID)
		visiblePlayers := applyCardVisibility(player, players, seerInvestigated)

		// Get alive players as targets (visibility pre-applied)
		var aliveTargets []Player
		for _, p := range visiblePlayers {
			if p.IsAlive {
				aliveTargets = append(aliveTargets, p)
			}
		}

		// Populate Hunter revenge data — check for any dead Hunter who hasn't taken their revenge shot yet
		var hunterRevengeNeeded, hunterRevengeDone bool
		var isTheHunter bool
		var hunterVictimPlayer, hunterSelectedPlayer *Player
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
				isTheHunter = (p.PlayerID == playerID)
				hunterTargets = aliveTargets
				// Check for pending selection from this hunter
				if isTheHunter {
					var selectAction GameAction
					if db.Get(&selectAction, `
						SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
						FROM game_action
						WHERE game_id=? AND round=? AND actor_player_id=? AND action_type=?`,
						game.ID, game.Round, playerID, ActionHunterSelect) == nil && selectAction.TargetPlayerID != nil {
						hunterSelectedPlayer = getVisiblePlayer(db, game.ID, *selectAction.TargetPlayerID, player, seerInvestigated)
					}
				}
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
				// HunterVictimPlayer: dead player, full role visible (dead rule in applyCardVisibility)
				hunterVictimPlayer = getVisiblePlayer(db, game.ID, *revengeAction.TargetPlayerID, player, seerInvestigated)
				isTheHunter = (revengeAction.ActorPlayerID == playerID)
			}
		}

		// Get current votes for this day
		var votes []DayVote
		dayVoteCounts := map[int64]int{}
		var currentVotePlayer *Player

		type voteCountRow struct {
			TargetPlayerID int64 `db:"target_player_id"`
			Count          int   `db:"count"`
		}
		var dayCountRows []voteCountRow
		db.Select(&dayCountRows, `
			SELECT target_player_id, COUNT(*) as count FROM game_action
			WHERE game_id=? AND round=? AND phase='day' AND action_type=? AND target_player_id IS NOT NULL
			GROUP BY target_player_id`,
			game.ID, game.Round, ActionDayVote)
		for _, r := range dayCountRows {
			dayVoteCounts[r.TargetPlayerID] = r.Count
		}

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
					currentVotePlayer = getVisiblePlayer(db, game.ID, *action.TargetPlayerID, player, seerInvestigated)
				}
			}
			votes = append(votes, DayVote{VoterName: voterName, TargetName: targetName})
		}

		// All-acted and has-voted checks for End Vote button
		var totalDayActed int
		db.Get(&totalDayActed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'day' AND action_type = ?`,
			game.ID, game.Round, ActionDayVote)
		var playerActed int
		db.Get(&playerActed, `SELECT COUNT(*) FROM game_action WHERE game_id = ? AND round = ? AND phase = 'day' AND action_type = ? AND actor_player_id = ?`,
			game.ID, game.Round, ActionDayVote, playerID)

		data := DayData{
			Player:               &player,
			AliveTargets:         aliveTargets,
			NightNumber:          game.Round,
			NightVictims:         nightVictims,
			Votes:                votes,
			DayVoteCounts:        dayVoteCounts,
			CurrentVotePlayer:    currentVotePlayer,
			HunterRevengeNeeded:  hunterRevengeNeeded,
			HunterRevengeDone:    hunterRevengeDone,
			HunterVictimPlayer:   hunterVictimPlayer,
			IsTheHunter:          isTheHunter,
			HunterSelectedPlayer: hunterSelectedPlayer,
			HunterTargets:        hunterTargets,
			AllActed:             totalDayActed >= len(aliveTargets),
			HasVoted:             playerActed > 0,
		}

		if err := tmpl.ExecuteTemplate(&buf, "day_content.html", data); err != nil {
			h.logError("getGameComponent: ExecuteTemplate day_content", err)
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
			if len(alivePlayers) == 2 && getLoverPartner(db, game.ID, alivePlayers[0].PlayerID) == alivePlayers[1].PlayerID {
				winner = "lovers"
			} else {
				winner = "werewolves"
			}
		}

		var winners, losers []Player
		for _, p := range players {
			isWinner := false
			switch winner {
			case "villagers":
				isWinner = p.Team == "villager"
			case "werewolves":
				isWinner = p.Team == "werewolf"
			case "lovers":
				isWinner = p.IsAlive
			}
			if isWinner {
				winners = append(winners, p)
			} else {
				losers = append(losers, p)
			}
		}

		data := FinishedData{
			Winners: winners,
			Losers:  losers,
			Winner:  winner,
		}

		if err := tmpl.ExecuteTemplate(&buf, "finished_content.html", data); err != nil {
			h.logError("getGameComponent: ExecuteTemplate finished_content", err)
			return nil, err
		}
	}

	return &buf, nil
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	fv := registerFlags()
	flag.Parse()

	cfg := loadConfig(*fv.configPath)
	fv.applyTo(&cfg)

	devMode = cfg.Dev

	// Set up logging to both stdout and file
	logFile, err := os.OpenFile("werewolf.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatal("Failed to open log file:", err)
	}
	defer logFile.Close()
	log.SetOutput(io.MultiWriter(os.Stdout, logFile))

	// Initialize application logger from config
	logger, err := NewAppLogger(cfg.toLogConfig())
	if err != nil {
		log.Fatal("Failed to initialize logger:", err)
	}
	appLogger = logger
	defer CloseAppLogger()

	if appLogger.IsEnabled() {
		log.Println("Extended logging enabled")
	}

	db, err := sqlx.Connect("sqlite3", cfg.DB)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	if err := initDB(db, log.Printf); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	LogDBState(db, "after initDB")

	storyteller := initStoryteller(cfg)
	narrator := initNarrator(cfg)

	funcMap := template.FuncMap{
		"subtract": func(a, b int) int { return a - b },
		// roleSeal maps a role name to its webp seal path, e.g. "Wolf Cub" → "/static/seals/Wolf_Cub.webp"
		"roleSeal": func(name string) string {
			return "/static/seals/" + strings.ReplaceAll(name, " ", "_") + ".webp"
		},
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		log.Fatal("Failed to parse templates:", err)
	}

	app := &App{
		db:           db,
		templates:    tmpl,
		hubs:         make(map[string]*Hub),
		storyteller:  storyteller,
		narrator:     narrator,
		endingPrompt: loadEndingPrompt(cfg),
		logf:         log.Printf,
	}

	// Wrap handlers with compression, caching control, and optional logging
	wrapHandler := func(pattern string, handler http.HandlerFunc) {
		var hh http.Handler = handler
		// hh = compress(hh)
		hh = disableCaching(hh)
		if appLogger != nil && appLogger.logRequests {
			http.Handle(pattern, &LoggingHandler{Handler: hh, Logger: appLogger})
		} else {
			http.Handle(pattern, hh)
		}
	}

	wrapHandler("/", app.handleIndex)
	wrapHandler("/signup", app.handleSignup)
	wrapHandler("/login", app.handleLogin)
	wrapHandler("/logout", app.handleLogout)
	wrapHandler("/game/{name}", app.handleGame)
	wrapHandler("/ws/{name}", func(w http.ResponseWriter, r *http.Request) {
		gameName := r.PathValue("name")
		hub := app.getOrCreateHub(gameName)
		handleWebSocket(hub, w, r)
	})
	wrapHandler("/player/upload-image", app.handleUploadPlayerImage)
	// Image endpoint: register directly (not via wrapHandler) to allow browser caching
	http.HandleFunc("/player-image/{imageID}", app.handlePlayerImage)

	// Serve static files with compression for text-based files (CSS, JS, SVG)
	// Binary formats like images will be served without compression
	staticHandler := http.FileServer(http.FS(staticFS))
	http.Handle("/static/", staticHandler)

	log.Printf("Server starting on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, nil))
}
