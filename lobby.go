package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"log"
	"math/big"
)

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

// renderLobby renders the lobby component for a player
func renderLobby(game Game, player Player, players []Player) (*bytes.Buffer, error) {
	roles, _ := getRoles()

	// Count players and total role slots
	playerCount := len(players)
	totalRoles := 0

	// Get role configurations
	var roleConfigs []GameRoleConfig
	db.Select(&roleConfigs, "SELECT rowid as id, game_id, role_id, count FROM game_role_config WHERE game_id = ?", game.ID)

	// Build role config display
	var roleConfigDisplay []RoleConfigDisplay
	for _, rc := range roleConfigs {
		totalRoles += rc.Count
		var roleObj Role
		for _, r := range roles {
			if r.ID == rc.RoleID {
				roleObj = r
				break
			}
		}
		roleConfigDisplay = append(roleConfigDisplay, RoleConfigDisplay{Role: roleObj, Count: rc.Count})
	}

	canStart := playerCount > 0 && playerCount == totalRoles

	data := LobbyData{
		Players:     players,
		RoleConfigs: roleConfigDisplay,
		TotalRoles:  totalRoles,
		PlayerCount: playerCount,
		CanStart:    canStart,
		GameID:      game.ID,
		GameStatus:  game.Status,
	}

	var buf bytes.Buffer
	err := templates.ExecuteTemplate(&buf, "lobby.html", data)
	return &buf, err
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
