package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Storyteller generates a dramatic story after deaths in the game.
// systemPrompt is built per-call (see prompt.go) so it can reflect live game
// state. onChunk is called with each text chunk as it streams in.
type Storyteller interface {
	Tell(ctx context.Context, systemPrompt, userPrompt string, onChunk func(string)) (string, error)
}

// ── OpenAI-compatible ────────────────────────────────────────────────────────
// Works with OpenAI, Ollama, Groq, and any server that speaks the
// POST /v1/chat/completions SSE streaming protocol.

type openaiStoryteller struct {
	baseURL     string
	apiKey      string
	model       string
	temperature float64
	maxTokens   int
	extraParams map[string]any // additional JSON fields merged into every request body (e.g. OpenRouter's "provider", "top_p")
}

func (s *openaiStoryteller) Tell(ctx context.Context, systemPrompt, userPrompt string, onChunk func(string)) (string, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	body := map[string]any{}
	maps.Copy(body, s.extraParams)
	body["model"] = s.model
	body["stream"] = true
	body["temperature"] = s.temperature
	body["max_tokens"] = s.maxTokens
	body["messages"] = []message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai API %s: %s", resp.Status, b)
	}

	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			text := chunk.Choices[0].Delta.Content
			full.WriteString(text)
			if onChunk != nil {
				onChunk(text)
			}
		}
	}
	return strings.TrimSpace(full.String()), scanner.Err()
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// nextSentence pulls the first complete sentence from text.
// A sentence ends at . ! ? (with optional repeated punctuation) followed by
// whitespace or end of string. Returns ("", text) if no boundary found yet.
func nextSentence(text string) (sentence, rest string) {
	for i := 0; i < len(text); i++ {
		if text[i] != '.' && text[i] != '!' && text[i] != '?' {
			continue
		}
		end := i + 1
		for end < len(text) && (text[end] == '.' || text[end] == '!' || text[end] == '?') {
			end++
		}
		if end >= len(text) || text[end] == ' ' || text[end] == '\n' {
			return strings.TrimSpace(text[:end]), strings.TrimSpace(text[end:])
		}
	}
	return "", text
}

