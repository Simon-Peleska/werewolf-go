package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"html/template"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/gorilla/websocket"
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

// Toast represents a notification message to show to the user
type Toast struct {
	ID      string
	Type    string // "error", "warning", "success", "info"
	Message string
}

// logError logs an error with context and dumps the database in dev mode
func logError(context string, err error) {
	log.Printf("ERROR [%s]: %v", context, err)
	if devMode {
		rows, _ := db.Query(".dump")
		log.Printf("DB dump: %v", rows)
	}
}

// renderToast renders a toast notification HTML fragment
var toastCounter int64

func renderToast(toastType, message string) string {
	var buf bytes.Buffer
	toastCounter++
	toast := Toast{ID: strconv.FormatInt(toastCounter, 10), Type: toastType, Message: message}
	if err := templates.ExecuteTemplate(&buf, "toast.html", toast); err != nil {
		log.Printf("Failed to render toast: %v", err)
		return ""
	}
	return buf.String()
}

// sendErrorToast sends an error toast to a specific player via WebSocket
func sendErrorToast(playerID int64, message string) {
	html := renderToast("error", message)
	if html != "" {
		hub.sendToPlayer(playerID, []byte(html))
	}
}

type Game struct {
	ID          int64  `db:"id"`
	Status      string `db:"status"` // lobby, night, day, finished
	NightNumber int    `db:"night_number"`
}

type GameRoleConfig struct {
	ID     int64 `db:"id"`
	GameID int64 `db:"game_id"`
	RoleID int64 `db:"role_id"`
	Count  int   `db:"count"`
}

type Player struct {
	ID              int64  `db:"id"`
	GameID          int64  `db:"game_id"`
	PlayerID        int64  `db:"player_id"`
	Name            string `db:"name"`
	SecretCode      string `db:"secret_code"`
	RoleId          string `db:"role_id"`
	RoleName        string `db:"role_name"`
	RoleDescription string `db:"role_description"`
	Team            string `db:"team"`
	IsAlive         bool   `db:"is_alive"`
	IsObserver      bool   `db:"is_observer"`
}

func getPlayerInGame(gameID, playerID int64) (Player, error) {
	var player Player
	err := db.Get(&player, `SELECT g.rowid as id,
			g.game_id as game_id,
			g.player_id as player_id,
			p.name as name,
			p.secret_code as secret_code,
			r.rowid as role_id,
			r.name as role_name,
			r.description as role_description,
			r.team as team,
			g.is_alive as is_alive,
			g.is_observer as is_observer
		FROM game_player g
			JOIN player p on g.player_id = p.rowid
			JOIN role r on g.role_id = r.rowid
		WHERE g.game_id = ? AND g.player_id = ?`, gameID, playerID)
	return player, err
}

func getPlayersByGameId(id int64) ([]Player, error) {
	var players []Player
	err := db.Select(&players, `
		SELECT g.rowid as id,
			g.game_id as game_id,
			g.player_id as player_id,
			p.name as name,
			p.secret_code as secret_code,
			r.rowid as role_id,
			r.name as role_name,
			r.description as role_description,
			r.team as team,
			g.is_alive as is_alive,
			is_observer as is_observer
		FROM game_player g
			JOIN player p on g.player_id = p.rowid
			JOIN role r on g.role_id = r.rowid
		WHERE g.game_id = ?`, id)
	return players, err
}

func getPlayersByPlayerGameId(id int) (Player, error) {
	var players Player
	err := db.Select(&players, `
		SELECT g.rowid as id,
			g.game_id as game_id,
			g.player_id as player_id,
			p.name as name,
			p.secret_code as secret_code,
			r.rowid as role_id,
			r.name as role_name,
			r.description as role_description,
			r.team as team,
			g.is_alive as is_alive,
			is_observer as is_observer
		FROM game_player g
			JOIN player p on g.player_id = p.rowid
			JOIN role r on g.role_id = r.rowid
		WHERE g.rowid = ?`, id)
	return players, err
}

// Role definitions
type Role struct {
	ID          int64  `db:"id"`
	Name        string `db:"name"`
	Team        string `db:"team"`
	Description string `db:"description"`
}

func getRoles() ([]Role, error) {
	var roles []Role
	err := db.Select(&roles, `
		SELECT rowid as id,
			name,
			description,
			team
		FROM role
		`)
	return roles, err
}

