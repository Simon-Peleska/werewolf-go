package main

import (
	"log"
)

// FinishedData holds all data needed to render the finished game screen
type FinishedData struct {
	Players []Player
	Winner  string // "villagers" or "werewolves"
}

// transitionToNight moves the game to the next night phase
func transitionToNight(game *Game) {
	newRound := game.Round + 1
	_, err := db.Exec("UPDATE game SET status = 'night', round = ? WHERE rowid = ?", newRound, game.ID)
	if err != nil {
		logError("transitionToNight: update game", err)
		return
	}

	log.Printf("Day %d ended, transitioning to night %d", game.Round, newRound)
	DebugLog("transitionToNight", "Day %d ended, transitioning to night %d", game.Round, newRound)
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

	// Lovers win condition: if only 2 players alive and they are a linked lover pair,
	// they win together regardless of team (overrides normal win conditions).
	if werewolfCount+villagerCount == 2 {
		var alivePlayers []Player
		db.Select(&alivePlayers, `
			SELECT g.player_id as player_id FROM game_player g
			WHERE g.game_id = ? AND g.is_alive = 1`, game.ID)
		if len(alivePlayers) == 2 {
			if getLoverPartner(game.ID, alivePlayers[0].PlayerID) == alivePlayers[1].PlayerID {
				log.Printf("LOVERS WIN - last two alive are the lovers")
				endGame(game, "lovers")
				return true
			}
		}
	}

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

// handleWSNewGame resets the game: creates a new lobby game with the same role counts,
// cleans up the finished game, and puts all connected players into the new lobby.
func handleWSNewGame(client *Client) {
	game, err := getOrCreateCurrentGame()
	if err != nil {
		logError("handleWSNewGame: getOrCreateCurrentGame", err)
		sendErrorToast(client.playerID, "Failed to get game")
		return
	}

	if game.Status != "finished" {
		log.Printf("Cannot start new game: game status is '%s', expected 'finished'", game.Status)
		sendErrorToast(client.playerID, "Game is not finished yet")
		return
	}

	// Save role configs from the finished game
	var roleConfigs []GameRoleConfig
	err = db.Select(&roleConfigs, "SELECT rowid as id, game_id, role_id, count FROM game_role_config WHERE game_id = ?", game.ID)
	if err != nil {
		logError("handleWSNewGame: db.Select roleConfigs", err)
		sendErrorToast(client.playerID, "Failed to get role config")
		return
	}

	// Create new lobby game
	result, err := db.Exec("INSERT INTO game (status, round) VALUES ('lobby', 0)")
	if err != nil {
		logError("handleWSNewGame: create new game", err)
		sendErrorToast(client.playerID, "Failed to create new game")
		return
	}
	newGameID, _ := result.LastInsertId()

	// Copy role configs to new game
	for _, rc := range roleConfigs {
		_, err = db.Exec("INSERT INTO game_role_config (game_id, role_id, count) VALUES (?, ?, ?)", newGameID, rc.RoleID, rc.Count)
		if err != nil {
			logError("handleWSNewGame: copy role config", err)
		}
	}

	// Delete the old game and all its associated data
	oldGameID := game.ID
	db.Exec("DELETE FROM game_action WHERE game_id = ?", oldGameID)
	db.Exec("DELETE FROM game_lovers WHERE game_id = ?", oldGameID)
	db.Exec("DELETE FROM game_role_config WHERE game_id = ?", oldGameID)
	db.Exec("DELETE FROM game_player WHERE game_id = ?", oldGameID)
	db.Exec("DELETE FROM game WHERE rowid = ?", oldGameID)

	// Add all currently connected players to the new lobby
	playerIDs := hub.connectedPlayerIDs()
	for _, pid := range playerIDs {
		_, err = db.Exec("INSERT OR IGNORE INTO game_player (game_id, player_id) VALUES (?, ?)", newGameID, pid)
		if err != nil {
			logError("handleWSNewGame: add player to new game", err)
		}
	}

	log.Printf("New game %d created (replaced game %d), %d players added to lobby, %d role configs copied",
		newGameID, oldGameID, len(playerIDs), len(roleConfigs))
	LogDBState("after new game created")

	broadcastGameUpdate()
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
