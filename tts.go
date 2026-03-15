package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// Narrator streams TTS audio as raw PCM chunks (16-bit mono little-endian).
// onChunk is called with each chunk of PCM bytes as they arrive.
type Narrator interface {
	Speak(ctx context.Context, text string, onChunk func([]byte)) error
	// SampleRate returns the PCM sample rate in Hz (e.g. 24000).
	SampleRate() int
}

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

	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			onChunk(chunk)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// elevenlabsNarrator streams PCM from the ElevenLabs TTS REST API.
type elevenlabsNarrator struct {
	apiKey       string
	voiceID      string
	outputFormat string // e.g. "pcm_24000"
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

	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			onChunk(chunk)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// initNarrator creates a Narrator from config, or returns nil if disabled.
func initNarrator(cfg AppConfig) Narrator {
	sr := cfg.NarratorSampleRate
	if sr == 0 {
		sr = 24000
	}

	switch cfg.NarratorProvider {
	case "openai":
		if cfg.NarratorAPIKey == "" {
			log.Printf("Narrator: NARRATOR_API_KEY required for openai provider")
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
			apiKey:     cfg.NarratorAPIKey,
			baseURL:    "https://api.openai.com/v1",
			model:      model,
			voice:      voice,
			sampleRate: sr,
		}

	case "openai-compatible":
		if cfg.NarratorURL == "" {
			log.Printf("Narrator: NARRATOR_URL required for openai-compatible provider")
			return nil
		}
		model := cfg.NarratorModel
		if model == "" {
			model = "tts-1"
		}
		voice := cfg.NarratorVoice
		if voice == "" {
			voice = "default"
		}
		log.Printf("Narrator: openai-compatible TTS url=%s model=%s voice=%s sampleRate=%d", cfg.NarratorURL, model, voice, sr)
		return &openaiNarrator{
			apiKey:     cfg.NarratorAPIKey,
			baseURL:    cfg.NarratorURL,
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
