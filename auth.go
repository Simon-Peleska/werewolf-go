package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"
	"math/big"
	"net/http"
	"strconv"
)

const sessionCookieName = "werewolf_session"

func generateSecretCode() (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func setSessionCookie(w http.ResponseWriter, playerID int64) {
	tokenBig, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	token := tokenBig.Int64()

	db.Exec("INSERT INTO session (token, player_id) VALUES (?, ?)", token, playerID)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    strconv.FormatInt(token, 10),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func getPlayerIdFromSession(r *http.Request) (int64, error) {
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

func handleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Name is required")))
		return
	}

	var existing Player
	err := db.Get(&existing, "SELECT rowid as id, name, secret_code FROM player WHERE name = ?", name)
	if err == nil {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Name already taken. Use login with secret code if this is you.")))
		return
	}
	if err != sql.ErrNoRows {
		logError("handleSignup: db.Get player", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	secretCode, err := generateSecretCode()
	if err != nil {
		logError("handleSignup: generateSecretCode", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	result, err := db.Exec("INSERT INTO player (name, secret_code) VALUES (?, ?)", name, secretCode)
	if err != nil {
		logError("handleSignup: db.Exec insert player", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	playerID, _ := result.LastInsertId()
	log.Printf("New player created: name='%s', id=%d", name, playerID)
	DebugLog("handleSignup", "Player '%s' signed up with ID %d", name, playerID)
	LogDBState("after signup: " + name)

	setSessionCookie(w, playerID)
	w.Header().Set("HX-Redirect", "/game")
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.FormValue("name")
	secretCode := r.FormValue("secret_code")

	if name == "" || secretCode == "" {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Name and secret code are required")))
		return
	}

	var player Player
	err := db.Get(&player, "SELECT rowid as id, name, secret_code FROM player WHERE name = ? AND secret_code = ?", name, secretCode)
	if err == sql.ErrNoRows {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Invalid name or secret code")))
		return
	}
	if err != nil {
		logError("handleLogin: db.Get player", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast("error", "Something went wrong")))
		return
	}

	log.Printf("Player logged in: name='%s', id=%d", name, player.ID)
	DebugLog("handleLogin", "Player '%s' logged in with ID %d", name, player.ID)
	setSessionCookie(w, player.ID)
	w.Header().Set("HX-Redirect", "/game")
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	playerID, _ := getPlayerIdFromSession(r)
	var playerName string
	db.Get(&playerName, "SELECT name FROM player WHERE rowid = ?", playerID)

	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		token, _ := strconv.ParseInt(cookie.Value, 10, 64)
		db.Exec("DELETE FROM session WHERE token = ?", token)
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
