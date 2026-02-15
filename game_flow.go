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
