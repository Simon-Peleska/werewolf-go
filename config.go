package main

import (
	"encoding/json"
	"flag"
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
	StorytellerProvider    string `json:"storyteller_provider"`    // ollama | openai | claude | gemini | groq | openai-compatible
	StorytellerModel       string `json:"storyteller_model"`       // model name
	StorytellerOllamaURL   string `json:"storyteller_ollama_url"`  // Ollama server URL
	StorytellerURL         string `json:"storyteller_url"`         // base URL for openai-compatible
	StorytellerAPIKey      string `json:"storyteller_api_key"`     // API key for openai-compatible
	StorytellerTemperature string `json:"storyteller_temperature"` // float 0-1 as string
	StorytellerThinking    string `json:"storyteller_thinking"`    // none | low | medium | high | auto
	GroqAPIKey             string `json:"groq_api_key"`            // API key for groq provider
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
		DB:                   "file::memory:?cache=shared",
		Addr:                 ":8080",
		StorytellerOllamaURL: "http://localhost:11434",
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
	if v := envStr("STORYTELLER_PROVIDER"); v != "" {
		cfg.StorytellerProvider = v
	}
	if v := envStr("STORYTELLER_MODEL"); v != "" {
		cfg.StorytellerModel = v
	}
	if v := envStr("STORYTELLER_OLLAMA_URL"); v != "" {
		cfg.StorytellerOllamaURL = v
	}
	if v := envStr("STORYTELLER_URL"); v != "" {
		cfg.StorytellerURL = v
	}
	if v := envStr("STORYTELLER_API_KEY"); v != "" {
		cfg.StorytellerAPIKey = v
	}
	if v := envStr("STORYTELLER_TEMPERATURE"); v != "" {
		cfg.StorytellerTemperature = v
	}
	if v := envStr("STORYTELLER_THINKING"); v != "" {
		cfg.StorytellerThinking = v
	}
	if v := envStr("GROQ_API_KEY"); v != "" {
		cfg.GroqAPIKey = v
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
	str("storyteller_provider", &cfg.StorytellerProvider)
	str("storyteller_model", &cfg.StorytellerModel)
	str("storyteller_ollama_url", &cfg.StorytellerOllamaURL)
	str("storyteller_url", &cfg.StorytellerURL)
	str("storyteller_api_key", &cfg.StorytellerAPIKey)
	str("storyteller_temperature", &cfg.StorytellerTemperature)
	str("storyteller_thinking", &cfg.StorytellerThinking)
	str("groq_api_key", &cfg.GroqAPIKey)
}

// flagValues holds pointers to all registered CLI flags.
type flagValues struct {
	configPath             *string
	db                     *string
	dev                    *bool
	addr                   *string
	logOutputDir           *string
	logRequests            *bool
	logHTML                *bool
	logDB                  *bool
	logWS                  *bool
	logDebug               *bool
	storytellerProvider    *string
	storytellerModel       *string
	storytellerOllamaURL   *string
	storytellerURL         *string
	storytellerAPIKey      *string
	storytellerTemperature *string
	storytellerThinking    *string
	groqAPIKey             *string
}

// registerFlags registers all CLI flags and returns pointers to their values.
// Call flag.Parse() after this, then applyTo to layer them over the loaded config.
func registerFlags() flagValues {
	return flagValues{
		configPath:             flag.String("config", "config.json", "path to JSON config file"),
		db:                     flag.String("db", "", "database connection string"),
		dev:                    flag.Bool("dev", false, "enable development mode (verbose logging, db dumps on error)"),
		addr:                   flag.String("addr", "", "HTTP listen address (e.g. :8080)"),
		logOutputDir:           flag.String("log-output-dir", "", "directory for extended log files"),
		logRequests:            flag.Bool("log-requests", false, "log HTTP requests and responses"),
		logHTML:                flag.Bool("log-html", false, "log HTML states"),
		logDB:                  flag.Bool("log-db", false, "log database dumps"),
		logWS:                  flag.Bool("log-ws", false, "log WebSocket messages"),
		logDebug:               flag.Bool("log-debug", false, "enable debug logging"),
		storytellerProvider:    flag.String("storyteller-provider", "", "AI storyteller provider (ollama|openai|claude|gemini|groq|openai-compatible)"),
		storytellerModel:       flag.String("storyteller-model", "", "AI storyteller model name"),
		storytellerOllamaURL:   flag.String("storyteller-ollama-url", "", "Ollama server URL"),
		storytellerURL:         flag.String("storyteller-url", "", "base URL for openai-compatible provider"),
		storytellerAPIKey:      flag.String("storyteller-api-key", "", "API key for storyteller provider"),
		storytellerTemperature: flag.String("storyteller-temperature", "", "sampling temperature 0-1"),
		storytellerThinking:    flag.String("storyteller-thinking", "", "thinking mode: none|low|medium|high|auto"),
		groqAPIKey:             flag.String("groq-api-key", "", "Groq API key"),
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
		case "storyteller-provider":
			cfg.StorytellerProvider = *fv.storytellerProvider
		case "storyteller-model":
			cfg.StorytellerModel = *fv.storytellerModel
		case "storyteller-ollama-url":
			cfg.StorytellerOllamaURL = *fv.storytellerOllamaURL
		case "storyteller-url":
			cfg.StorytellerURL = *fv.storytellerURL
		case "storyteller-api-key":
			cfg.StorytellerAPIKey = *fv.storytellerAPIKey
		case "storyteller-temperature":
			cfg.StorytellerTemperature = *fv.storytellerTemperature
		case "storyteller-thinking":
			cfg.StorytellerThinking = *fv.storytellerThinking
		case "groq-api-key":
			cfg.GroqAPIKey = *fv.groqAPIKey
		}
	})
}
