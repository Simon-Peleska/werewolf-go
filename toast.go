package main

import (
	"bytes"
	"log"
	"strconv"
)

// Toast represents a notification message to show to the user
type Toast struct {
	ID      string
	Type    string // "error", "warning", "success", "info"
	Message string
	Sound   bool // if true, triggers client-side sound + vibration
}

// renderToast renders a toast notification HTML fragment
var toastCounter int64

func renderToast(toastType, message string) string {
	var buf bytes.Buffer
	toastCounter++
	toast := Toast{ID: strconv.FormatInt(toastCounter, 10), Type: toastType, Message: message}
	if err := templates.ExecuteTemplate(&buf, "toast.html", toast); err != nil {
		log.Printf("Failed to render toast: %v", err)
		return ""
	}
	return buf.String()
}

// sendErrorToast sends an error toast to a specific player via WebSocket
func sendErrorToast(playerID int64, message string) {
	html := renderToast("error", message)
	if html != "" {
		hub.sendToPlayer(playerID, []byte(html))
	}
}

// broadcastSoundToast sends a toast with sound+vibration to all connected clients
func broadcastSoundToast(toastType, message string) {
	var buf bytes.Buffer
	toastCounter++
	toast := Toast{ID: strconv.FormatInt(toastCounter, 10), Type: toastType, Message: message, Sound: true}
	if err := templates.ExecuteTemplate(&buf, "toast.html", toast); err != nil {
		log.Printf("Failed to render sound toast: %v", err)
		return
	}
	hub.broadcast <- buf.Bytes()
}