func getRoleById(id int) (Role, error) {
	var role Role
	err := db.Select(&role, `
		SELECT rowid as id,
			name,
			description,
			team,
		FROM role
		WHERE id = ?
		`, id)
	return role, err
}

// WSMessage represents a message from the client
type WSMessage struct {
	Action         string `json:"action"`
	RoleID         string `json:"role_id,omitempty"`
	Delta          string `json:"delta,omitempty"`
	TargetPlayerID string `json:"target_player_id,omitempty"`
}

// GameAction represents any action taken during the game (night or day phase)
// Visibility determines who can see this action:
//   - "public": everyone can see
//   - "team:werewolf": only werewolf team can see
//   - "team:villager": only villager team can see
//   - "actor": only the actor can see
//   - "resolved": hidden until phase ends, then becomes public
type GameAction struct {
	ID             int64  `db:"id"`
	GameID         int64  `db:"game_id"`
	Round          int    `db:"round"`
	Phase          string `db:"phase"` // "night" or "day"
	ActorPlayerID  int64  `db:"actor_player_id"`
	ActionType     string `db:"action_type"`
	TargetPlayerID *int64 `db:"target_player_id"`
	Visibility     string `db:"visibility"`
}

// Action types
const (
	ActionWerewolfKill    = "werewolf_kill"
	ActionDayVote         = "day_vote"
	ActionElimination     = "elimination"
	ActionSeerInvestigate = "seer_investigate"
	ActionDoctorProtect   = "doctor_protect"
	ActionGuardProtect    = "guard_protect"
	ActionHunterRevenge   = "hunter_revenge"
)

// Visibility types
const (
	VisibilityPublic       = "public"
	VisibilityTeamWerewolf = "team:werewolf"
	VisibilityTeamVillager = "team:villager"
	VisibilityActor        = "actor"
	VisibilityResolved     = "resolved"
)

// canSeeAction determines if a player can see a specific action based on visibility rules
func canSeeAction(action GameAction, viewer Player, currentRound int, currentPhase string) bool {
	switch action.Visibility {
	case VisibilityPublic:
		return true
	case VisibilityTeamWerewolf:
		return viewer.Team == "werewolf"
	case VisibilityTeamVillager:
		return viewer.Team == "villager"
	case VisibilityActor:
		return viewer.PlayerID == action.ActorPlayerID
	case VisibilityResolved:
		// Visible once we're past the phase when action was created
		if action.Round < currentRound {
			return true
		}
		if action.Round == currentRound && action.Phase == "night" && currentPhase == "day" {
			return true
		}
		return false
	default:
		return false
	}
}

