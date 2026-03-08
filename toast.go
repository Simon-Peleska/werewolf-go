package main

import (
	"bytes"
	"html/template"
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

func renderToast(tmpl *template.Template, toastType, message string) string {
	var buf bytes.Buffer
	toastCounter++
	toast := Toast{ID: strconv.FormatInt(toastCounter, 10), Type: toastType, Message: message}
	if err := tmpl.ExecuteTemplate(&buf, "toast.html", toast); err != nil {
		log.Printf("Failed to render toast: %v", err)
		return ""
	}
	return buf.String()
}

// sendErrorToast sends an error toast to a specific player via WebSocket
func (h *Hub) sendErrorToast(playerID int64, message string) {
	html := renderToast(h.templates, "error", message)
	if html != "" {
		h.sendToPlayer(playerID, []byte(html))
	}
}

// broadcastSoundToast sends a toast with sound+vibration to all connected clients
func (h *Hub) broadcastSoundToast(toastType, message string) {
	var buf bytes.Buffer
	toastCounter++
	toast := Toast{ID: strconv.FormatInt(toastCounter, 10), Type: toastType, Message: message, Sound: true}
	if err := h.templates.ExecuteTemplate(&buf, "toast.html", toast); err != nil {
		log.Printf("Failed to render sound toast: %v", err)
		return
	}
	h.broadcast <- buf.Bytes()
}
