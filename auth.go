package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"
	"math/big"
	"net/http"
	"strconv"

	"github.com/jmoiron/sqlx"
)

const sessionCookieName = "werewolf_session"

func generateSecretCode() (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func setSessionCookie(db *sqlx.DB, w http.ResponseWriter, playerID int64) error {
	tokenBig, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	token := tokenBig.Int64()

	_, err := db.Exec("INSERT INTO session (token, player_id) VALUES (?, ?)", token, playerID)
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    strconv.FormatInt(token, 10),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func getPlayerIdFromSession(db *sqlx.DB, r *http.Request) (int64, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return -1, err
	}

	token, err := strconv.ParseInt(cookie.Value, 10, 64)
	if err != nil {
		return -1, err
	}

	var playerID int64
	err = db.Get(&playerID, "SELECT player_id FROM session WHERE token = ?", token)
	if err != nil {
		return -1, err
	}

	return playerID, nil
}

func (app *App) handleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, "error", "Name is required")))
		return
	}

	var existing Player
	err := app.db.Get(&existing, "SELECT rowid as id, name, secret_code FROM player WHERE name = ?", name)
	if err == nil {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, "error", "Name already taken. Use login with secret code if this is you.")))
		return
	}
	if err != sql.ErrNoRows {
		logError("handleSignup: db.Get player", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, "error", "Something went wrong")))
		return
	}

	secretCode, err := generateSecretCode()
	if err != nil {
		logError("handleSignup: generateSecretCode", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, "error", "Something went wrong")))
		return
	}

	result, err := app.db.Exec("INSERT INTO player (name, secret_code) VALUES (?, ?)", name, secretCode)
	if err != nil {
		logError("handleSignup: db.Exec insert player", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, "error", "Something went wrong")))
		return
	}

	playerID, _ := result.LastInsertId()
	log.Printf("New player created: name='%s', id=%d", name, playerID)
	DebugLog("handleSignup", "Player '%s' signed up with ID %d", name, playerID)
	LogDBState(app.db, "after signup: "+name)

	if err := setSessionCookie(app.db, w, playerID); err != nil {
		logError("handleSignup: setSessionCookie", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, "error", "Something went wrong")))
		return
	}
	w.Header().Set("HX-Redirect", "/game")
}

func (app *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.FormValue("name")
	secretCode := r.FormValue("secret_code")

	if name == "" || secretCode == "" {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, "error", "Name and secret code are required")))
		return
	}

	var player Player
	err := app.db.Get(&player, "SELECT rowid as id, name, secret_code FROM player WHERE name = ? AND secret_code = ?", name, secretCode)
	if err == sql.ErrNoRows {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, "error", "Invalid name or secret code")))
		return
	}
	if err != nil {
		logError("handleLogin: db.Get player", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, "error", "Something went wrong")))
		return
	}

	log.Printf("Player logged in: name='%s', id=%d", name, player.ID)
	DebugLog("handleLogin", "Player '%s' logged in with ID %d", name, player.ID)
	if err := setSessionCookie(app.db, w, player.ID); err != nil {
		logError("handleLogin: setSessionCookie", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, "error", "Something went wrong")))
		return
	}
	w.Header().Set("HX-Redirect", "/game")
}

func (app *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	playerID, _ := getPlayerIdFromSession(app.db, r)
	var playerName string
	app.db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)

	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		token, _ := strconv.ParseInt(cookie.Value, 10, 64)
		app.db.Exec("DELETE FROM session WHERE token = ?", token)
	}

	log.Printf("Player logged out: name='%s', id=%d", playerName, playerID)
	DebugLog("handleLogout", "Player '%s' (ID: %d) logged out", playerName, playerID)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}
