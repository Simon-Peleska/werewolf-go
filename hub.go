package main

import (
	"bytes"
	"database/sql"
	"html/template"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/jmoiron/sqlx"
)

// WSMessage represents a message from the client
type WSMessage struct {
	Action          string `json:"action"`
	RoleID          string `json:"role_id,omitempty"`
	Delta           string `json:"delta,omitempty"`
	TargetPlayerID  string `json:"target_player_id,omitempty"`
	SuspectPlayerID string `json:"suspect_player_id,omitempty"`
	DeathTheory     string `json:"death_theory,omitempty"`
	Notes           string `json:"notes,omitempty"`
}

const clientSendBuf = 64 // outbound message buffer per client

// hubMsg is a tagged WebSocket message (text or binary).
type hubMsg struct {
	binary bool
	data   []byte
}

// Client represents a websocket connection with player info
type Client struct {
	conn     *websocket.Conn
	playerID int64
	hub      *Hub
	send     chan hubMsg // buffered outbound messages; closed on disconnect
}

// writer drains the send channel and writes to the WebSocket connection.
// Runs in its own goroutine so slow clients never block the hub.
func (c *Client) writer() {
	defer c.hub.clientWg.Done()
	for msg := range c.send {
		mt := websocket.TextMessage
		if msg.binary {
			mt = websocket.BinaryMessage
		}
		if err := c.conn.WriteMessage(mt, msg.data); err != nil {
			log.Printf("WebSocket write error to player %d: %v", c.playerID, err)
			return
		}
	}
}

// WebSocket hub for broadcasting updates to all connected clients
type Hub struct {
	clients        map[*websocket.Conn]*Client
	broadcast      chan []byte
	register       chan *Client
	unregister     chan *websocket.Conn
	broadcastReqCh chan struct{} // coalescing signal for broadcastGameUpdate
	mu             sync.RWMutex
	done           chan struct{}
	wg             sync.WaitGroup
	clientWg       sync.WaitGroup // tracks active WebSocket reader goroutines

	db          *sqlx.DB
	templates   *template.Template
	storyteller Storyteller
	narrator    Narrator
}

func newHub(db *sqlx.DB, templates *template.Template, storyteller Storyteller, narrator Narrator) *Hub {
	return &Hub{
		clients:        make(map[*websocket.Conn]*Client),
		broadcast:      make(chan []byte),
		register:       make(chan *Client),
		unregister:     make(chan *websocket.Conn, 64),
		broadcastReqCh: make(chan struct{}, 1),
		done:           make(chan struct{}),
		db:             db,
		templates:      templates,
		storyteller:    storyteller,
		narrator:       narrator,
	}
}

// triggerBroadcast signals the broadcast worker to call broadcastGameUpdate.
// Multiple rapid calls coalesce into a single broadcast.
func (h *Hub) triggerBroadcast() {
	select {
	case h.broadcastReqCh <- struct{}{}:
	default: // already pending; worker will pick it up
	}
}

// stop signals the hub goroutine to exit and waits for it and all
// WebSocket goroutines to finish. Channels are closed here (after all
// senders have stopped) to avoid "send on closed channel" panics.
func (h *Hub) stop() {
	close(h.done)
	h.wg.Wait() // waits for run() + broadcast worker; no senders alive after this

	// Close send channels for any remaining clients so writer goroutines exit.
	h.mu.Lock()
	for _, client := range h.clients {
		close(client.send)
		client.conn.Close()
	}
	h.mu.Unlock()

	h.clientWg.Wait()
}

func (h *Hub) sendToPlayer(playerID int64, message []byte) {
	// Fetch player name for logging before acquiring the lock.
	var playerName string
	h.db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)
	LogWSMessage("OUT", playerName, string(message))

	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, client := range h.clients {
		if client.playerID == playerID {
			select {
			case client.send <- hubMsg{data: message}:
			default:
				log.Printf("WebSocket send buffer full for player %d, dropping message", playerID)
			}
		}
	}
}

// broadcastAudio sends raw PCM audio bytes to all connected clients as a binary WebSocket frame.
func (h *Hub) broadcastAudio(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, client := range h.clients {
		select {
		case client.send <- hubMsg{binary: true, data: data}:
		default:
			log.Printf("WebSocket audio buffer full for player %d, dropping chunk", client.playerID)
		}
	}
}

