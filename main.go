package main

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"flag"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
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

func handleCharacterInfo(w http.ResponseWriter, r *http.Request) {
	playerID, err := getPlayerIdFromSession(r)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleCharacterInfo: getOrCreateCurrentGame", err)
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	player, err := getPlayerInGame(game.ID, playerID)
	if err != nil {
		// Player not in game yet, return empty
		return
	}

	templates.ExecuteTemplate(w, "character_info.html", player)
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

func disableCaching(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Cache-Control", "no-cache")

		next.ServeHTTP(w, r)
	})
}

// gzipWriter wraps http.ResponseWriter to compress output
type gzipWriter struct {
	http.ResponseWriter
	Writer *gzip.Writer
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

func (w *gzipWriter) Flush() {
	w.Writer.Flush()
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// shouldCompress determines if a content type should be gzip compressed
// Compresses text-based formats but not binary formats like images
func shouldCompress(contentType string) bool {
	compressiblePrefixes := []string{
		"text/",
		"application/json",
		"application/javascript",
		"image/svg",
	}
	for _, prefix := range compressiblePrefixes {
		if strings.HasPrefix(contentType, prefix) {
			return true
		}
	}
	return false
}

// responseWriter wraps http.ResponseWriter to handle conditional gzip compression
type responseWriter struct {
	http.ResponseWriter
	gz            *gzip.Writer
	wrappedWriter http.ResponseWriter
	headerSent    bool
}

// WriteHeader checks content type and sets up compression if appropriate
func (w *responseWriter) WriteHeader(statusCode int) {
	if w.headerSent {
		return
	}
	w.headerSent = true

	contentType := w.Header().Get("Content-Type")
	acceptGzip := strings.Contains(w.Header().Get("Accept-Encoding"), "gzip")

	// Only compress if content type is compressible and client supports gzip
	if contentType != "" && shouldCompress(contentType) && acceptGzip {
		w.gz = gzip.NewWriter(w.wrappedWriter)
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
	}

	w.ResponseWriter.WriteHeader(statusCode)
}

// Write writes to gzip writer if it exists, otherwise to original writer
func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.headerSent {
		w.WriteHeader(http.StatusOK)
	}

	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Flush flushes both gzip and response writer
func (w *responseWriter) Flush() {
	if w.gz != nil {
		w.gz.Flush()
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Close closes the gzip writer if it exists
func (w *responseWriter) Close() error {
	if w.gz != nil {
		return w.gz.Close()
	}
	return nil
}

// compress adds gzip compression to compressible responses
func compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := &responseWriter{
			ResponseWriter: w,
			wrappedWriter:  w,
		}
		defer wrapped.Close()

		next.ServeHTTP(wrapped, r)
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
	case "seer_investigate":
		handleWSSeerInvestigate(client, msg)
	case "doctor_protect":
		handleWSDoctorProtect(client, msg)
	case "guard_protect":
		handleWSGuardProtect(client, msg)
	case "day_vote":
		handleWSDayVote(client, msg)
	case "hunter_revenge":
		handleWSHunterRevenge(client, msg)
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
				game.ID, game.NightNumber, ActionWerewolfKill)

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
				game.ID, game.NightNumber, playerID, ActionSeerInvestigate)
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
				game.ID, game.NightNumber, playerID, ActionDoctorProtect)
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
				game.ID, game.NightNumber, playerID, ActionGuardProtect)
			if err == nil && action.TargetPlayerID != nil {
				guardHasProtected = true
				db.Get(&guardProtecting, "SELECT name FROM player WHERE rowid = ?", *action.TargetPlayerID)
			}

			// Build guard targets: alive players excluding self and last night's target
			var lastProtectedID int64
			if game.NightNumber > 1 {
				var lastAction GameAction
				err := db.Get(&lastAction, `
					SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
					FROM game_action
					WHERE game_id = ? AND round = ? AND phase = 'night' AND actor_player_id = ? AND action_type = ?`,
					game.ID, game.NightNumber-1, playerID, ActionGuardProtect)
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

		data := NightData{
			Players:           players,
			AliveTargets:      aliveTargets,
			IsWerewolf:        isWerewolf,
			Werewolves:        werewolves,
			Votes:             votes,
			CurrentVote:       currentVote,
			NightNumber:       game.NightNumber,
			IsSeer:            isSeer,
			HasInvestigated:   hasInvestigated,
			SeerResults:       seerResults,
			IsDoctor:          isDoctor,
			HasProtected:      hasProtected,
			DoctorProtecting:  doctorProtecting,
			IsGuard:           isGuard,
			GuardHasProtected: guardHasProtected,
			GuardProtecting:   guardProtecting,
			GuardTargets:      guardTargets,
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

		// Find who died last night
		var lastVictim string
		var lastVictimRole string
		for _, p := range players {
			if !p.IsAlive {
				var count int
				db.Get(&count, `
					SELECT COUNT(*) FROM game_action
					WHERE game_id = ? AND round = ? AND phase = 'night' AND action_type = ? AND target_player_id = ?`,
					game.ID, game.NightNumber, ActionWerewolfKill, p.PlayerID)
				if count > 0 {
					lastVictim = p.Name
					lastVictimRole = p.RoleName
					break
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
				game.ID, game.NightNumber, ActionHunterRevenge)
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
			game.ID, game.NightNumber, ActionDayVote)

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

		data := DayData{
			Players:             players,
			AliveTargets:        aliveTargets,
			NightNumber:         game.NightNumber,
			LastNightVictim:     lastVictim,
			LastNightVictimRole: lastVictimRole,
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
		}

		if err := templates.ExecuteTemplate(&buf, "day_content.html", data); err != nil {
			logError("getGameComponent: ExecuteTemplate day_content", err)
			return nil, err
		}
	} else if game.Status == "finished" {
		// Determine winner
		var werewolfCount int
		db.Get(&werewolfCount, `
			SELECT COUNT(*) FROM game_player g
			JOIN role r ON g.role_id = r.rowid
			WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)

		winner := "villagers"
		if werewolfCount > 0 {
			winner = "werewolves"
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
		h = compress(h)
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
	wrapHandler("/game/character", handleCharacterInfo)

	// Serve static files with compression for text-based files (CSS, JS, SVG)
	// Binary formats like images will be served without compression
	staticHandler := compress(http.FileServer(http.FS(staticFS)))
	http.Handle("/static/", staticHandler)

	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
