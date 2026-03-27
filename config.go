package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
)

// AppConfig holds all server configuration.
// Priority (lowest → highest): defaults < env vars < JSON config file < CLI flags.
type AppConfig struct {
	// Server
	DB   string `json:"db"`   // database connection string
	Dev  bool   `json:"dev"`  // dev mode: verbose logging, db dumps on errors
	Addr string `json:"addr"` // HTTP listen address

	// Logging (extended diagnostics, off by default)
	LogOutputDir string `json:"log_output_dir"`
	LogRequests  bool   `json:"log_requests"`
	LogHTML      bool   `json:"log_html"`
	LogDB        bool   `json:"log_db"`
	LogWS        bool   `json:"log_ws"`
	LogDebug     bool   `json:"log_debug"`

	// AI Storyteller
	Storyteller                 bool   `json:"storyteller"`                    // enable AI storyteller
	OpenAIModel                 string `json:"openai_model"`                   // model name
	OpenAIAPIBase               string `json:"openai_api_base"`                // base URL (default: https://api.openai.com/v1)
	OpenAIAPIKey                string `json:"openai_api_key"`                 // API key
	StorytellerTemperature      string `json:"storyteller_temperature"`        // float 0-2 as string (default 1.2)
	StorytellerSystemPromptFile string `json:"storyteller_system_prompt_file"` // path to file with system prompt (overrides default)
	StorytellerEndingPromptFile string `json:"storyteller_ending_prompt_file"` // path to file with ending prompt (overrides default)

	// AI Narrator (TTS)
	NarratorProvider   string `json:"narrator_provider"`    // openai | openai-compatible | elevenlabs
	NarratorModel      string `json:"narrator_model"`       // e.g. "tts-1", "tts-1-hd"
	NarratorVoice      string `json:"narrator_voice"`       // e.g. "onyx", "alloy" or ElevenLabs voice ID
	NarratorAPIKey     string `json:"narrator_api_key"`     // API key
	NarratorURL        string `json:"narrator_url"`         // base URL for openai-compatible
	NarratorSampleRate int    `json:"narrator_sample_rate"` // PCM sample rate in Hz (default 24000)
}

func (cfg AppConfig) toLogConfig() LogConfig {
	return LogConfig{
		OutputDir:   cfg.LogOutputDir,
		LogRequests: cfg.LogRequests,
		LogHTML:     cfg.LogHTML,
		LogDB:       cfg.LogDB,
		LogWS:       cfg.LogWS,
		Debug:       cfg.LogDebug,
	}
}

func defaultConfig() AppConfig {
	return AppConfig{
		DB:   "file::memory:?cache=shared",
		Addr: ":8080",
	}
}

// loadConfig builds a config by layering: defaults → env vars → JSON config file.
// CLI flag overrides are applied separately by flagValues.applyTo after flag.Parse.
func loadConfig(configPath string) AppConfig {
	cfg := defaultConfig()

	// Layer 1: env vars
	envStr := os.Getenv
	envBool := func(key string) (val bool, set bool) {
		v := os.Getenv(key)
		if v == "" {
			return false, false
		}
		return v == "1" || v == "true" || v == "yes", true
	}

	if v := envStr("DB"); v != "" {
		cfg.DB = v
	}
	if v, ok := envBool("DEV"); ok {
		cfg.Dev = v
	}
	if v := envStr("ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := envStr("LOG_OUTPUT_DIR"); v != "" {
		cfg.LogOutputDir = v
	}
	if v, ok := envBool("LOG_REQUESTS"); ok {
		cfg.LogRequests = v
	}
	if v, ok := envBool("LOG_HTML"); ok {
		cfg.LogHTML = v
	}
	if v, ok := envBool("LOG_DB"); ok {
		cfg.LogDB = v
	}
	if v, ok := envBool("LOG_WS"); ok {
		cfg.LogWS = v
	}
	if v, ok := envBool("LOG_DEBUG"); ok {
		cfg.LogDebug = v
	}
	if v, ok := envBool("STORYTELLER"); ok {
		cfg.Storyteller = v
	}
	if v := envStr("OPENAI_MODEL"); v != "" {
		cfg.OpenAIModel = v
	}
	if v := envStr("OPENAI_API_BASE"); v != "" {
		cfg.OpenAIAPIBase = v
	}
	if v := envStr("OPENAI_API_KEY"); v != "" {
		cfg.OpenAIAPIKey = v
	}
	if v := envStr("STORYTELLER_TEMPERATURE"); v != "" {
		cfg.StorytellerTemperature = v
	}
	if v := envStr("STORYTELLER_SYSTEM_PROMPT_FILE"); v != "" {
		cfg.StorytellerSystemPromptFile = v
	}
	if v := envStr("STORYTELLER_ENDING_PROMPT_FILE"); v != "" {
		cfg.StorytellerEndingPromptFile = v
	}
	if v := envStr("NARRATOR_PROVIDER"); v != "" {
		cfg.NarratorProvider = v
	}
	if v := envStr("NARRATOR_MODEL"); v != "" {
		cfg.NarratorModel = v
	}
	if v := envStr("NARRATOR_VOICE"); v != "" {
		cfg.NarratorVoice = v
	}
	if v := envStr("NARRATOR_API_KEY"); v != "" {
		cfg.NarratorAPIKey = v
	}
	if v := envStr("NARRATOR_URL"); v != "" {
		cfg.NarratorURL = v
	}
	if v := envStr("NARRATOR_SAMPLE_RATE"); v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			cfg.NarratorSampleRate = n
		}
	}

	// Layer 2: JSON config file — only fields present in the file override env vars
	if data, err := os.ReadFile(configPath); err == nil {
		var overlay map[string]json.RawMessage
		if err := json.Unmarshal(data, &overlay); err != nil {
			log.Printf("Config: failed to parse %s: %v", configPath, err)
		} else {
			applyJSONOverlay(&cfg, overlay)
			log.Printf("Config: loaded from %s", configPath)
		}
	} else if !os.IsNotExist(err) {
		log.Printf("Config: failed to read %s: %v", configPath, err)
	}

	return cfg
}