func (h *Hub) run() {
	// run() goroutine
	h.wg.Add(1)
	defer h.wg.Done()

	// Broadcast worker: drains broadcastReqCh and calls broadcastGameUpdate.
	// Runs concurrently with run() so the hub goroutine is never blocked by
	// the heavy DB + template work inside broadcastGameUpdate.
	// Tracked by h.wg so stop() waits for it before closing client channels.
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for {
			select {
			case <-h.broadcastReqCh:
				h.broadcastGameUpdate()
			case <-h.done:
				return
			}
		}
	}()

	for {
		select {
		case <-h.done:
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client.conn] = client
			h.mu.Unlock()
			h.clientWg.Add(1)
			go client.writer()
			var playerName string
			h.db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", client.playerID)
			log.Printf("WebSocket client connected (player %d: %s). Total: %d", client.playerID, playerName, len(h.clients))
			DebugLog("hub.register", "Player '%s' (ID: %d) connected via WebSocket", playerName, client.playerID)
			h.addPlayerToLobby(client.playerID)

		case conn := <-h.unregister:
			var removePlayerID int64
			h.mu.Lock()
			client, ok := h.clients[conn]
			if ok {
				playerID := client.playerID
				var playerName string
				h.db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)
				delete(h.clients, conn)
				close(client.send) // signal writer goroutine to exit
				conn.Close()

				// Check if player has any remaining connections
				hasOtherConn := false
				for _, c := range h.clients {
					if c.playerID == playerID {
						hasOtherConn = true
						break
					}
				}

				if !hasOtherConn {
					log.Printf("Player '%s' (ID: %d) has no more connections, removing from lobby", playerName, playerID)
					DebugLog("hub.unregister", "Player '%s' (ID: %d) has no more connections, removing from lobby", playerName, playerID)
					removePlayerID = playerID
				} else {
					log.Printf("Player '%s' (ID: %d)  still has other connections", playerName, playerID)
					DebugLog("hub.unregister", "Player '%s' (ID: %d) still has other connections", playerName, playerID)
				}
			}
			h.mu.Unlock()
			log.Printf("WebSocket client disconnected. Total: %d", len(h.clients))
			// Call removePlayerFromLobby after releasing mutex — it calls triggerBroadcast
			// which needs no lock, but the pattern is consistent with before.
			if removePlayerID != 0 {
				h.removePlayerFromLobby(removePlayerID)
				log.Printf("Removed Player: %d", removePlayerID)
			}

		case message := <-h.broadcast:
			h.mu.RLock()
			for _, client := range h.clients {
				select {
				case client.send <- hubMsg{data: message}:
				default:
					log.Printf("WebSocket broadcast buffer full for player %d, dropping message", client.playerID)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// connectedPlayerIDs returns the set of unique player IDs with active WebSocket connections.
func (h *Hub) connectedPlayerIDs() []int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := make(map[int64]bool)
	var ids []int64
	for _, client := range h.clients {
		if !seen[client.playerID] {
			seen[client.playerID] = true
			ids = append(ids, client.playerID)
		}
	}
	return ids
}

// broadcastGameUpdate sends the current game state to all connected clients
func (h *Hub) broadcastGameUpdate() {
	game, err := getOrCreateCurrentGame(h.db)
	if err != nil {
		logError("broadcastGameUpdate: getOrCreateCurrentGame", err)
		return
	}

	players, err := getPlayersByGameId(h.db, game.ID)
	if err != nil {
		logError("broadcastGameUpdate: getPlayersByGameId", err)
		return
	}

	DebugLog("broadcastGameUpdate", "Broadcasting to %d players in game %d (status: %s)", len(players), game.ID, game.Status)

	for _, p := range players {
		// Build all three template outputs and combine into a single WebSocket message.
		// HTMX processes all hx-swap-oob elements found in one message atomically,
		// which means clients receive a consistent update in one htmx:wsAfterMessage event.
		var combined bytes.Buffer

		buf, err := getGameComponent(h.db, h.templates, p.PlayerID, game)
		if err != nil {
			logError("broadcastGameUpdate: getGameComponent", err)
			continue
		}
		combined.Write(buf.Bytes())

		seerInvestigated := getSeerInvestigated(h.db, game.ID, p.PlayerID)
		visiblePlayers := applyCardVisibility(p, selfFirstPlayers(players, p.PlayerID), seerInvestigated)
		data := SidebarData{
			Player:         &p,
			Players:        visiblePlayers,
			Game:           game,
			LoverPartnerID: getLoverPartner(h.db, game.ID, p.PlayerID),
		}
		h.templates.ExecuteTemplate(&combined, "sidebar.html", data)

		historyBuf, err := getGameHistory(h.db, h.templates, p.PlayerID, game)
		if err != nil {
			logError("broadcastGameHistory: getGameHistory", err)
			continue
		}
		combined.Write(historyBuf.Bytes())

		h.sendToPlayer(p.PlayerID, combined.Bytes())
	}
}

// logDBState logs the database state with this hub's db
func (h *Hub) logDBState(context string) {
	LogDBState(h.db, context)
}

// addPlayerToLobby adds a player to the game if it's in lobby state
func (h *Hub) addPlayerToLobby(playerID int64) {
	var playerName string
	h.db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)

	game, err := getOrCreateCurrentGame(h.db)
	if err != nil {
		logError("addPlayerToLobby: getOrCreateCurrentGame", err)
		return
	}

	if game.Status != "lobby" {
		DebugLog("addPlayerToLobby", "Player '%s' (ID: %d) cannot join - game status is '%s'", playerName, playerID, game.Status)
		return
	}

	result, err := h.db.Exec("INSERT OR IGNORE INTO game_player (game_id, player_id) VALUES (?, ?)", game.ID, playerID)
	if err != nil {
		logError("addPlayerToLobby: db.Exec insert", err)
		return
	}

	rows, _ := result.RowsAffected()
	if rows > 0 {
		log.Printf("Player %d (%s) added to lobby (connected)", playerID, playerName)
		DebugLog("addPlayerToLobby", "Player '%s' (ID: %d) joined game %d lobby", playerName, playerID, game.ID)
		h.logDBState("after player join: " + playerName)
		h.triggerBroadcast()

		log.Printf("Player %d (%s) added to lobby (triggered broadcast )", playerID, playerName)
	} else {
		DebugLog("addPlayerToLobby", "Player '%s' (ID: %d) already in game %d", playerName, playerID, game.ID)
	}
}

