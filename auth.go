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
	"strings"

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

// handleCheckName is polled by the sign-in form as the user types their name. It
// reports whether an account with that name already exists, so the form can reveal
// the secret-code field (returning player) or stay a one-field signup (new player).
func (app *App) handleCheckName(w http.ResponseWriter, r *http.Request) {
	lang := getLangFromCookie(r)
	name := strings.TrimSpace(r.URL.Query().Get("name"))

	nameExists := false
	if name != "" {
		_, err := getPlayerByName(app.db, name)
		nameExists = err == nil
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.templates.ExecuteTemplate(w, "auth-control", struct {
		NameExists bool
		Lang       string
	}{nameExists, lang}); err != nil {
		app.logf("handleCheckName: ExecuteTemplate: %v", err)
	}
}

// handleSignin is the single endpoint behind the unified sign-in form: it creates a
// new account if the name doesn't exist yet, or verifies the secret code if it does.
// Deciding signup-vs-login here (not on the client) means a race between the live
// /check-name lookup and the actual submit can't create a duplicate or bad login.
func (app *App) handleSignin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	lang := getLangFromCookie(r)
	toast := func(key string) {
		w.Header().Set("HX-Reswap", "none")
		w.Write([]byte(renderToast(app.templates, app.logf, "error", T(lang, key))))
	}

	gameName := r.FormValue("game_name")
	name := r.FormValue("name")
	if name == "" {
		toast("err_name_required")
		return
	}
	secretCode := r.FormValue("secret_code")

	existing, lookupErr := getPlayerByName(app.db, name)

	var playerID int64
	switch {
	case lookupErr == sql.ErrNoRows:
		newSecret, err := generateSecretCode()
		if err != nil {
			app.logf("ERROR [handleSignin: generateSecretCode]: %v", err)
			toast("err_something_wrong")
			return
		}
		result, err := app.db.Exec("INSERT INTO player (name, secret_code) VALUES (?, ?)", name, newSecret)
		if err != nil {
			app.logf("ERROR [handleSignin: db.Exec insert player]: %v", err)
			toast("err_something_wrong")
			return
		}
		playerID, _ = result.LastInsertId()
		app.logf("New player created: name='%s', id=%d", name, playerID)
		DebugLog("handleSignin", "Player '%s' signed up with ID %d", name, playerID)
		LogDBState(app.db, "after signup: "+name)
	case lookupErr != nil:
		app.logf("ERROR [handleSignin: db.Get player]: %v", lookupErr)
		toast("err_something_wrong")
		return
	default:
		// Name already taken — require the matching secret code to log in.
		if secretCode == "" {
			toast("err_name_taken")
			return
		}
		if secretCode != existing.SecretCode {
			toast("err_invalid_credentials")
			return
		}
		playerID = existing.ID
		app.logf("Player logged in: name='%s', id=%d", name, playerID)
		DebugLog("handleSignin", "Player '%s' logged in with ID %d", name, playerID)
	}

	if err := setSessionCookie(app.db, w, playerID); err != nil {
		app.logf("ERROR [handleSignin: setSessionCookie]: %v", err)
		toast("err_something_wrong")
		return
	}
	redirectTarget := "/"
	if gameName != "" {
		redirectTarget = "/game/" + gameName
	}
	w.Header().Set("HX-Redirect", redirectTarget)
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
	playerName := getPlayerName(app.db, playerID)

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