// censor returns "****" if a secret is set, or "" if empty.
func censor(s string) string {
	if s == "" {
		return ""
	}
	return "****"
}

// logConfig prints all configuration settings, censoring secrets.
func (cfg AppConfig) logConfig() {
	log.Println("=== Configuration ===")
	log.Printf("  db:                            %s", cfg.DB)
	log.Printf("  dev:                           %v", cfg.Dev)
	log.Printf("  addr:                          %s", cfg.Addr)
	log.Printf("  log_output_dir:                %s", cfg.LogOutputDir)
	log.Printf("  log_requests:                  %v", cfg.LogRequests)
	log.Printf("  log_html:                      %v", cfg.LogHTML)
	log.Printf("  log_db:                        %v", cfg.LogDB)
	log.Printf("  log_ws:                        %v", cfg.LogWS)
	log.Printf("  log_debug:                     %v", cfg.LogDebug)
	log.Printf("  storyteller:                   %v", cfg.Storyteller)
	log.Printf("  openai_model:                  %s", cfg.OpenAIModel)
	log.Printf("  openai_api_base:               %s", cfg.OpenAIAPIBase)
	log.Printf("  openai_api_key:                %s", censor(cfg.OpenAIAPIKey))
	log.Printf("  storyteller_temperature:       %s", cfg.StorytellerTemperature)
	log.Printf("  storyteller_system_prompt_file: %s", cfg.StorytellerSystemPromptFile)
	log.Printf("  storyteller_ending_prompt_file: %s", cfg.StorytellerEndingPromptFile)
	log.Printf("  narrator_provider:             %s", cfg.NarratorProvider)
	log.Printf("  narrator_model:                %s", cfg.NarratorModel)
	log.Printf("  narrator_voice:                %s", cfg.NarratorVoice)
	log.Printf("  narrator_api_key:              %s", censor(cfg.NarratorAPIKey))
	log.Printf("  narrator_url:                  %s", cfg.NarratorURL)
	log.Printf("  narrator_sample_rate:          %d", cfg.NarratorSampleRate)
	log.Println("=====================")
}

// applyJSONOverlay only sets fields that are explicitly present in the JSON map.
func applyJSONOverlay(cfg *AppConfig, m map[string]json.RawMessage) {
	str := func(key string, dst *string) {
		if v, ok := m[key]; ok {
			json.Unmarshal(v, dst)
		}
	}
	boolean := func(key string, dst *bool) {
		if v, ok := m[key]; ok {
			json.Unmarshal(v, dst)
		}
	}
	str("db", &cfg.DB)
	boolean("dev", &cfg.Dev)
	str("addr", &cfg.Addr)
	str("log_output_dir", &cfg.LogOutputDir)
	boolean("log_requests", &cfg.LogRequests)
	boolean("log_html", &cfg.LogHTML)
	boolean("log_db", &cfg.LogDB)
	boolean("log_ws", &cfg.LogWS)
	boolean("log_debug", &cfg.LogDebug)
	boolean("storyteller", &cfg.Storyteller)
	str("openai_model", &cfg.OpenAIModel)
	str("openai_api_base", &cfg.OpenAIAPIBase)
	str("openai_api_key", &cfg.OpenAIAPIKey)
	str("storyteller_temperature", &cfg.StorytellerTemperature)
	str("storyteller_system_prompt_file", &cfg.StorytellerSystemPromptFile)
	str("storyteller_ending_prompt_file", &cfg.StorytellerEndingPromptFile)
	str("narrator_provider", &cfg.NarratorProvider)
	str("narrator_model", &cfg.NarratorModel)
	str("narrator_voice", &cfg.NarratorVoice)
	str("narrator_api_key", &cfg.NarratorAPIKey)
	str("narrator_url", &cfg.NarratorURL)
	if v, ok := m["narrator_sample_rate"]; ok {
		json.Unmarshal(v, &cfg.NarratorSampleRate)
	}
}

