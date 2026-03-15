package main

import (
	"bytes"
	"html/template"
	"strconv"
)

// Toast represents a notification message to show to the user
type Toast struct {
	ID      string
	Type    string // "error", "warning", "success", "info"
	Message string
}

// renderToast renders a toast notification HTML fragment
var toastCounter int64

func renderToast(tmpl *template.Template, logfn func(string, ...any), toastType, message string) string {
	var buf bytes.Buffer
	toastCounter++
	toast := Toast{ID: strconv.FormatInt(toastCounter, 10), Type: toastType, Message: message}
	if err := tmpl.ExecuteTemplate(&buf, "toast.html", toast); err != nil {
		logfn("Failed to render toast: %v", err)
		return ""
	}
	return buf.String()
}

// sendErrorToast sends an error toast to a specific player via WebSocket
func (h *Hub) sendErrorToast(playerID int64, message string) {
	html := renderToast(h.templates, h.logf, "error", message)
	if html != "" {
		h.sendToPlayer(playerID, []byte(html))
	}
}
