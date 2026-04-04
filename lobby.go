package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
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

func handleWSUpdateRole(client *Client, msg WSMessage) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSUpdateRole: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "lobby" {
		h.logf("Cannot update roles: game status is '%s', expected 'lobby'", game.Status)
		h.sendErrorToast(client.playerID, "Cannot update roles: game already started")
		return
	}

	roleID := msg.RoleID
	delta := msg.Delta

	// For additions, check that we haven't already filled all player slots
	if delta == "1" {
		var totalRoles int
		h.db.Get(&totalRoles, "SELECT COALESCE(SUM(count), 0) FROM game_role_config WHERE game_id = ?", game.ID)
		var playerCount int
		h.db.Get(&playerCount, "SELECT COUNT(*) FROM game_player WHERE game_id = ?", game.ID)
		if totalRoles >= playerCount {
			h.logf("Rejected role addition: %d roles already cover all %d players", totalRoles, playerCount)
			return
		}
	}

	// Get current count
	var current GameRoleConfig
	err = h.db.Get(&current, "SELECT rowid as id, game_id, role_id, count FROM game_role_config WHERE game_id = ? AND role_id = ?", game.ID, roleID)

	if err == sql.ErrNoRows {
		if delta == "1" {
			h.db.Exec("INSERT INTO game_role_config (game_id, role_id, count) VALUES (?, ?, 1)", game.ID, roleID)
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
			h.db.Exec("UPDATE game_role_config SET count = ? WHERE rowid = ?", newCount, current.ID)
			DebugLog("handleWSUpdateRole", "Updated role %s count to %d for game %d", roleID, newCount, game.ID)
		} else {
			h.db.Exec("DELETE FROM game_role_config WHERE rowid = ?", current.ID)
			DebugLog("handleWSUpdateRole", "Removed role %s from game %d", roleID, game.ID)
		}
	}

	h.logDBState("after role update")
	h.triggerBroadcast()
}

func handleWSStartGame(client *Client) {
	h := client.hub
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSStartGame: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	h.logf("Starting game: id=%d, status='%s'", game.ID, game.Status)

	if game.Status != "lobby" {
		h.logf("Cannot start: game status is '%s', expected 'lobby'", game.Status)
		h.sendErrorToast(client.playerID, "Game already started")
		return
	}

	// Get players
	players, err := getPlayersByGameId(h.db, game.ID)
	if err != nil {
		h.logError("handleWSStartGame: getPlayersByGameId", err)
		h.sendErrorToast(client.playerID, "Failed to get players")
		return
	}
	h.logf("Found %d players in game", len(players))

	// Get role configuration
	var roleConfigs []GameRoleConfig
	err = h.db.Select(&roleConfigs, "SELECT rowid as id, game_id, role_id, count FROM game_role_config WHERE game_id = ?", game.ID)
	if err != nil {
		h.logError("handleWSStartGame: db.Select roleConfigs", err)
		h.sendErrorToast(client.playerID, "Failed to get role configuration")
		return
	}
	h.logf("Found %d role configs", len(roleConfigs))

	// Build role pool
	var rolePool []int64
	for _, rc := range roleConfigs {
		for i := 0; i < rc.Count; i++ {
			rolePool = append(rolePool, rc.RoleID)
		}
	}
	h.logf("Role pool size: %d", len(rolePool))

	if len(rolePool) != len(players) {
		h.logf("Cannot start: role count (%d) != player count (%d)", len(rolePool), len(players))
		h.sendErrorToast(client.playerID, "Role count must match player count")
		return
	}

	// Shuffle role pool
	shuffleRoles(rolePool)
	h.logf("Roles shuffled, assigning to players...")

	// Replace any Joker slots with a random role drawn from all non-Joker roles
	var jokerRoleID int64
	h.db.Get(&jokerRoleID, "SELECT rowid FROM role WHERE name = 'Joker'")
	var allRoleIDs []int64
	h.db.Select(&allRoleIDs, "SELECT rowid FROM role WHERE name != 'Joker'")
	for i, roleID := range rolePool {
		if roleID == jokerRoleID {
			jBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(allRoleIDs))))
			if err != nil {
				h.sendErrorToast(client.playerID, "Failed to assign Joker role")
				return
			}
			rolePool[i] = allRoleIDs[jBig.Int64()]
			h.logf("Joker at slot %d replaced with role ID %d", i, rolePool[i])
		}
	}

	// Assign roles to players
	for i, gp := range players {
		h.logf("Assigning role %d to player %d (game_player id=%d)", rolePool[i], gp.PlayerID, gp.ID)
		_, err := h.db.Exec("UPDATE game_player SET role_id = ? WHERE rowid = ?", rolePool[i], gp.ID)
		if err != nil {
			h.logError("handleWSStartGame: db.Exec assign role", err)
			h.sendErrorToast(client.playerID, "Failed to assign roles")
			return
		}
	}
	h.logf("Roles assigned, updating game status...")

	// Update game status and set night 1
	_, err = h.db.Exec("UPDATE game SET status = 'night', round = 1 WHERE rowid = ?", game.ID)
	if err != nil {
		h.logError("handleWSStartGame: db.Exec update game status", err)
		h.sendErrorToast(client.playerID, "Failed to start game")
		return
	}
	h.logf("Game status updated to 'night' (night 1), broadcasting...")
	DebugLog("handleWSStartGame", "Game %d started, transitioning to night phase (night 1)", game.ID)
	h.logDBState("after game start")

	h.triggerBroadcast()
	h.maybeSpeakStory(game.ID, "The game begins. Night falls upon the village.")
	h.logf("Game started successfully!")
}

// renderLobby renders the lobby component for a player
func renderLobby(h *Hub, game Game, player Player, players []Player) (*bytes.Buffer, error) {
	roles, _ := getRoles(h.db)

	// Count players and total role slots
	playerCount := len(players)
	totalRoles := 0

	// Get role configurations
	var roleConfigs []GameRoleConfig
	h.db.Select(&roleConfigs, "SELECT rowid as id, game_id, role_id, count FROM game_role_config WHERE game_id = ?", game.ID)

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
	err := h.templates.ExecuteTemplate(&buf, "lobby.html", data)
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