// removePlayerFromLobby removes a player from the game if it's still in lobby state
func (h *Hub) removePlayerFromLobby(playerID int64) {
	var playerName string
	h.db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)

	game, err := getOrCreateCurrentGame(h.db)
	if err != nil {
		logError("removePlayerFromLobby: getOrCreateCurrentGame", err)
		return
	}

	if game.Status != "lobby" {
		DebugLog("removePlayerFromLobby", "Player '%s' (ID: %d) cannot leave - game status is '%s'", playerName, playerID, game.Status)
		return
	}

	_, err = h.db.Exec("DELETE FROM game_player WHERE game_id = ? AND player_id = ?", game.ID, playerID)
	if err != nil {
		logError("removePlayerFromLobby: db.Exec delete", err)
		return
	}

	log.Printf("Player %d (%s) removed from lobby (disconnected)", playerID, playerName)
	DebugLog("removePlayerFromLobby", "Player '%s' (ID: %d) left game %d lobby", playerName, playerID, game.ID)
	h.logDBState("after player leave: " + playerName)
	h.triggerBroadcast()
}

// getOrCreateCurrentGame returns the current waiting game, or creates one if none exists
func getOrCreateCurrentGame(db *sqlx.DB) (*Game, error) {
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
	} else if err != nil {
		return nil, err
	}
	return &game, nil
}

func handleWebSocket(app *App, w http.ResponseWriter, r *http.Request) {
	currentHub := app.hub

	playerID, err := getPlayerIdFromSession(app.db, r)
	if err != nil {
		DebugLog("handleWebSocket", "Rejected WebSocket connection - not logged in")
		http.Error(w, "Not logged in", http.StatusUnauthorized)
		return
	}

	var playerName string
	app.db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)
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

	// Check if the player still exists in the current game.
	// On reconnect after a disconnect, the player may have been removed.
	var game Game
	err = app.db.Get(&game, "SELECT rowid as id, status, round FROM game ORDER BY id DESC LIMIT 1")
	if err == nil && game.Status != "lobby" {
		var count int
		app.db.Get(&count, "SELECT COUNT(*) FROM game_player WHERE game_id = ? AND player_id = ?", game.ID, playerID)
		if count == 0 {
			DebugLog("handleWebSocket", "Player '%s' (ID: %d) not in game %d, redirecting to index", playerName, playerID, game.ID)
			conn.WriteMessage(websocket.TextMessage, []byte(`<div id="game-content" hx-swap-oob="innerHTML" hx-on::load="window.location.href='/'"></div>`))
			conn.Close()
			return
		}
	}

	client := &Client{conn: conn, playerID: playerID, hub: currentHub, send: make(chan hubMsg, clientSendBuf)}
	currentHub.register <- client

	// Handle messages and disconnection.
	// clientWg tracks this goroutine so hub.stop() can wait for it to exit
	// before cleanup proceeds — preventing resources from being closed while
	// this goroutine is still using them.
	currentHub.clientWg.Add(1)
	go func() {
		defer currentHub.clientWg.Done()
		defer func() {
			currentHub.unregister <- conn
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
