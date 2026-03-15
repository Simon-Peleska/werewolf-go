package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const storytellerSystemPrompt = `You are a dramatic storyteller for a medieval werewolf game. When players are killed, you tell a short atmospheric story about their fate. Keep it to 2-3 sentences. Be gothic and dramatic, fitting for a village plagued by werewolves.`

// Storyteller generates a dramatic story after deaths in the game.
// onChunk is called with each text chunk as it streams in.
type Storyteller interface {
	Tell(ctx context.Context, history []string, onChunk func(string)) (string, error)
}

// ── OpenAI-compatible ────────────────────────────────────────────────────────
// Works with OpenAI, Ollama, Groq, and any server that speaks the
// POST /v1/chat/completions SSE streaming protocol.

type openaiStoryteller struct {
	baseURL     string
	apiKey      string
	model       string
	temperature float64
	hasTemp     bool
}

func (s *openaiStoryteller) Tell(ctx context.Context, history []string, onChunk func(string)) (string, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	body := map[string]any{
		"model":  s.model,
		"stream": true,
		"messages": []message{
			{Role: "system", Content: storytellerSystemPrompt},
			{Role: "user", Content: buildUserPrompt(history)},
		},
	}
	if s.hasTemp {
		body["temperature"] = s.temperature
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

// ── Claude-compatible ────────────────────────────────────────────────────────
// Talks to the Anthropic Messages API (POST /v1/messages) with SSE streaming.
// Also supports extended thinking via budget_tokens.

type claudeStoryteller struct {
	baseURL        string
	apiKey         string
	model          string
	temperature    float64
	hasTemp        bool
	thinkingBudget int // 0 = disabled; >0 enables extended thinking
}

func (s *claudeStoryteller) Tell(ctx context.Context, history []string, onChunk func(string)) (string, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	body := map[string]any{
		"model":      s.model,
		"max_tokens": 1024,
		"stream":     true,
		"system":     storytellerSystemPrompt,
		"messages":   []message{{Role: "user", Content: buildUserPrompt(history)}},
	}
	if s.thinkingBudget > 0 {
		// Extended thinking requires temperature=1
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": s.thinkingBudget}
		body["temperature"] = 1
	} else if s.hasTemp {
		body["temperature"] = s.temperature
	}

	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("claude API %s: %s", resp.Status, b)
	}

	var full strings.Builder
	var lastEvent string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			lastEvent = strings.TrimPrefix(line, "event: ")
			continue
		}
		if lastEvent != "content_block_delta" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var chunk struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		// Skip thinking blocks; only emit text_delta
		if chunk.Delta.Type == "text_delta" && chunk.Delta.Text != "" {
			full.WriteString(chunk.Delta.Text)
			if onChunk != nil {
				onChunk(chunk.Delta.Text)
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

func buildUserPrompt(history []string) string {
	return "Game history so far:\n" + strings.Join(history, "\n") +
		"\n\nTell a short dramatic story (2-3 sentences) about what just happened to the victim."
}

// thinkingBudget maps a thinking-mode string to a token budget for Claude.
func thinkingBudget(mode string) int {
	switch mode {
	case "low":
		return 2000
	case "medium":
		return 8000
	case "high":
		return 32000
	case "auto":
		return 16000
	default:
		return 0
	}
}

// initStoryteller creates a Storyteller from config, or returns nil if disabled.
func initStoryteller(cfg AppConfig) Storyteller {
	model := cfg.StorytellerModel

	var temperature float64
	var hasTemp bool
	if cfg.StorytellerTemperature != "" {
		if f, err := strconv.ParseFloat(cfg.StorytellerTemperature, 64); err == nil {
			temperature = f
			hasTemp = true
		} else {
			log.Printf("Storyteller: invalid temperature %q: %v", cfg.StorytellerTemperature, err)
		}
	}

	switch cfg.StorytellerProvider {
	case "openai":
		baseURL := strings.TrimRight(cfg.StorytellerURL, "/")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		log.Printf("Storyteller: openai model=%s url=%s temperature=%v", model, baseURL, cfg.StorytellerTemperature)
		return &openaiStoryteller{baseURL: baseURL, apiKey: cfg.StorytellerAPIKey, model: model, temperature: temperature, hasTemp: hasTemp}

	case "claude":
		baseURL := strings.TrimRight(cfg.StorytellerURL, "/")
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		budget := thinkingBudget(cfg.StorytellerThinking)
		log.Printf("Storyteller: claude model=%s url=%s temperature=%v thinking=%s", model, baseURL, cfg.StorytellerTemperature, cfg.StorytellerThinking)
		return &claudeStoryteller{baseURL: baseURL, apiKey: cfg.StorytellerAPIKey, model: model, temperature: temperature, hasTemp: hasTemp, thinkingBudget: budget}

	default:
		log.Printf("Storyteller: disabled (set storyteller_provider to openai or claude)")
		return nil
	}
}

// ── Story generation ─────────────────────────────────────────────────────────

// maybeGenerateStory asynchronously streams a story into the game history after a death.
// Returns immediately; story tokens appear progressively via broadcastGameUpdate.
// actorPlayerID must be a valid player rowid (typically the victim's ID).
func (h *Hub) maybeGenerateStory(gameID int64, round int, phase string, actorPlayerID int64) {
	if h.storyteller == nil {
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

		// Insert placeholder row (empty description = hidden from history until text arrives)
		result, err := h.db.Exec(`
			INSERT OR IGNORE INTO game_action
				(game_id, round, phase, actor_player_id, action_type, visibility, description)
			VALUES (?, ?, ?, ?, ?, ?, '')`,
			gameID, round, phase, actorPlayerID, ActionStory, VisibilityPublic)
		if err != nil {
			h.logError("maybeGenerateStory: insert placeholder", err)
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

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// sentenceBuf accumulates tokens until a sentence boundary is detected.
		// Tell is blocking and calls onChunk synchronously, so no mutex needed here.
		var sentenceBuf strings.Builder
		_, err = h.storyteller.Tell(ctx, descriptions, func(chunk string) {
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
			h.logf("maybeGenerateStory: storyteller error: %v", err)
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
	}()
}

// maybeSpeakStory asynchronously narrates text as audio streamed to all connected clients.
func (h *Hub) maybeSpeakStory(gameID int64, text string) {
	if h.narrator == nil {
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
