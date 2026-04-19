package main

import "fmt"

// FinishedData holds all data needed to render the finished game screen
type FinishedData struct {
	Winners []Player
	Losers  []Player
	Winner  string // "villagers", "werewolves", or "lovers"
	Lang    string
}

// transitionToNight moves the game to the next night phase
func (h *Hub) transitionToNight(game *Game) {
	newRound := game.Round + 1
	_, err := h.db.Exec("UPDATE game SET status = 'night', round = ? WHERE rowid = ?", newRound, game.ID)
	if err != nil {
		h.logError("transitionToNight: update game", err)
		return
	}

	h.logf("Day %d ended, transitioning to night %d", game.Round, newRound)
	DebugLog("transitionToNight", "Day %d ended, transitioning to night %d", game.Round, newRound)
	h.logDBState("after day resolution")

	h.triggerBroadcast()
	h.maybeSpeakStory(game.ID, fmt.Sprintf("Night %d falls upon the village.", newRound))
}

// checkWinConditions checks if the game has ended and returns true if so
func (h *Hub) checkWinConditions(game *Game) bool {
	var werewolfCount, villagerCount int
	err := h.db.Get(&werewolfCount, `
		SELECT COUNT(*) FROM game_player g
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'werewolf'`, game.ID)
	if err != nil {
		h.logError("checkWinConditions: count werewolves", err)
		return false
	}

	err = h.db.Get(&villagerCount, `
		SELECT COUNT(*) FROM game_player g
		JOIN role r ON g.role_id = r.rowid
		WHERE g.game_id = ? AND g.is_alive = 1 AND r.team = 'villager'`, game.ID)
	if err != nil {
		h.logError("checkWinConditions: count villagers", err)
		return false
	}

	h.logf("Win check: %d werewolves, %d villagers alive", werewolfCount, villagerCount)

	// Lovers win condition: if only 2 players alive and they are a linked lover pair,
	// they win together regardless of team (overrides normal win conditions).
	if werewolfCount+villagerCount == 2 {
		var alivePlayers []Player
		h.db.Select(&alivePlayers, `
			SELECT g.player_id as player_id FROM game_player g
			WHERE g.game_id = ? AND g.is_alive = 1`, game.ID)
		if len(alivePlayers) == 2 {
			if getLoverPartner(h.db, game.ID, alivePlayers[0].PlayerID) == alivePlayers[1].PlayerID {
				h.logf("LOVERS WIN - last two alive are the lovers")
				h.endGame(game, "lovers")
				return true
			}
		}
	}

	// Villagers win if all werewolves are dead
	if werewolfCount == 0 {
		h.logf("VILLAGERS WIN - all werewolves eliminated")
		h.endGame(game, "villagers")
		return true
	}

	// Werewolves win if all villagers are dead
	if villagerCount == 0 {
		h.logf("WEREWOLVES WIN - all villagers eliminated")
		h.endGame(game, "werewolves")
		return true
	}

	return false
}

// handleWSNewGame resets the game: creates a new lobby game with the same role counts,
// cleans up the finished game, and puts all connected players into the new lobby.
func (h *Hub) handleWSNewGame(client *Client) {
	lang := h.getPlayerLang(client.playerID)
	game, err := h.getGame()
	if err != nil {
		h.logError("handleWSNewGame: getOrCreateCurrentGame", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_get_game"))
		return
	}

	if game.Status != "finished" {
		h.logf("Cannot start new game: game status is '%s', expected 'finished'", game.Status)
		h.sendErrorToast(client.playerID, T(lang, "err_game_not_finished"))
		return
	}

	// Save role configs from the finished game
	var roleConfigs []GameRoleConfig
	err = h.db.Select(&roleConfigs, "SELECT rowid as id, game_id, role_id, count FROM game_role_config WHERE game_id = ?", game.ID)
	if err != nil {
		h.logError("handleWSNewGame: db.Select roleConfigs", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_role_config"))
		return
	}

	// Delete the old game and all its associated data first (frees the unique name)
	oldGameID := game.ID
	h.db.Exec("DELETE FROM game_action WHERE game_id = ?", oldGameID)
	h.db.Exec("DELETE FROM game_lovers WHERE game_id = ?", oldGameID)
	h.db.Exec("DELETE FROM game_role_config WHERE game_id = ?", oldGameID)
	h.db.Exec("DELETE FROM game_player WHERE game_id = ?", oldGameID)
	h.db.Exec("DELETE FROM game WHERE rowid = ?", oldGameID)

	// Create new lobby game with same name
	result, err := h.db.Exec("INSERT INTO game (name, status, round) VALUES (?, 'lobby', 0)", h.gameName)
	if err != nil {
		h.logError("handleWSNewGame: create new game", err)
		h.sendErrorToast(client.playerID, T(lang, "err_failed_create_game"))
		return
	}
	newGameID, _ := result.LastInsertId()

	// Copy role configs to new game
	for _, rc := range roleConfigs {
		_, err = h.db.Exec("INSERT INTO game_role_config (game_id, role_id, count) VALUES (?, ?, ?)", newGameID, rc.RoleID, rc.Count)
		if err != nil {
			h.logError("handleWSNewGame: copy role config", err)
		}
	}

	// Add all currently connected players to the new lobby
	playerIDs := h.connectedPlayerIDs()
	for _, pid := range playerIDs {
		_, err = h.db.Exec("INSERT OR IGNORE INTO game_player (game_id, player_id) VALUES (?, ?)", newGameID, pid)
		if err != nil {
			h.logError("handleWSNewGame: add player to new game", err)
		}
	}

	h.logf("New game %d created (replaced game %d), %d players added to lobby, %d role configs copied",
		newGameID, oldGameID, len(playerIDs), len(roleConfigs))
	h.logDBState("after new game created")

	h.triggerBroadcast()
}

// endGame marks the game as finished with a winner
func (h *Hub) endGame(game *Game, winner string) {
	_, err := h.db.Exec("UPDATE game SET status = 'finished' WHERE rowid = ?", game.ID)
	if err != nil {
		h.logError("endGame: update game status", err)
		return
	}

	h.logf("Game %d finished, winner: %s", game.ID, winner)
	DebugLog("endGame", "Game %d finished, winner: %s", game.ID, winner)
	h.logDBState("after game end")

	h.triggerBroadcast()
	h.maybeGenerateEnding(game.ID, game.Round, winner)
}
