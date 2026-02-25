package main

import (
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/anthropic"
	"github.com/tmc/langchaingo/llms/googleai"
	"github.com/tmc/langchaingo/llms/ollama"
	"github.com/tmc/langchaingo/llms/openai"
)

const storytellerSystemPrompt = `You are a dramatic storyteller for a medieval werewolf game. When players are killed, you tell a short atmospheric story about their fate. Keep it to 2-3 sentences. Be gothic and dramatic, fitting for a village plagued by werewolves.`

// Storyteller generates a dramatic story after deaths in the game.
// onChunk is called with each text chunk as it streams in.
type Storyteller interface {
	Tell(ctx context.Context, history []string, onChunk func(string)) (string, error)
}

// globalStoryteller is nil when no provider is configured (feature disabled).
var globalStoryteller Storyteller

type llmStoryteller struct {
	llm          llms.Model
	systemPrompt string
	callOpts     []llms.CallOption
}

func (s *llmStoryteller) Tell(ctx context.Context, history []string, onChunk func(string)) (string, error) {
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, s.systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman,
			"Game history so far:\n"+strings.Join(history, "\n")+
				"\n\nTell a short dramatic story (2-3 sentences) about what just happened to the victim."),
	}

	var fullText strings.Builder
	opts := append(s.callOpts, llms.WithStreamingFunc(func(_ context.Context, chunk []byte) error {
		text := string(chunk)
		fullText.WriteString(text)
		if onChunk != nil {
			onChunk(text)
		}
		return nil
	}))

	_, err := s.llm.GenerateContent(ctx, messages, opts...)
	return strings.TrimSpace(fullText.String()), err
}

// buildCallOpts builds LLM call options from the config.
func buildCallOpts(cfg AppConfig) []llms.CallOption {
	var opts []llms.CallOption

	if cfg.StorytellerTemperature != "" {
		if f, err := strconv.ParseFloat(cfg.StorytellerTemperature, 64); err == nil {
			opts = append(opts, llms.WithTemperature(f))
			log.Printf("Storyteller: temperature=%.2f", f)
		} else {
			log.Printf("Storyteller: invalid temperature %q: %v", cfg.StorytellerTemperature, err)
		}
	}

	if cfg.StorytellerThinking != "" {
		mode := llms.ThinkingMode(cfg.StorytellerThinking)
		switch mode {
		case llms.ThinkingModeNone, llms.ThinkingModeLow, llms.ThinkingModeMedium, llms.ThinkingModeHigh, llms.ThinkingModeAuto:
			opts = append(opts, llms.WithThinkingMode(mode))
			log.Printf("Storyteller: thinking=%s", mode)
		default:
			log.Printf("Storyteller: invalid thinking %q (valid: none, low, medium, high, auto)", cfg.StorytellerThinking)
		}
	}

	return opts
}

