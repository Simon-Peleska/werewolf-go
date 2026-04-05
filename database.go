package main

import (
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

type Game struct {
	ID     int64  `db:"id"`
	Name   string `db:"name"`
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
	Lover           int64  `db:"lover"`
	IsDoppelganger  bool   `db:"is_doppelganger"`  // true if player was originally a Doppelganger who has copied a role
	ProfileImageID  *int64 `db:"profile_image_id"` // nil = no image; rowid of player_image row (changes per upload → cache-buster)
}

func getPlayerInGame(db *sqlx.DB, gameID, playerID int64) (Player, error) {
	var player Player
	err := db.Get(&player, `
		SELECT gp.rowid as id,
			g.rowid as game_id,
			p.rowid as player_id,
			p.name as name,
			p.secret_code as secret_code,
			r.rowid as role_id,
			r.name as role_name,
			r.description as role_description,
			r.team as team,
			gp.is_alive as is_alive,
			gp.is_observer as is_observer,
			IFNULL(l.player2_id, 0) as lover,
			CASE WHEN gp.original_role_id IS NOT NULL THEN 1 ELSE 0 END as is_doppelganger,
			p.profile_image_id as profile_image_id
		FROM game_player gp
			JOIN player p on gp.player_id = p.rowid
			JOIN game g on gp.game_id = g.rowid
			JOIN role r on gp.role_id = r.rowid
			LEFT JOIN game_lovers l on l.player1_id = p.rowid
		WHERE gp.game_id = ? AND gp.player_id = ?`, gameID, playerID)
	return player, err
}

func getPlayersByGameId(db *sqlx.DB, id int64) ([]Player, error) {
	var players []Player
	err := db.Select(&players, `
		SELECT gp.rowid as id,
			g.rowid as game_id,
			p.rowid as player_id,
			p.name as name,
			p.secret_code as secret_code,
			r.rowid as role_id,
			r.name as role_name,
			r.description as role_description,
			r.team as team,
			gp.is_alive as is_alive,
			is_observer as is_observer,
			IFNULL(l.player2_id, 0) as lover,
			CASE WHEN gp.original_role_id IS NOT NULL THEN 1 ELSE 0 END as is_doppelganger,
			p.profile_image_id as profile_image_id
		FROM game_player gp
			JOIN player p on gp.player_id = p.rowid
			JOIN game g on gp.game_id = g.rowid
			JOIN role r on gp.role_id = r.rowid
			LEFT JOIN game_lovers l on l.player1_id = p.rowid
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

func getRoles(db *sqlx.DB) ([]Role, error) {
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
	ActionWerewolfKill       = "werewolf_kill"
	ActionDayVote            = "day_vote"
	ActionElimination        = "elimination"
	ActionSeerSelect         = "seer_select" // pending investigation selection (toggled, committed on Investigate)
	ActionSeerInvestigate    = "seer_investigate"
	ActionDoctorSelect       = "doctor_select" // pending protection target (toggled, committed on Protect)
	ActionDoctorProtect      = "doctor_protect"
	ActionGuardSelect        = "guard_select" // pending protection target (toggled, committed on Protect)
	ActionGuardProtect       = "guard_protect"
	ActionHunterSelect       = "hunter_select" // pending revenge target (toggled, committed on Shoot)
	ActionHunterRevenge      = "hunter_revenge"
	ActionWitchSelectHeal    = "witch_select_heal"    // pending heal selection (toggled, committed on Apply)
	ActionWitchSelectPoison  = "witch_select_poison"  // pending poison selection (toggled, committed on Apply)
	ActionWitchHeal          = "witch_heal"           // committed heal (written by witch_apply)
	ActionWitchKill          = "witch_kill"           // committed kill (written by witch_apply)
	ActionWitchApply         = "witch_apply"          // witch presses Done; replaces witch_pass
	ActionWerewolfKill2      = "werewolf_kill_2"      // second kill on Wolf Cub death night
	ActionCupidLink          = "cupid_link"           // tracks Cupid's step-1 lover choice (Night 1 only)
	ActionCupidLink2         = "cupid_link_2"         // tracks Cupid's step-2 lover choice (Night 1 only)
	ActionDoppelgangerSelect = "doppelganger_select"  // pending copy target (Night 1 only)
	ActionDoppelgangerCopy   = "doppelganger_copy"    // committed copy: Doppelganger becomes target's role (Night 1 only)
	ActionLoverHeartbreak    = "lover_heartbreak"     // partner dies of heartbreak when their lover is killed
	ActionWerewolfEndVote    = "werewolf_end_vote"    // one wolf presses End Vote to lock in first kill
	ActionWerewolfEndVote2   = "werewolf_end_vote_2"  // one wolf presses End Vote to lock in second kill (Wolf Cub)
	ActionNightKill          = "night_kill"           // public death record inserted when any player dies at night
	ActionStory              = "story"                // AI-generated story shown in history after deaths
	ActionNightSurvey        = "night_survey"         // one per player when they press Continue (visibility=resolved)
	ActionNightSurveySuspect = "night_survey_suspect" // pending suspect selection (toggled, committed on Continue)
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

// getVoteCounts returns vote counts for a specific phase
func getVoteCounts(db *sqlx.DB, gameID int64, round int, phase string, actionType string) (map[int64]int, int, error) {
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
func getLoverPartner(db *sqlx.DB, gameID, playerID int64) int64 {
	var partnerID int64
	db.Get(&partnerID, `SELECT player2_id FROM game_lovers WHERE game_id = ? AND player1_id = ?`, gameID, playerID)
	return partnerID
}

func initDB(db *sqlx.DB, logfn func(string, ...any)) error {
	schema := `
	PRAGMA journal_mode=WAL;
	PRAGMA synchronous=NORMAL;
	PRAGMA busy_timeout=5000;
	PRAGMA cache_size=-64000;
	PRAGMA mmap_size=268435456;
	PRAGMA temp_store=MEMORY;
	PRAGMA auto_vacuum=INCREMENTAL;
	PRAGMA page_size=4096;

	CREATE TABLE IF NOT EXISTS game (
		name TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'lobby',
		round INTEGER NOT NULL DEFAULT 0
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_game_name ON game(name) WHERE name != '';
	CREATE TABLE IF NOT EXISTS player (
		name TEXT UNIQUE NOT NULL,
		secret_code TEXT NOT NULL,
		profile_image_id INTEGER REFERENCES player_image,
		profile_image_uploaded_at INTEGER
	);
	CREATE TABLE IF NOT EXISTS game_player (
		game_id INTEGER NOT NULL,
		player_id INTEGER NOT NULL,
		role_id INTEGER NOT NULL DEFAULT 1,
		is_alive INTEGER NOT NULL DEFAULT 1,
		is_observer INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY (game_id) REFERENCES game(rowid),
		FOREIGN KEY (player_id) REFERENCES player(rowid),
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
		FOREIGN KEY (game_id) REFERENCES game(rowid),
		FOREIGN KEY (role_id) REFERENCES role(rowid),
		UNIQUE(game_id, role_id)
	);
	CREATE TABLE IF NOT EXISTS session (
		token INTEGER,
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
	CREATE TABLE IF NOT EXISTS cupid_selection (
		game_id INTEGER NOT NULL,
		cupid_player_id INTEGER NOT NULL,
		first_player_id INTEGER,
		second_player_id INTEGER,
		FOREIGN KEY (game_id) REFERENCES game(rowid),
		FOREIGN KEY (cupid_player_id) REFERENCES player(rowid),
		FOREIGN KEY (first_player_id) REFERENCES player(rowid),
		FOREIGN KEY (second_player_id) REFERENCES player(rowid),
		UNIQUE(game_id, cupid_player_id)
	);
	CREATE TABLE IF NOT EXISTS player_image (
		image_data BLOB NOT NULL,
		mime_type TEXT NOT NULL
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
	  ('Wolf Cub', 'If eliminated, werewolves kill two victims the next night.', 'werewolf'),
	  ('Doppelganger', 'On night 1, secretly copies another player''s role and becomes that role for the rest of the game.', 'villager'),
	  ('Joker', 'Gets assigned a random other role at the start of the game.', 'villager')
	`
	_, err := db.Exec(schema)
	if err != nil {
		logfn("initDB error: %v", err)
		return err
	}

	// Migration: add profile_image_id to player table (idempotent)
	if err := addColumnIfNotExists(db, "player", "profile_image_id", "INTEGER REFERENCES player_image(player_id)"); err != nil {
		logfn("initDB migration error: %v", err)
		return err
	}

	// Migration: add original_role_id to game_player for Doppelganger tracking (idempotent)
	if err := addColumnIfNotExists(db, "game_player", "original_role_id", "INTEGER REFERENCES role(rowid)"); err != nil {
		logfn("initDB migration error: %v", err)
		return err
	}

	logfn("Database initialized successfully")
	return nil
}

func addColumnIfNotExists(db *sqlx.DB, table, column, definition string) error {
	_, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	if err != nil && strings.Contains(err.Error(), "duplicate column name") {
		return nil
	}
	return err
}

func savePlayerImage(db *sqlx.DB, playerID int64, data []byte, mimeType string) (int64, error) {
	// Get old image ID so we can delete it after inserting the new one.
	var oldImageID *int64
	db.Get(&oldImageID, `SELECT profile_image_id FROM player WHERE rowid = ?`, playerID)

	result, err := db.Exec(`INSERT INTO player_image(image_data, mime_type) VALUES (?, ?)`, data, mimeType)
	if err != nil {
		return 0, err
	}
	imageID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err = db.Exec(`UPDATE player SET profile_image_id = ? WHERE rowid = ?`, imageID, playerID); err != nil {
		return 0, err
	}
	if oldImageID != nil {
		db.Exec(`DELETE FROM player_image WHERE rowid = ?`, *oldImageID)
	}
	return imageID, nil
}

func getPlayerImage(db *sqlx.DB, imageID int64) ([]byte, string, error) {
	var row struct {
		Data     []byte `db:"image_data"`
		MimeType string `db:"mime_type"`
	}
	err := db.Get(&row, `SELECT image_data, mime_type FROM player_image WHERE rowid = ?`, imageID)
	return row.Data, row.MimeType, err
}

// getOrCreateGameByName returns the game with the given name, creating it if it doesn't exist.
func getOrCreateGameByName(db *sqlx.DB, name string) (*Game, error) {
	db.Exec("INSERT OR IGNORE INTO game (name, status, round) VALUES (?, 'lobby', 0)", name)
	var game Game
	err := db.Get(&game, "SELECT rowid as id, name, status, round FROM game WHERE name = ?", name)
	return &game, err
}
