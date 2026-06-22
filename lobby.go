package main

import (
	"crypto/rand"
	"database/sql"
	"math/big"
)

type LobbyData struct {
	Players     []Player
	RoleConfigs []RoleConfigDisplay
	RoleCards   []PlayerCardData
	TotalRoles  int
	PlayerCount int
	CanStart    bool
	GameID      int64
	GameStatus  string
	Lang        string
}

type RoleConfigDisplay struct {
	Role  Role
	Count int
}

func handleWSUpdateRole(client *Client, msg WSMessage) {
	h := client.hub
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSUpdateRole: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	if game.Status != "lobby" {
		h.logf("Cannot update roles: game status is '%s', expected 'lobby'", game.Status)
		h.sendErrorToast(client.playerID, T(lang, "err_game_already_started"))
		return
	}

	roleID := msg.RoleID
	delta := msg.Delta

	// reject role additions once slots already cover every player
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
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSStartGame: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	h.logf("Starting game: id=%d, status='%s'", game.ID, game.Status)

	if game.Status != "lobby" {
		h.logf("Cannot start: game status is '%s', expected 'lobby'", game.Status)
		h.sendErrorToast(client.playerID, T(lang, "err_game_started"))
		return
	}

	players, err := getPlayersByGameId(h.db, game.ID)
	if err != nil {
		h.logError("handleWSStartGame: getPlayersByGameId", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_players"))
		return
	}
	h.logf("Found %d players in game", len(players))

	var roleConfigs []GameRoleConfig
	err = h.db.Select(&roleConfigs, "SELECT rowid as id, game_id, role_id, count FROM game_role_config WHERE game_id = ?", game.ID)
	if err != nil {
		h.logError("handleWSStartGame: db.Select roleConfigs", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_roles"))
		return
	}
	h.logf("Found %d role configs", len(roleConfigs))

	var rolePool []int64
	for _, rc := range roleConfigs {
		for i := 0; i < rc.Count; i++ {
			rolePool = append(rolePool, rc.RoleID)
		}
	}
	h.logf("Role pool size: %d", len(rolePool))

	if len(rolePool) != len(players) {
		h.logf("Cannot start: role count (%d) != player count (%d)", len(rolePool), len(players))
		h.sendErrorToast(client.playerID, T(lang, "err_role_count_mismatch"))
		return
	}

	shuffleRoles(rolePool)
	h.logf("Roles shuffled, assigning to players...")

	// Joker is never seen in-game — replace each Joker slot with a random non-Joker role
	var jokerRoleID int64
	h.db.Get(&jokerRoleID, "SELECT rowid FROM role WHERE name = 'Joker'")
	var allRoleIDs []int64
	h.db.Select(&allRoleIDs, "SELECT rowid FROM role WHERE name != 'Joker'")
	for i, roleID := range rolePool {
		if roleID == jokerRoleID {
			jBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(allRoleIDs))))
			if err != nil {
				h.sendErrorToast(client.playerID, T(lang, "err_failed_assign_joker"))
				return
			}
			rolePool[i] = allRoleIDs[jBig.Int64()]
			h.logf("Joker at slot %d replaced with role ID %d", i, rolePool[i])
		}
	}

	for i, gp := range players {
		h.logf("Assigning role %d to player %d (game_player id=%d)", rolePool[i], gp.PlayerID, gp.ID)
		_, err := h.db.Exec("UPDATE game_player SET role_id = ? WHERE rowid = ?", rolePool[i], gp.ID)
		if err != nil {
			h.logError("handleWSStartGame: db.Exec assign role", err)
			h.sendErrorToast(client.playerID, T(lang, "err_failed_assign_roles"))
			return
		}
	}
	h.logf("Roles assigned, updating game status...")

	_, err = h.db.Exec("UPDATE game SET status = 'night', round = 1 WHERE rowid = ?", game.ID)
	if err != nil {
		h.logError("handleWSStartGame: db.Exec update game status", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_start_game"))
		return
	}
	h.logf("Game status updated to 'night' (night 1), broadcasting...")
	DebugLog("handleWSStartGame", "Game %d started, transitioning to night phase (night 1)", game.ID)
	h.logDBState("after game start")

	h.triggerBroadcast()
	h.maybeSpeakStory(game.ID, T(h.storytellerLang, "tts_game_begins"))
	h.logf("Game started successfully!")
}

func shuffleRoles(roles []int64) {
	for i := len(roles) - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			// rand.Int essentially never fails; swap with the previous element rather than aborting the shuffle
			roles[i], roles[i-1] = roles[i-1], roles[i]
			continue
		}
		j := int(jBig.Int64())
		roles[i], roles[j] = roles[j], roles[i]
	}
}