// initStoryteller sets up the global storyteller from config.
func initStoryteller(cfg AppConfig) {
	provider := cfg.StorytellerProvider
	model := cfg.StorytellerModel
	callOpts := buildCallOpts(cfg)

	switch provider {
	case "ollama":
		llm, err := ollama.New(ollama.WithModel(model), ollama.WithServerURL(cfg.StorytellerOllamaURL))
		if err != nil {
			log.Printf("Storyteller: failed to init Ollama (%s at %s): %v", model, cfg.StorytellerOllamaURL, err)
			return
		}
		globalStoryteller = &llmStoryteller{llm: llm, systemPrompt: storytellerSystemPrompt, callOpts: callOpts}
		log.Printf("Storyteller: Ollama model=%s url=%s", model, cfg.StorytellerOllamaURL)
	case "openai":
		llm, err := openai.New(openai.WithModel(model))
		if err != nil {
			log.Printf("Storyteller: failed to init OpenAI (%s): %v", model, err)
			return
		}
		globalStoryteller = &llmStoryteller{llm: llm, systemPrompt: storytellerSystemPrompt, callOpts: callOpts}
		log.Printf("Storyteller: OpenAI model=%s", model)
	case "claude":
		llm, err := anthropic.New(anthropic.WithModel(model))
		if err != nil {
			log.Printf("Storyteller: failed to init Claude (%s): %v", model, err)
			return
		}
		globalStoryteller = &llmStoryteller{llm: llm, systemPrompt: storytellerSystemPrompt, callOpts: callOpts}
		log.Printf("Storyteller: Claude model=%s", model)
	case "gemini":
		llm, err := googleai.New(context.Background(), googleai.WithDefaultModel(model))
		if err != nil {
			log.Printf("Storyteller: failed to init Gemini (%s): %v", model, err)
			return
		}
		globalStoryteller = &llmStoryteller{llm: llm, systemPrompt: storytellerSystemPrompt, callOpts: callOpts}
		log.Printf("Storyteller: Gemini model=%s", model)
	case "groq":
		llm, err := openai.New(
			openai.WithModel(model),
			openai.WithBaseURL("https://api.groq.com/openai/v1"),
			openai.WithToken(cfg.GroqAPIKey),
		)
		if err != nil {
			log.Printf("Storyteller: failed to init Groq (%s): %v", model, err)
			return
		}
		globalStoryteller = &llmStoryteller{llm: llm, systemPrompt: storytellerSystemPrompt, callOpts: callOpts}
		log.Printf("Storyteller: Groq model=%s", model)
	case "openai-compatible":
		if cfg.StorytellerURL == "" {
			log.Printf("Storyteller: storyteller_url is required for openai-compatible provider")
			return
		}
		opts := []openai.Option{
			openai.WithModel(model),
			openai.WithBaseURL(cfg.StorytellerURL),
		}
		if cfg.StorytellerAPIKey != "" {
			opts = append(opts, openai.WithToken(cfg.StorytellerAPIKey))
		}
		llm, err := openai.New(opts...)
		if err != nil {
			log.Printf("Storyteller: failed to init openai-compatible (%s at %s): %v", model, cfg.StorytellerURL, err)
			return
		}
		globalStoryteller = &llmStoryteller{llm: llm, systemPrompt: storytellerSystemPrompt, callOpts: callOpts}
		log.Printf("Storyteller: openai-compatible model=%s url=%s", model, cfg.StorytellerURL)
	default:
		log.Printf("Storyteller: disabled (set storyteller_provider to enable)")
	}
}

// maybeGenerateStory asynchronously streams a story into the game history after a death.
// Returns immediately; story tokens appear progressively via broadcastGameUpdate.
// actorPlayerID must be a valid player rowid (typically the victim's ID).
func maybeGenerateStory(gameID int64, round int, phase string, actorPlayerID int64) {
	if globalStoryteller == nil {
		return
	}

	go func() {
		// Fetch all public history at this point in time
		var descriptions []string
		if err := db.Select(&descriptions, `
			SELECT description FROM game_action
			WHERE game_id = ? AND description != '' AND visibility = ?
			ORDER BY rowid ASC`, gameID, VisibilityPublic); err != nil {
			log.Printf("maybeGenerateStory: fetch history: %v", err)
			return
		}

		// Insert placeholder row (empty description = hidden from history until text arrives)
		result, err := db.Exec(`
			INSERT OR IGNORE INTO game_action
				(game_id, round, phase, actor_player_id, action_type, visibility, description)
			VALUES (?, ?, ?, ?, ?, ?, '')`,
			gameID, round, phase, actorPlayerID, ActionStory, VisibilityPublic)
		if err != nil {
			logError("maybeGenerateStory: insert placeholder", err)
			return
		}
		storyRowID, _ := result.LastInsertId()
		if storyRowID == 0 {
			return // row already exists (shouldn't happen)
		}

		// Buffer for streamed tokens, updated by the streaming callback
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
						db.Exec(`UPDATE game_action SET description=? WHERE rowid=?`, strings.TrimSpace(text), storyRowID)
						broadcastGameUpdate()
					}
				case <-done:
					return
				}
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		_, err = globalStoryteller.Tell(ctx, descriptions, func(chunk string) {
			mu.Lock()
			buf.WriteString(chunk)
			mu.Unlock()
		})

		close(done)

		if err != nil {
			log.Printf("maybeGenerateStory: storyteller error: %v", err)
			db.Exec(`DELETE FROM game_action WHERE rowid=? AND description=''`, storyRowID)
			return
		}

		// Final flush with trimmed complete text
		mu.Lock()
		finalText := strings.TrimSpace(buf.String())
		mu.Unlock()

		if finalText == "" {
			db.Exec(`DELETE FROM game_action WHERE rowid=?`, storyRowID)
			return
		}

		db.Exec(`UPDATE game_action SET description=? WHERE rowid=?`, finalText, storyRowID)
		log.Printf("Storyteller: completed story for game %d round %d %s", gameID, round, phase)
		broadcastGameUpdate()
	}()
}
