package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// Narrator streams TTS audio as raw PCM chunks (16-bit mono little-endian).
type Narrator interface {
	Speak(ctx context.Context, text string, onChunk func([]byte)) error
	SampleRate() int
}

// geminiTTSModel honours inline bracketed style tags like "[happy]".
const geminiTTSModel = "google/gemini-3.1-flash-tts-preview"

// openaiNarrator streams PCM from the OpenAI TTS API (or any openai-compatible endpoint).
// OpenAI's PCM output is 24kHz, 16-bit, mono, little-endian.
type openaiNarrator struct {
	apiKey     string
	baseURL    string
	model      string
	voice      string
	sampleRate int
}

func (n *openaiNarrator) SampleRate() int { return n.sampleRate }

func (n *openaiNarrator) Speak(ctx context.Context, text string, onChunk func([]byte)) error {
	// Gemini's TTS preview honours an inline style tag; ask it for Austrian
	// High German. Other models would just read the tag aloud, so gate on it.
	if n.model == geminiTTSModel {
		rand.New(rand.NewSource(time.Now().UnixNano()))
		accents := []string{
			"[Österreichisches Hochdeutsch, tiefe Männerstimme, ernster Prophet] ",
			// "[русский] ",
			// "[dansk] ",
			// "[ᐃᓄᒃᑎᑐᑦ] ",
			// "[日本語] ",
		}
		rn := rand.Int() % len(accents)

		text = accents[rn] + text
	}

	body, _ := json.Marshal(map[string]any{
		"model":           n.model,
		"input":           text,
		"voice":           n.voice,
		"response_format": "pcm",
	})

	req, err := http.NewRequestWithContext(ctx, "POST", n.baseURL+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+n.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("TTS API %s: %s", resp.Status, errBody)
	}

	return streamPCM(resp.Body, onChunk)
}

type elevenlabsNarrator struct {
	apiKey       string
	voiceID      string
	outputFormat string
	sampleRate   int
}

func (n *elevenlabsNarrator) SampleRate() int { return n.sampleRate }

func (n *elevenlabsNarrator) Speak(ctx context.Context, text string, onChunk func([]byte)) error {
	url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s/stream?output_format=%s",
		n.voiceID, n.outputFormat)

	body, _ := json.Marshal(map[string]any{
		"text":     text,
		"model_id": "eleven_multilingual_v2",
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("xi-api-key", n.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ElevenLabs TTS %s: %s", resp.Status, errBody)
	}

	return streamPCM(resp.Body, onChunk)
}

// Chunks are always an even number of bytes so Int16Array on the frontend
// never straddles a sample boundary and causes misalignment static.
func streamPCM(body io.Reader, onChunk func([]byte)) error {
	buf := make([]byte, 4096)
	var leftover byte
	hasLeftover := false
	for {
		n, err := body.Read(buf)
		if n > 0 {
			data := buf[:n]
			if hasLeftover {
				// Prepend the held-back byte from the previous read.
				combined := make([]byte, 1+n)
				combined[0] = leftover
				copy(combined[1:], data)
				data = combined
				hasLeftover = false
			}
			if len(data)%2 != 0 {
				// Hold back the trailing odd byte for the next chunk.
				leftover = data[len(data)-1]
				hasLeftover = true
				data = data[:len(data)-1]
			}
			if len(data) > 0 {
				chunk := make([]byte, len(data))
				copy(chunk, data)
				onChunk(chunk)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func initNarrator(cfg AppConfig) Narrator {
	sr := cfg.NarratorSampleRate
	if sr == 0 {
		sr = 24000
	}

	switch cfg.NarratorProvider {
	case "openai":
		// Fall back to the storyteller's key when the narrator one is unset —
		// handy when both point at the same OpenAI account.
		apiKey := cfg.NarratorAPIKey
		if apiKey == "" {
			apiKey = cfg.OpenAIAPIKey
		}
		if apiKey == "" {
			log.Printf("Narrator: NARRATOR_API_KEY or OPENAI_API_KEY required for openai provider")
			return nil
		}
		model := cfg.NarratorModel
		if model == "" {
			model = "tts-1"
		}
		voice := cfg.NarratorVoice
		if voice == "" {
			voice = "onyx"
		}
		log.Printf("Narrator: OpenAI TTS model=%s voice=%s sampleRate=%d", model, voice, sr)
		return &openaiNarrator{
			apiKey:     apiKey,
			baseURL:    "https://api.openai.com/v1",
			model:      model,
			voice:      voice,
			sampleRate: sr,
		}

	case "openai-compatible":
		// Fall back to the storyteller's endpoint/key when the narrator ones
		// are unset — handy when both point at the same OpenAI-compatible server.
		url := cfg.NarratorURL
		if url == "" {
			url = cfg.OpenAIAPIBase
		}
		if url == "" {
			log.Printf("Narrator: NARRATOR_URL or OPENAI_API_BASE required for openai-compatible provider")
			return nil
		}
		url = strings.TrimRight(url, "/")
		apiKey := cfg.NarratorAPIKey
		if apiKey == "" {
			apiKey = cfg.OpenAIAPIKey
		}
		model := cfg.NarratorModel
		if model == "" {
			model = "tts-1"
		}
		voice := cfg.NarratorVoice
		if voice == "" {
			voice = "default"
		}
		log.Printf("Narrator: openai-compatible TTS url=%s model=%s voice=%s sampleRate=%d", url, model, voice, sr)
		return &openaiNarrator{
			apiKey:     apiKey,
			baseURL:    url,
			model:      model,
			voice:      voice,
			sampleRate: sr,
		}

	case "elevenlabs":
		if cfg.NarratorAPIKey == "" {
			log.Printf("Narrator: NARRATOR_API_KEY and NARRATOR_VOICE (voice ID) required for elevenlabs")
			return nil
		}
		outputFormat := fmt.Sprintf("pcm_%d", sr)
		log.Printf("Narrator: ElevenLabs TTS voice=%s outputFormat=%s", cfg.NarratorVoice, outputFormat)
		return &elevenlabsNarrator{
			apiKey:       cfg.NarratorAPIKey,
			voiceID:      cfg.NarratorVoice,
			outputFormat: outputFormat,
			sampleRate:   sr,
		}

	default:
		log.Printf("Narrator: disabled (set narrator_provider to enable)")
		return nil
	}
}