// flagValues holds pointers to all registered CLI flags.
type flagValues struct {
	configPath                  *string
	db                          *string
	dev                         *bool
	addr                        *string
	logOutputDir                *string
	logRequests                 *bool
	logHTML                     *bool
	logDB                       *bool
	logWS                       *bool
	logDebug                    *bool
	storyteller                 *bool
	openaiModel                 *string
	openaiAPIBase               *string
	openaiAPIKey                *string
	storytellerTemperature      *string
	storytellerSystemPromptFile *string
	storytellerEndingPromptFile *string
	narratorProvider            *string
	narratorModel               *string
	narratorVoice               *string
	narratorAPIKey              *string
	narratorURL                 *string
	narratorSampleRate          *int
}

// registerFlags registers all CLI flags and returns pointers to their values.
// Call flag.Parse() after this, then applyTo to layer them over the loaded config.
func registerFlags() flagValues {
	return flagValues{
		configPath:                  flag.String("config", "/etc/werewolf/config.json", "path to JSON config file"),
		db:                          flag.String("db", "", "database connection string"),
		dev:                         flag.Bool("dev", false, "enable development mode (verbose logging, db dumps on error)"),
		addr:                        flag.String("addr", "", "HTTP listen address (e.g. :8080)"),
		logOutputDir:                flag.String("log-output-dir", "", "directory for extended log files"),
		logRequests:                 flag.Bool("log-requests", false, "log HTTP requests and responses"),
		logHTML:                     flag.Bool("log-html", false, "log HTML states"),
		logDB:                       flag.Bool("log-db", false, "log database dumps"),
		logWS:                       flag.Bool("log-ws", false, "log WebSocket messages"),
		logDebug:                    flag.Bool("log-debug", false, "enable debug logging"),
		storyteller:                 flag.Bool("storyteller", false, "enable AI storyteller"),
		openaiModel:                 flag.String("openai-model", "", "OpenAI model name"),
		openaiAPIBase:               flag.String("openai-api-base", "", "OpenAI API base URL (default: https://api.openai.com/v1)"),
		openaiAPIKey:                flag.String("openai-api-key", "", "OpenAI API key"),
		storytellerTemperature:      flag.String("storyteller-temperature", "", "sampling temperature 0-2 (default 1.2)"),
		storytellerSystemPromptFile: flag.String("storyteller-system-prompt-file", "", "path to file with system prompt (overrides default)"),
		storytellerEndingPromptFile: flag.String("storyteller-ending-prompt-file", "", "path to file with ending prompt (overrides default)"),
		narratorProvider:            flag.String("narrator-provider", "", "TTS narrator provider (openai|openai-compatible|elevenlabs)"),
		narratorModel:               flag.String("narrator-model", "", "TTS model name (e.g. tts-1)"),
		narratorVoice:               flag.String("narrator-voice", "", "TTS voice (e.g. onyx, alloy, or ElevenLabs voice ID)"),
		narratorAPIKey:              flag.String("narrator-api-key", "", "API key for TTS provider"),
		narratorURL:                 flag.String("narrator-url", "", "base URL for openai-compatible TTS provider"),
		narratorSampleRate:          flag.Int("narrator-sample-rate", 0, "PCM sample rate in Hz (default 24000)"),
	}
}

// applyTo overlays any CLI flags that were explicitly set onto cfg.
// Flags that were not passed on the command line are ignored (env/JSON values win).
func (fv flagValues) applyTo(cfg *AppConfig) {
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "db":
			cfg.DB = *fv.db
		case "dev":
			cfg.Dev = *fv.dev
		case "addr":
			cfg.Addr = *fv.addr
		case "log-output-dir":
			cfg.LogOutputDir = *fv.logOutputDir
		case "log-requests":
			cfg.LogRequests = *fv.logRequests
		case "log-html":
			cfg.LogHTML = *fv.logHTML
		case "log-db":
			cfg.LogDB = *fv.logDB
		case "log-ws":
			cfg.LogWS = *fv.logWS
		case "log-debug":
			cfg.LogDebug = *fv.logDebug
		case "storyteller":
			cfg.Storyteller = *fv.storyteller
		case "openai-model":
			cfg.OpenAIModel = *fv.openaiModel
		case "openai-api-base":
			cfg.OpenAIAPIBase = *fv.openaiAPIBase
		case "openai-api-key":
			cfg.OpenAIAPIKey = *fv.openaiAPIKey
		case "storyteller-temperature":
			cfg.StorytellerTemperature = *fv.storytellerTemperature
		case "storyteller-system-prompt-file":
			cfg.StorytellerSystemPromptFile = *fv.storytellerSystemPromptFile
		case "storyteller-ending-prompt-file":
			cfg.StorytellerEndingPromptFile = *fv.storytellerEndingPromptFile
		case "narrator-provider":
			cfg.NarratorProvider = *fv.narratorProvider
		case "narrator-model":
			cfg.NarratorModel = *fv.narratorModel
		case "narrator-voice":
			cfg.NarratorVoice = *fv.narratorVoice
		case "narrator-api-key":
			cfg.NarratorAPIKey = *fv.narratorAPIKey
		case "narrator-url":
			cfg.NarratorURL = *fv.narratorURL
		case "narrator-sample-rate":
			cfg.NarratorSampleRate = *fv.narratorSampleRate
		}
	})
}
