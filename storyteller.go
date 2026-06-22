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

type Storyteller interface {
	Tell(ctx context.Context, systemPrompt, userPrompt string, onChunk func(string)) (string, error)
}

type openaiStoryteller struct {
	baseURL     string
	apiKey      string
	model       string
	temperature float64
	maxTokens   int
	extraParams map[string]any
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

func (h *Hub) streamStory(gameID int64, round int, phase string, actorPlayerID int64, systemPrompt, userPrompt string, timeout time.Duration) {
	// empty description hides the row from history until text arrives
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
		return // row for this slot already exists
	}

	// mu guards buf between the flush ticker and the onChunk callback below
	var mu sync.Mutex
	var buf strings.Builder

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

	// one goroutine drains sentenceCh so sentences are spoken in order, never overlapping
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

	// no mutex: Tell calls onChunk synchronously, so only this goroutine ever touches sentenceBuf
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

	// last sentence has no trailing punctuation, so nextSentence never caught it
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
		// no visibility filter: game's over, the storyteller gets the full picture
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

		h.streamStory(gameID, round, "finished", players[0].PlayerID, systemPrompt, userPrompt, 60*time.Second)
	}()
}

func initStoryteller(cfg AppConfig) Storyteller {
	if !cfg.Storyteller {
		log.Printf("Storyteller: disabled")
		return nil
	}

	temperature := 0.7
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

// maybeGenerateStory is fire-and-forget; story tokens appear progressively via broadcastGameUpdate.
func (h *Hub) maybeGenerateStory(gameID int64, round int, phase string, actorPlayerID int64) {
	if h.storyteller == nil || !h.aiEnabled(gameID) {
		return
	}

	go func() {
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
			return
		}

		h.logf("Narrator: TTS completed for game %d", gameID)
	}()
}
