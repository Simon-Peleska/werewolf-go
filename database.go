package main

import (
	"log"
)

type Game struct {
	ID     int64  `db:"id"`
	Status string `db:"status"` // lobby, night, day, finished
	Round  int    `db:"round"`
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
	Description    string `db:"description"` // human-readable history entry; empty = hidden
}

// Action types
const (
	ActionWerewolfKill     = "werewolf_kill"
	ActionDayVote          = "day_vote"
	ActionElimination      = "elimination"
	ActionSeerInvestigate  = "seer_investigate"
	ActionDoctorProtect    = "doctor_protect"
	ActionGuardProtect     = "guard_protect"
	ActionHunterRevenge    = "hunter_revenge"
	ActionWitchHeal        = "witch_heal"
	ActionWitchKill        = "witch_kill"
	ActionWitchPass        = "witch_pass"
	ActionWerewolfKill2    = "werewolf_kill_2"     // second kill on Wolf Cub death night
	ActionCupidLink        = "cupid_link"          // tracks Cupid's step-1 lover choice (Night 1 only)
	ActionLoverHeartbreak  = "lover_heartbreak"    // partner dies of heartbreak when their lover is killed
	ActionWerewolfEndVote  = "werewolf_end_vote"   // one wolf presses End Vote to lock in first kill
	ActionWerewolfEndVote2 = "werewolf_end_vote_2" // one wolf presses End Vote to lock in second kill (Wolf Cub)
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

// getLoverPartner returns the partner's player ID if playerID is one of the two lovers,
// or 0 if they are not a lover (or Cupid hasn't linked anyone yet).
// Both directions are stored in game_lovers, so this is a simple lookup.
func getLoverPartner(gameID, playerID int64) int64 {
	var partnerID int64
	db.Get(&partnerID, `SELECT player2_id FROM game_lovers WHERE game_id = ? AND player1_id = ?`, gameID, playerID)
	return partnerID
}

func initDB() error {
	schema := `
	PRAGMA journal_mode=WAL;

	CREATE TABLE IF NOT EXISTS game (
		status TEXT NOT NULL DEFAULT 'lobby',
		round INTEGER NOT NULL DEFAULT 0
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
	CREATE TABLE IF NOT EXISTS game_lovers (
		game_id INTEGER NOT NULL,
		player1_id INTEGER NOT NULL,
		player2_id INTEGER NOT NULL,
		FOREIGN KEY (game_id) REFERENCES game(rowid),
		UNIQUE(game_id, player1_id)
	);
	CREATE TABLE IF NOT EXISTS game_action (
		game_id INTEGER NOT NULL,
		round INTEGER NOT NULL,
		phase TEXT NOT NULL,
		actor_player_id INTEGER NOT NULL,
		action_type TEXT NOT NULL,
		target_player_id INTEGER,
		visibility TEXT NOT NULL DEFAULT 'public',
		description TEXT NOT NULL DEFAULT '',
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
