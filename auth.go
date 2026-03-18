package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
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

	gameName := r.FormValue("game_name")
	if gameName == "" {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Game name is required")))
		return
	}

	name := r.FormValue("name")
	if name == "" {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Name is required")))
		return
	}

	var existing Player
	err := app.db.Get(&existing, "SELECT rowid as id, name, secret_code FROM player WHERE name = ?", name)
	if err == nil {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Name already taken. Use login with secret code if this is you.")))
		return
	}
	if err != sql.ErrNoRows {
		app.logf("ERROR [handleSignup: db.Get player]: %v", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Something went wrong")))
		return
	}

	secretCode, err := generateSecretCode()
	if err != nil {
		app.logf("ERROR [handleSignup: generateSecretCode]: %v", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Something went wrong")))
		return
	}

	result, err := app.db.Exec("INSERT INTO player (name, secret_code) VALUES (?, ?)", name, secretCode)
	if err != nil {
		app.logf("ERROR [handleSignup: db.Exec insert player]: %v", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Something went wrong")))
		return
	}

	playerID, _ := result.LastInsertId()
	app.logf("New player created: name='%s', id=%d", name, playerID)
	DebugLog("handleSignup", "Player '%s' signed up with ID %d", name, playerID)
	LogDBState(app.db, "after signup: "+name)

	if err := setSessionCookie(app.db, w, playerID); err != nil {
		app.logf("ERROR [handleSignup: setSessionCookie]: %v", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Something went wrong")))
		return
	}
	w.Header().Set("HX-Redirect", "/game/"+gameName)
}

func (app *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gameName := r.FormValue("game_name")
	if gameName == "" {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Game name is required")))
		return
	}

	name := r.FormValue("name")
	secretCode := r.FormValue("secret_code")

	if name == "" || secretCode == "" {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Name and secret code are required")))
		return
	}

	var player Player
	err := app.db.Get(&player, "SELECT rowid as id, name, secret_code FROM player WHERE name = ? AND secret_code = ?", name, secretCode)
	if err == sql.ErrNoRows {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Invalid name or secret code")))
		return
	}
	if err != nil {
		app.logf("ERROR [handleLogin: db.Get player]: %v", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Something went wrong")))
		return
	}

	app.logf("Player logged in: name='%s', id=%d", name, player.ID)
	DebugLog("handleLogin", "Player '%s' logged in with ID %d", name, player.ID)
	if err := setSessionCookie(app.db, w, player.ID); err != nil {
		app.logf("ERROR [handleLogin: setSessionCookie]: %v", err)
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", "Something went wrong")))
		return
	}
	w.Header().Set("HX-Redirect", "/game/"+gameName)
}

func (app *App) handlePlayerImage(w http.ResponseWriter, r *http.Request) {
	imageIDStr := r.PathValue("imageID")
	imageID, err := strconv.ParseInt(imageIDStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data, mimeType, err := getPlayerImage(app.db, imageID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func (app *App) handleUploadPlayerImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	playerID, err := getPlayerIdFromSession(app.db, r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseMultipartForm(2 << 20); err != nil {
		http.Error(w, "Image too large (max 2MB)", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "No image provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	mimeType := header.Header.Get("Content-Type")
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif":
		// ok
	default:
		http.Error(w, "Unsupported image type (jpeg/png/gif only)", http.StatusBadRequest)
		return
	}

	raw, err := io.ReadAll(file)
	if err != nil {
		app.logf("ERROR [handleUploadPlayerImage: ReadAll]: %v", err)
		http.Error(w, "Failed to read image", http.StatusInternalServerError)
		return
	}

	data, err := processProfileImage(raw)
	if err != nil {
		app.logf("ERROR [handleUploadPlayerImage: processProfileImage]: %v", err)
		http.Error(w, "Failed to process image", http.StatusBadRequest)
		return
	}

	imageID, err := savePlayerImage(app.db, playerID, data, "image/jpeg")
	if err != nil {
		app.logf("ERROR [handleUploadPlayerImage: savePlayerImage]: %v", err)
		http.Error(w, "Failed to save image", http.StatusInternalServerError)
		return
	}

	app.logf("Player %d uploaded profile image (%s → image/jpeg 512×512, %d bytes)", playerID, mimeType, len(data))

	// Broadcast to all hubs so everyone sees the updated profile image
	app.hubsMu.RLock()
	for _, hub := range app.hubs {
		hub.triggerBroadcast()
	}
	app.hubsMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		ImageID int64 `json:"image_id"`
	}{ImageID: imageID})
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

	app.logf("Player logged out: name='%s', id=%d", playerName, playerID)
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

// processProfileImage decodes any supported image (jpeg/png/gif), center-crops it to a
// square, resizes it to 512×512, and returns JPEG-encoded bytes.
func processProfileImage(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	cropped := centerCropSquare(src)
	resized := resizeNearest(cropped, 512, 512)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 85}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

// centerCropSquare crops an image to a square at its center.
func centerCropSquare(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == h {
		return src
	}
	size := w
	if h < w {
		size = h
	}
	x0 := b.Min.X + (w-size)/2
	y0 := b.Min.Y + (h-size)/2
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.Draw(dst, dst.Bounds(), src, image.Pt(x0, y0), draw.Src)
	return dst
}

// resizeNearest scales src to width×height using nearest-neighbor sampling.
func resizeNearest(src image.Image, width, height int) image.Image {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			sx := sb.Min.X + x*sw/width
			sy := sb.Min.Y + y*sh/height
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}
