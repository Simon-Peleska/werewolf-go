package main

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
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

// Client represents a websocket connection with player info
type Client struct {
	conn     *websocket.Conn
	playerID int64
	writeMu  sync.Mutex // Serialize writes to WebSocket (required by gorilla/websocket)
}

// WebSocket hub for broadcasting updates to all connected clients
type Hub struct {
	clients    map[*websocket.Conn]*Client
	broadcast  chan []byte
	register   chan *Client
	unregister chan *websocket.Conn
	mu         sync.RWMutex
	done       chan struct{}
	wg         sync.WaitGroup
}

func newHub() *Hub {
	return &Hub{
		clients:    make(map[*websocket.Conn]*Client),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *websocket.Conn, 64),
		done:       make(chan struct{}),
	}
}

// stop signals the hub goroutine to exit and waits for it to finish
func (h *Hub) stop() {
	close(h.done)
	h.wg.Wait()
}

var hub = newHub()

func (h *Hub) sendToPlayer(playerID int64, message []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, client := range h.clients {
		if client.playerID == playerID {
			// Get player name for logging
			var playerName string
			db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)
			LogWSMessage("OUT", playerName, string(message))

			// Serialize writes to each connection
			client.writeMu.Lock()
			err := client.conn.WriteMessage(websocket.TextMessage, message)
			client.writeMu.Unlock()

			if err != nil {
				log.Printf("WebSocket write error to player %d: %v", playerID, err)
			}
		}
	}
}

func (h *Hub) run() {
	h.wg.Add(1)
	defer h.wg.Done()
	for {
		select {
		case <-h.done:
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client.conn] = client
			h.mu.Unlock()
			var playerName string
			db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", client.playerID)
			log.Printf("WebSocket client connected (player %d: %s). Total: %d", client.playerID, playerName, len(h.clients))
			DebugLog("hub.register", "Player '%s' (ID: %d) connected via WebSocket", playerName, client.playerID)
			addPlayerToLobby(client.playerID)

		case conn := <-h.unregister:
			var removePlayerID int64
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

				if !hasOtherConn {
					DebugLog("hub.unregister", "Player '%s' (ID: %d) has no more connections, removing from lobby", playerName, playerID)
					removePlayerID = playerID
				} else {
					DebugLog("hub.unregister", "Player '%s' (ID: %d) still has other connections", playerName, playerID)
				}
			}
			h.mu.Unlock()
			log.Printf("WebSocket client disconnected. Total: %d", len(h.clients))
			// Call removePlayerFromLobby after releasing mutex — it calls broadcastGameUpdate
			// which calls sendToPlayer which needs the read lock
			if removePlayerID != 0 {
				removePlayerFromLobby(removePlayerID)
			}

		case message := <-h.broadcast:
			h.mu.RLock()
			for conn, client := range h.clients {
				// Serialize writes to each connection
				client.writeMu.Lock()
				err := conn.WriteMessage(websocket.TextMessage, message)
				client.writeMu.Unlock()

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

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Capture globals at entry to avoid race conditions in parallel tests
	currentDB := db
	currentHub := hub

	playerID, err := getPlayerIdFromSession(r)
	if err != nil {
		DebugLog("handleWebSocket", "Rejected WebSocket connection - not logged in")
		http.Error(w, "Not logged in", http.StatusUnauthorized)
		return
	}

	var playerName string
	currentDB.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)
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
	currentHub.register <- client

	// Handle messages and disconnection
	go func() {
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
