package main

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// WSMessage represents a message from the client
type WSMessage struct {
	Action         string `json:"action"`
	RoleID         string `json:"role_id,omitempty"`
	Delta          string `json:"delta,omitempty"`
	TargetPlayerID string `json:"target_player_id,omitempty"`
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