// getActionsForPlayer returns all actions a player is allowed to see for a specific round/phase
func getActionsForPlayer(gameID int64, viewer Player, currentRound int, currentPhase string, queryRound int, queryPhase string) ([]GameAction, error) {
	var allActions []GameAction
	err := db.Select(&allActions, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id = ? AND round = ? AND phase = ?`,
		gameID, queryRound, queryPhase)
	if err != nil {
		return nil, err
	}

	var visibleActions []GameAction
	for _, action := range allActions {
		if canSeeAction(action, viewer, currentRound, currentPhase) {
			visibleActions = append(visibleActions, action)
		}
	}
	return visibleActions, nil
}

// getVoteCounts returns vote counts for a specific phase
func getVoteCounts(gameID int64, round int, phase string, actionType string) (map[int64]int, int, error) {
	var actions []GameAction
	err := db.Select(&actions, `
		SELECT rowid as id, game_id, round, phase, actor_player_id, action_type, target_player_id, visibility
		FROM game_action
		WHERE game_id = ? AND round = ? AND phase = ? AND action_type = ?`,
		gameID, round, phase, actionType)
	if err != nil {
		return nil, 0, err
	}

	voteCounts := make(map[int64]int)
	for _, action := range actions {
		if action.TargetPlayerID != nil {
			voteCounts[*action.TargetPlayerID]++
		}
	}
	return voteCounts, len(actions), nil
}

// Client represents a websocket connection with player info
type Client struct {
	conn     *websocket.Conn
	playerID int64
}

// WebSocket hub for broadcasting updates to all connected clients
type Hub struct {
	clients    map[*websocket.Conn]*Client
	broadcast  chan []byte
	register   chan *Client
	unregister chan *websocket.Conn
	mu         sync.RWMutex
}

var hub = &Hub{
	clients:    make(map[*websocket.Conn]*Client),
	broadcast:  make(chan []byte),
	register:   make(chan *Client),
	unregister: make(chan *websocket.Conn),
}

func (h *Hub) sendToPlayer(playerID int64, message []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, client := range h.clients {
		if client.playerID == playerID {
			// Get player name for logging
			var playerName string
			db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)
			LogWSMessage("OUT", playerName, string(message))

			err := client.conn.WriteMessage(websocket.TextMessage, message)
			if err != nil {
				log.Printf("WebSocket write error to player %d: %v", playerID, err)
			}
		}
	}
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client.conn] = client
			h.mu.Unlock()
			var playerName string
			db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", client.playerID)
			log.Printf("WebSocket client connected (player %d: %s). Total: %d", client.playerID, playerName, len(h.clients))
			DebugLog("hub.register", "Player '%s' (ID: %d) connected via WebSocket", playerName, client.playerID)
			go addPlayerToLobby(client.playerID)

		case conn := <-h.unregister:
			h.mu.Lock()
			client, ok := h.clients[conn]
			if ok {
				playerID := client.playerID
				var playerName string
				db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)
				delete(h.clients, conn)
				conn.Close()

				// Check if player has any remaining connections
				hasOtherConn := false
				for _, c := range h.clients {
					if c.playerID == playerID {
						hasOtherConn = true
						break
					}
				}

				// If no connections left, remove from lobby
				if !hasOtherConn {
					DebugLog("hub.unregister", "Player '%s' (ID: %d) has no more connections, removing from lobby", playerName, playerID)
					go removePlayerFromLobby(playerID)
				} else {
					DebugLog("hub.unregister", "Player '%s' (ID: %d) still has other connections", playerName, playerID)
				}
			}
			h.mu.Unlock()
			log.Printf("WebSocket client disconnected. Total: %d", len(h.clients))

		case message := <-h.broadcast:
			h.mu.RLock()
			for conn := range h.clients {
				err := conn.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					log.Printf("WebSocket write error: %v", err)
					conn.Close()
					delete(h.clients, conn)
				}
			}
			h.mu.RUnlock()
		}
	}
}

func disableCaching(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Cache-Control", "no-cache")

		next.ServeHTTP(w, r)
	})
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

	// Wrap handlers with logging if enabled
	wrapHandler := func(pattern string, handler http.HandlerFunc) {
		h := disableCaching(http.HandlerFunc(handler))
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
	http.Handle("/static/", http.FileServer(http.FS(staticFS)))

	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func initDB() error {
	schema := `
	PRAGMA journal_mode=WAL;

	CREATE TABLE IF NOT EXISTS game (
		status TEXT NOT NULL DEFAULT 'lobby',
		night_number INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS player (
		name TEXT UNIQUE NOT NULL,
		secret_code TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS game_player (
		game_id INTEGER NOT NULL,
		player_id INTEGER NOT NULL,
		role_id INTEGER NOT NULL DEFAULT 1,
		is_alive INTEGER NOT NULL DEFAULT 1,
		is_observer INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY (game_id) REFERENCES game(id),
		FOREIGN KEY (player_id) REFERENCES players(id),
		UNIQUE(game_id, player_id)
	);
	CREATE TABLE IF NOT EXISTS role (
		name TEXT NOT NULL UNIQUE,
		description TEXT NOT NULL,
		team TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS game_role_config (
		game_id INTEGER NOT NULL,
		role_id INTEGER NOT NULL,
		count INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY (game_id) REFERENCES game(id),
		FOREIGN KEY (role_id) REFERENCES role(id),
		UNIQUE(game_id, role_id)
	);
	CREATE TABLE IF NOT EXISTS session (
		token INTEGER PRIMARY KEY,
		player_id INTEGER NOT NULL,
		FOREIGN KEY (player_id) REFERENCES player(rowid)
	);
	CREATE TABLE IF NOT EXISTS game_action (
		game_id INTEGER NOT NULL,
		round INTEGER NOT NULL,
		phase TEXT NOT NULL,
		actor_player_id INTEGER NOT NULL,
		action_type TEXT NOT NULL,
		target_player_id INTEGER,
		visibility TEXT NOT NULL DEFAULT 'public',
		FOREIGN KEY (game_id) REFERENCES game(rowid),
		FOREIGN KEY (actor_player_id) REFERENCES player(rowid),
		FOREIGN KEY (target_player_id) REFERENCES player(rowid),
		UNIQUE(game_id, round, phase, actor_player_id, action_type)
	);
	CREATE INDEX IF NOT EXISTS idx_game_action_lookup ON game_action(game_id, round, phase, visibility);

	INSERT OR IGNORE INTO role (name, description, team)
	VALUES
	  ('Villager', 'No special powers, relies on deduction and discussion.', 'villager'),
	  ('Werewolf', 'Knows other werewolves, votes to kill villagers at night.', 'werewolf'),
	  ('Seer', 'Can investigate one player per night to learn if they are a werewolf.', 'villager'),
	  ('Doctor', 'Can protect one player from werewolf attack each night.', 'villager'),
	  ('Witch', 'Has one heal potion and one poison potion to use during the game.', 'villager'),
	  ('Hunter', 'When eliminated, can immediately kill one player.', 'villager'),
	  ('Cupid', 'On night 1, chooses two players to become lovers.', 'villager'),
	  ('Guard', 'Protects one player per night, but not the same player twice in a row.', 'villager'),
	  ('Mason', 'Knows other masons, providing confirmed villagers.', 'villager'),
	  ('Wolf Cub', 'If eliminated, werewolves kill two victims the next night.', 'werewolf')
	`
	_, err := db.Exec(schema)
	if err != nil {
		log.Printf("initDB error: %v", err)
		return err
	}
	log.Printf("Database initialized successfully")
	return nil
}

func generateSecretCode() (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

const sessionCookieName = "werewolf_session"

func setSessionCookie(w http.ResponseWriter, playerID int64) {
	tokenBig, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	token := tokenBig.Int64()

	db.Exec("INSERT INTO session (token, player_id) VALUES (?, ?)", token, playerID)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    strconv.FormatInt(token, 10),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func getPlayerIdFromSession(r *http.Request) (int64, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return -1, err
	}

	token, err := strconv.ParseInt(cookie.Value, 10, 64)
	if err != nil {
		return -1, err
	}

	var playerID int64
	err = db.Get(&playerID, "SELECT player_id FROM session WHERE token = ?", token)
	if err != nil {
		return -1, err
	}

	return playerID, nil
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

func handleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Name is required")))
		return
	}

	var existing Player
	err := db.Get(&existing, "SELECT rowid as id, name, secret_code FROM player WHERE name = ?", name)
	if err == nil {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Name already taken. Use login with secret code if this is you.")))
		return
	}
	if err != sql.ErrNoRows {
		logError("handleSignup: db.Get player", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	secretCode, err := generateSecretCode()
	if err != nil {
		logError("handleSignup: generateSecretCode", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	result, err := db.Exec("INSERT INTO player (name, secret_code) VALUES (?, ?)", name, secretCode)
	if err != nil {
		logError("handleSignup: db.Exec insert player", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	playerID, _ := result.LastInsertId()
	log.Printf("New player created: name='%s', id=%d", name, playerID)
	DebugLog("handleSignup", "Player '%s' signed up with ID %d", name, playerID)
	LogDBState("after signup: " + name)

	setSessionCookie(w, playerID)
	w.Header().Set("HX-Redirect", "/game")
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.FormValue("name")
	secretCode := r.FormValue("secret_code")

	if name == "" || secretCode == "" {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Name and secret code are required")))
		return
	}

	var player Player
	err := db.Get(&player, "SELECT rowid as id, name, secret_code FROM player WHERE name = ? AND secret_code = ?", name, secretCode)
	if err == sql.ErrNoRows {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Invalid name or secret code")))
		return
	}
	if err != nil {
		logError("handleLogin: db.Get player", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	log.Printf("Player logged in: name='%s', id=%d", name, player.ID)
	DebugLog("handleLogin", "Player '%s' logged in with ID %d", name, player.ID)
	setSessionCookie(w, player.ID)
	w.Header().Set("HX-Redirect", "/game")
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	playerID, _ := getPlayerIdFromSession(r)
	var playerName string
	db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)

	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		token, _ := strconv.ParseInt(cookie.Value, 10, 64)
		db.Exec("DELETE FROM session WHERE token = ?", token)
	}

	log.Printf("Player logged out: name='%s', id=%d", playerName, playerID)
	DebugLog("handleLogout", "Player '%s' (ID: %d) logged out", playerName, playerID)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	playerID, err := getPlayerIdFromSession(r)
	if err != nil {
		DebugLog("handleWebSocket", "Rejected WebSocket connection - not logged in")
		http.Error(w, "Not logged in", http.StatusUnauthorized)
		return
	}

	var playerName string
	db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)
	DebugLog("handleWebSocket", "Player '%s' (ID: %d) initiating WebSocket connection", playerName, playerID)

	var upgrader = websocket.Upgrader{
		// CheckOrigin: func(r *http.Request) bool {
		// 	return true // Allow all origins for local development
		// },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error for player %d (%s): %v", playerID, playerName, err)
		return
	}

	DebugLog("handleWebSocket", "WebSocket upgraded successfully for player '%s' (ID: %d)", playerName, playerID)
	client := &Client{conn: conn, playerID: playerID}
	hub.register <- client

	// Handle messages and disconnection
	go func() {
		defer func() {
			hub.unregister <- conn
		}()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				break
			}
			handleWSMessage(client, message)
		}
	}()
}

func handleWSMessage(client *Client, message []byte) {
	// Log incoming WebSocket message
	var playerName string
	db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", client.playerID)
	LogWSMessage("IN", playerName, string(message))

	var msg WSMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Printf("Failed to parse WebSocket message: %v", err)
		return
	}

	log.Printf("WebSocket message from player %d (%s): action=%s", client.playerID, playerName, msg.Action)

	switch msg.Action {
	case "update_role":
		handleWSUpdateRole(client, msg)
	case "start_game":
		handleWSStartGame(client)
	case "werewolf_vote":
		handleWSWerewolfVote(client, msg)
	case "day_vote":
		handleWSDayVote(client, msg)
	case "seer_investigate":
		handleWSSeerInvestigate(client, msg)
	case "doctor_protect":
		handleWSDoctorProtect(client, msg)
	case "guard_protect":
		handleWSGuardProtect(client, msg)
	case "hunter_revenge":
		handleWSHunterRevenge(client, msg)
	default:
		log.Printf("Unknown WebSocket action: %s", msg.Action)
	}
}

func handleWSUpdateRole(client *Client, msg WSMessage) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSUpdateRole: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "lobby" {
		log.Printf("Cannot update roles: game status is '%s', expected 'lobby'", game.Status)
		sendErrorToast(client.playerID, "Cannot update roles: game already started")
		return
	}

	roleID := msg.RoleID
	delta := msg.Delta

	// Get current count
	var current GameRoleConfig
	err = db.Get(&current, "SELECT rowid as id, game_id, role_id, count FROM game_role_config WHERE game_id = ? AND role_id = ?", game.ID, roleID)

	if err == sql.ErrNoRows {
		if delta == "1" {
			db.Exec("INSERT INTO game_role_config (game_id, role_id, count) VALUES (?, ?, 1)", game.ID, roleID)
			DebugLog("handleWSUpdateRole", "Added role %s to game %d (count: 1)", roleID, game.ID)
		}
	} else if err == nil {
		newCount := current.Count
		if delta == "1" {
			newCount++
		} else if delta == "-1" && newCount > 0 {
			newCount--
		}
		if newCount > 0 {
			db.Exec("UPDATE game_role_config SET count = ? WHERE rowid = ?", newCount, current.ID)
			DebugLog("handleWSUpdateRole", "Updated role %s count to %d for game %d", roleID, newCount, game.ID)
		} else {
			db.Exec("DELETE FROM game_role_config WHERE rowid = ?", current.ID)
			DebugLog("handleWSUpdateRole", "Removed role %s from game %d", roleID, game.ID)
		}
	}

	LogDBState("after role update")
	broadcastGameUpdate()
}

func handleWSStartGame(client *Client) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSStartGame: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	log.Printf("Starting game: id=%d, status='%s'", game.ID, game.Status)

	if game.Status != "lobby" {
		log.Printf("Cannot start: game status is '%s', expected 'lobby'", game.Status)
		sendErrorToast(client.playerID, "Game already started")
		return
	}

	// Get players
	players, err := getPlayersByGameId(game.ID)
	if err != nil {
		logError("handleWSStartGame: getPlayersByGameId", err)
		sendErrorToast(client.playerID, "Failed to get players")
		return
	}
	log.Printf("Found %d players in game", len(players))

	// Get role configuration
	var roleConfigs []GameRoleConfig
	err = db.Select(&roleConfigs, "SELECT rowid as id, game_id, role_id, count FROM game_role_config WHERE game_id = ?", game.ID)
	if err != nil {
		logError("handleWSStartGame: db.Select roleConfigs", err)
		sendErrorToast(client.playerID, "Failed to get role configuration")
		return
	}
	log.Printf("Found %d role configs", len(roleConfigs))

	// Build role pool
	var rolePool []int64
	for _, rc := range roleConfigs {
		for i := 0; i < rc.Count; i++ {
			rolePool = append(rolePool, rc.RoleID)
		}
	}
	log.Printf("Role pool size: %d", len(rolePool))

	if len(rolePool) != len(players) {
		log.Printf("Cannot start: role count (%d) != player count (%d)", len(rolePool), len(players))
		sendErrorToast(client.playerID, "Role count must match player count")
		return
	}

	// Shuffle role pool
	shuffleRoles(rolePool)
	log.Printf("Roles shuffled, assigning to players...")

	// Assign roles to players
	for i, gp := range players {
		log.Printf("Assigning role %d to player %d (game_player id=%d)", rolePool[i], gp.PlayerID, gp.ID)
		_, err := db.Exec("UPDATE game_player SET role_id = ? WHERE rowid = ?", rolePool[i], gp.ID)
		if err != nil {
			logError("handleWSStartGame: db.Exec assign role", err)
			sendErrorToast(client.playerID, "Failed to assign roles")
			return
		}
	}
	log.Printf("Roles assigned, updating game status...")

	// Update game status and set night 1
	_, err = db.Exec("UPDATE game SET status = 'night', night_number = 1 WHERE rowid = ?", game.ID)
	if err != nil {
		logError("handleWSStartGame: db.Exec update game status", err)
		sendErrorToast(client.playerID, "Failed to start game")
		return
	}
	log.Printf("Game status updated to 'night' (night 1), broadcasting...")
	DebugLog("handleWSStartGame", "Game %d started, transitioning to night phase (night 1)", game.ID)
	LogDBState("after game start")

	broadcastGameUpdate()
	log.Printf("Game started successfully!")
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

// transitionToNight moves the game to the next night phase
func transitionToNight(game *Game) {
	newRound := game.NightNumber + 1
	_, err := db.Exec("UPDATE game SET status = 'night', night_number = ? WHERE rowid = ?", newRound, game.ID)
	if err != nil {
		logError("transitionToNight: update game", err)
		return
	}

	log.Printf("Day %d ended, transitioning to night %d", game.NightNumber, newRound)
	DebugLog("transitionToNight", "Day %d ended, transitioning to night %d", game.NightNumber, newRound)
	LogDBState("after day resolution")

	broadcastGameUpdate()
}

// checkWinConditions checks if the game has ended and returns true if so
func checkWinConditions(game *Game) bool {
	var werewolfCount, villagerCount int
	err := db.Get(&werewolfCount, `
		SELECT COUNT(*) FROM game_player g
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)
	if err != nil {
		logError("checkWinConditions: count werewolves", err)
		return false
	}

	err = db.Get(&villagerCount, `
		SELECT COUNT(*) FROM game_player g
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'villager'`, game.ID)
	if err != nil {
		logError("checkWinConditions: count villagers", err)
		return false
	}

	log.Printf("Win check: %d werewolves, %d villagers alive", werewolfCount, villagerCount)

	// Villagers win if all werewolves are dead
	if werewolfCount == 0 {
		log.Printf("VILLAGERS WIN - all werewolves eliminated")
		endGame(game, "villagers")
		return true
	}

	// Werewolves win if all villagers are dead
	if villagerCount == 0 {
		log.Printf("WEREWOLVES WIN - all villagers eliminated")
		endGame(game, "werewolves")
		return true
	}

	return false
}

// endGame marks the game as finished with a winner
func endGame(game *Game, winner string) {
	_, err := db.Exec("UPDATE game SET status = 'finished' WHERE rowid = ?", game.ID)
	if err != nil {
		logError("endGame: update game status", err)
		return
	}

	log.Printf("Game %d finished, winner: %s", game.ID, winner)
	DebugLog("endGame", "Game %d finished, winner: %s", game.ID, winner)
	LogDBState("after game end")

	broadcastGameUpdate()
}

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

// addPlayerToLobby adds a player to the game if it's in lobby state
func addPlayerToLobby(playerID int64) {
	var playerName string
	db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)

	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("addPlayerToLobby: getOrCreateCurrentGame", err)
		return
	}

	if game.Status != "lobby" {
		DebugLog("addPlayerToLobby", "Player '%s' (ID: %d) cannot join - game status is '%s'", playerName, playerID, game.Status)
		return
	}

	result, err := db.Exec("INSERT OR IGNORE INTO game_player (game_id, player_id) VALUES (?, ?)", game.ID, playerID)
	if err != nil {
		logError("addPlayerToLobby: db.Exec insert", err)
		return
	}

	rows, _ := result.RowsAffected()
	if rows > 0 {
		log.Printf("Player %d (%s) added to lobby (connected)", playerID, playerName)
		DebugLog("addPlayerToLobby", "Player '%s' (ID: %d) joined game %d lobby", playerName, playerID, game.ID)
		LogDBState("after player join: " + playerName)
		broadcastGameUpdate()
	} else {
		DebugLog("addPlayerToLobby", "Player '%s' (ID: %d) already in game %d", playerName, playerID, game.ID)
	}
}

// removePlayerFromLobby removes a player from the game if it's still in lobby state
func removePlayerFromLobby(playerID int64) {
	var playerName string
	db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)

	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("removePlayerFromLobby: getOrCreateCurrentGame", err)
		return
	}

	if game.Status != "lobby" {
		DebugLog("removePlayerFromLobby", "Player '%s' (ID: %d) cannot leave - game status is '%s'", playerName, playerID, game.Status)
		return
	}

	_, err = db.Exec("DELETE FROM game_player WHERE game_id = ? AND player_id = ?", game.ID, playerID)
	if err != nil {
		logError("removePlayerFromLobby: db.Exec delete", err)
		return
	}

	log.Printf("Player %d (%s) removed from lobby (disconnected)", playerID, playerName)
	DebugLog("removePlayerFromLobby", "Player '%s' (ID: %d) left game %d lobby", playerName, playerID, game.ID)
	LogDBState("after player leave: " + playerName)
	broadcastGameUpdate()
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
		templates.ExecuteTemplate(&charBuf, "character_info.html", p)
		hub.sendToPlayer(p.PlayerID, charBuf.Bytes())
	}
}