// streamStory runs the full storyteller pipeline synchronously (call it from a
// goroutine): insert a placeholder history row, stream the LLM response into it
// (flushing to DB + clients every 300ms), pipe completed sentences to the
// narrator, then finalize the row or clean it up on error/empty.
// Callers must have already checked h.storyteller != nil && h.aiEnabled(gameID).
func (h *Hub) streamStory(gameID int64, round int, phase string, actorPlayerID int64, systemPrompt, userPrompt string, timeout time.Duration) {
	// Insert placeholder row (empty description = hidden from history until text arrives)
	result, err := h.db.Exec(`
		INSERT OR IGNORE INTO game_action
			(game_id, round, phase, actor_player_id, action_type, visibility, description)
		VALUES (?, ?, ?, ?, ?, ?, '')`,
		gameID, round, phase, actorPlayerID, ActionStory, VisibilityPublic)
	if err != nil {
		h.logError("streamStory: insert placeholder", err)
		return
	}
	storyRowID, _ := result.LastInsertId()
	if storyRowID == 0 {
		return // row already exists (shouldn't happen)
	}

	// Buffer for streamed tokens (for DB flush)
	var mu sync.Mutex
	var buf strings.Builder

	// Flush goroutine: pushes partial text to DB and clients every 300ms
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				text := buf.String()
				mu.Unlock()
				if text != "" {
					h.db.Exec(`UPDATE game_action SET description=? WHERE rowid=?`, strings.TrimSpace(text), storyRowID)
					h.triggerBroadcast()
				}
			case <-done:
				return
			}
		}
	}()

	// Sentence-by-sentence TTS: one goroutine drains the channel sequentially
	// so sentences are always spoken in order without gaps or overlap.
	var sentenceCh chan string
	if h.narrator != nil {
		sentenceCh = make(chan string, 8)
		go func() {
			for sentence := range sentenceCh {
				ttsCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				err := h.narrator.Speak(ttsCtx, sentence, func(chunk []byte) {
					h.broadcastAudio(chunk)
				})
				cancel()
				if err != nil {
					h.logf("Narrator: TTS error: %v", err)
				}
			}
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// sentenceBuf accumulates tokens until a sentence boundary is detected.
	// Tell is blocking and calls onChunk synchronously, so no mutex needed here.
	var sentenceBuf strings.Builder
	_, err = h.storyteller.Tell(ctx, systemPrompt, userPrompt, func(chunk string) {
		mu.Lock()
		buf.WriteString(chunk)
		mu.Unlock()

		if sentenceCh != nil {
			sentenceBuf.WriteString(chunk)
			for {
				sentence, rest := nextSentence(sentenceBuf.String())
				if sentence == "" {
					break
				}
				sentenceCh <- sentence
				sentenceBuf.Reset()
				sentenceBuf.WriteString(rest)
			}
		}
	})

	close(done)

	// Flush any remaining text (last sentence without trailing punctuation)
	if sentenceCh != nil {
		if err == nil {
			if remaining := strings.TrimSpace(sentenceBuf.String()); remaining != "" {
				sentenceCh <- remaining
			}
		}
		close(sentenceCh)
	}

	if err != nil {
		h.logf("streamStory: storyteller error: %v", err)
		h.db.Exec(`DELETE FROM game_action WHERE rowid=? AND description=''`, storyRowID)
		return
	}

	// Final flush with trimmed complete text
	mu.Lock()
	finalText := strings.TrimSpace(buf.String())
	mu.Unlock()

	if finalText == "" {
		h.db.Exec(`DELETE FROM game_action WHERE rowid=?`, storyRowID)
		return
	}

	h.db.Exec(`UPDATE game_action SET description=? WHERE rowid=?`, finalText, storyRowID)
	h.logf("Storyteller: completed story for game %d round %d %s", gameID, round, phase)
	h.triggerBroadcast()
}

// maybeGenerateEnding generates a dramatic AI-written game summary and speaks it via TTS.
// Falls back to a static maybeSpeakStory announcement if no storyteller is configured.
func (h *Hub) maybeGenerateEnding(gameID int64, round int, winner string) {
	if !h.aiEnabled(gameID) {
		return
	}
	if h.storyteller == nil {
		switch winner {
		case "villagers":
			h.maybeSpeakStory(gameID, T(h.storytellerLang, "tts_villagers_win"))
		case "werewolves":
			h.maybeSpeakStory(gameID, T(h.storytellerLang, "tts_werewolves_win"))
		case "lovers":
			h.maybeSpeakStory(gameID, T(h.storytellerLang, "tts_lovers_win"))
		}
		return
	}

	go func() {
		// Fetch ALL actions — game is over, storyteller gets the full picture including private events
		var descriptions []string
		if err := h.db.Select(&descriptions, `
			SELECT description FROM game_action
			WHERE game_id = ? AND description != ''
			ORDER BY rowid ASC`, gameID); err != nil {
			h.logf("maybeGenerateEnding: fetch history: %v", err)
			return
		}

		players, err := getPlayersByGameId(h.db, gameID)
		if err != nil || len(players) == 0 {
			h.logf("maybeGenerateEnding: fetch players: %v", err)
			return
		}

		systemPrompt := h.buildGameSystemPrompt(gameID)
		userPrompt := buildUserPrompt(descriptions, players, winner)

		// players[0] satisfies the NOT NULL actor_player_id FK constraint.
		h.streamStory(gameID, round, "finished", players[0].PlayerID, systemPrompt, userPrompt, 60*time.Second)
	}()
}

// initStoryteller creates a Storyteller from config, or returns nil if disabled.
func initStoryteller(cfg AppConfig) Storyteller {
	if !cfg.Storyteller {
		log.Printf("Storyteller: disabled")
		return nil
	}

	// Default temperature: 1.2 (above average for more varied storytelling)
	temperature := 1.2
	if cfg.StorytellerTemperature != "" {
		if f, err := strconv.ParseFloat(cfg.StorytellerTemperature, 64); err == nil {
			temperature = f
		} else {
			log.Printf("Storyteller: invalid temperature %q: %v", cfg.StorytellerTemperature, err)
		}
	}

	var extraParams map[string]any
	if cfg.StorytellerExtraParams != "" {
		if err := json.Unmarshal([]byte(cfg.StorytellerExtraParams), &extraParams); err != nil {
			log.Printf("Storyteller: invalid storyteller_extra_params JSON, ignoring: %v", err)
			extraParams = nil
		} else {
			log.Printf("Storyteller: extra params: %s", cfg.StorytellerExtraParams)
		}
	}

	baseURL := strings.TrimRight(cfg.OpenAIAPIBase, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	maxTokens := cfg.StorytellerMaxTokens
	if maxTokens <= 0 {
		maxTokens = 600
	}

	log.Printf("Storyteller: model=%s url=%s temperature=%.2f max_tokens=%d", cfg.OpenAIModel, baseURL, temperature, maxTokens)
	return &openaiStoryteller{baseURL: baseURL, apiKey: cfg.OpenAIAPIKey, model: cfg.OpenAIModel, temperature: temperature, maxTokens: maxTokens, extraParams: extraParams}
}

// ── Story generation ─────────────────────────────────────────────────────────

// maybeGenerateStory asynchronously streams a story into the game history after a death.
// Returns immediately; story tokens appear progressively via broadcastGameUpdate.
// actorPlayerID must be a valid player rowid (typically the victim's ID).
func (h *Hub) maybeGenerateStory(gameID int64, round int, phase string, actorPlayerID int64) {
	if h.storyteller == nil || !h.aiEnabled(gameID) {
		return
	}

	go func() {
		// Fetch all public history at this point in time
		var descriptions []string
		if err := h.db.Select(&descriptions, `
			SELECT description FROM game_action
			WHERE game_id = ? AND description != '' AND visibility = ?
			ORDER BY rowid ASC`, gameID, VisibilityPublic); err != nil {
			h.logf("maybeGenerateStory: fetch history: %v", err)
			return
		}

		players, err := getPlayersByGameId(h.db, gameID)
		if err != nil {
			h.logf("maybeGenerateStory: fetch players: %v", err)
		}

		systemPrompt := h.buildGameSystemPrompt(gameID)
		userPrompt := buildUserPrompt(descriptions, players, "")

		h.streamStory(gameID, round, phase, actorPlayerID, systemPrompt, userPrompt, 30*time.Second)
	}()
}

// maybeSpeakStory asynchronously narrates text as audio streamed to all connected clients.
func (h *Hub) maybeSpeakStory(gameID int64, text string) {
	if h.narrator == nil || !h.aiEnabled(gameID) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		h.logf("Narrator: starting TTS for game %d", gameID)
		err := h.narrator.Speak(ctx, text, func(chunk []byte) {
			h.broadcastAudio(chunk)
		})
		if err != nil {
			h.logf("Narrator: TTS error for game %d: %v", gameID, err)
		} else {
			h.logf("Narrator: TTS completed for game %d", gameID)
		}
	}()
}