// getOrCreateCurrentGame returns the current waiting game, or creates one if none exists
func getOrCreateCurrentGame() (*Game, error) {
	var game Game
	err := db.Get(&game, "SELECT rowid as id, status, night_number FROM game ORDER BY id DESC LIMIT 1")
	if err == sql.ErrNoRows {
		result, err := db.Exec("INSERT INTO game (status, night_number) VALUES ('lobby', 0)")
		if err != nil {
			return nil, err
		}
		gameID, _ := result.LastInsertId()
		game = Game{ID: gameID, Status: "lobby", NightNumber: 0}
		log.Printf("Created new game: id=%d, status='lobby'", gameID)
		DebugLog("getOrCreateCurrentGame", "Created new game %d", gameID)
		LogDBState("after new game created")
	} else if err != nil {
		return nil, err
	}
	return &game, nil
}

// LobbyData holds all data needed to render the lobby
type LobbyData struct {
	Players     []Player
	RoleConfigs []RoleConfigDisplay
	TotalRoles  int
	PlayerCount int
	CanStart    bool
	GameID      int64
	GameStatus  string
}

type RoleConfigDisplay struct {
	Role  Role
	Count int
}

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

// DayVote represents a player's vote during the day
type DayVote struct {
	VoterName  string
	TargetName string
}

// DayData holds all data needed to render the day phase
type DayData struct {
	Players             []Player
	AliveTargets        []Player
	NightNumber         int
	LastNightVictim     string
	LastNightVictimRole string
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

// FinishedData holds all data needed to render the finished game screen
type FinishedData struct {
	Players []Player
	Winner  string // "villagers" or "werewolves"
}

type GameData struct {
	Player        *Player
	Players       []Player
	Game          *Game
	IsInGame      bool
	GameComponent string
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

// shuffleRoles shuffles the role pool using crypto/rand
func shuffleRoles(roles []int64) {
	for i := len(roles) - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			// Fallback: just swap with previous element
			roles[i], roles[i-1] = roles[i-1], roles[i]
			continue
		}
		j := int(jBig.Int64())
		roles[i], roles[j] = roles[j], roles[i]
	}
}
