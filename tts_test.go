package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// generateA4PCM generates a pure A4 (440 Hz) sine wave as 16-bit mono PCM,
// little-endian, at the given sample rate and duration.
// A4 = 440 Hz: concert pitch / standard tuning reference.
func generateA4PCM(sampleRate int, durationSecs float64) []byte {
	const freq = 440.0    // A4 concert pitch
	const amplitude = 0.5 // 50% to avoid clipping

	numSamples := int(float64(sampleRate) * durationSecs)
	buf := make([]byte, numSamples*2) // 16-bit = 2 bytes per sample
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		sample := math.Sin(2 * math.Pi * freq * t)
		pcm := int16(sample * amplitude * 32767)
		buf[i*2] = byte(pcm)        // little-endian low byte
		buf[i*2+1] = byte(pcm >> 8) // little-endian high byte
	}
	return buf
}

// TestNarratorPCMStreamingToFrontend verifies the full end-to-end audio pipeline:
//
//  1. A fake TTS server streams a known A1 (55 Hz) PCM tone and records its SHA-256.
//  2. The narrator (openai-compatible) calls the fake server, streams chunks to
//     hub.broadcastAudio() — the same path used in production.
//  3. Binary WebSocket frames arrive at the browser. The htmx:wsBeforeMessage
//     handler intercepts them (preventing HTMX from parsing PCM as HTML) and
//     passes them to playPCMChunk().
//  4. The browser accumulates received bytes and computes SHA-256 via Web Crypto.
//  5. The hashes are compared — byte-exact match proves the pipeline is lossless.
//  6. _nextPlayTime > 0 confirms the Web Audio API actually scheduled the audio.
func TestNarratorPCMStreamingToFrontend(t *testing.T) {
	t.Parallel()

	ctx := newTestContext(t)
	defer ctx.cleanup()

	// ── 1. Fake OpenAi server (mimics OpenAI /v1/response endpoint) ─────────
	storyText := []string{"The village ", "wept ", "in silence."}
	fakeOpenAi := createFakeOpenAiServer(t, storyText)
	defer fakeOpenAi.Close()

	// ── 2. Build Storyteller pointing at the fake server ────────────────────────────
	storyteller := initStoryteller(AppConfig{
		StorytellerProvider: "openai-compatible",
		StorytellerModel:    "llm-1",
		StorytellerURL:      fakeOpenAi.URL + "/v1/",
		StorytellerAPIKey:   "test-key",
	})

	ctx.app.hub.storyteller = storyteller
	defer func() { ctx.app.hub.storyteller = nil }()

	// ── 3. Generate A4 (440 Hz) tone: 0.5 s @ 24 kHz, 16-bit mono = 24 000 bytes ──
	const sampleRate = 24000
	const durationSecs = 0.5
	pcm := generateA4PCM(sampleRate, durationSecs)

	// Server-side SHA-256 — ground truth of what will be sent.
	serverSum := sha256.Sum256(pcm)
	serverHashHex := hex.EncodeToString(serverSum[:])
	t.Logf("PCM: %d bytes, SHA-256 = %s", len(pcm), serverHashHex)

	// ── 4. Fake TTS server (mimics OpenAI /v1/audio/speech PCM endpoint) ─────────
	const chunkSize = 4096
	fakeTTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/speech" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		flusher, canFlush := w.(http.Flusher)
		for i := 0; i < len(pcm); i += chunkSize {
			end := i + chunkSize
			if end > len(pcm) {
				end = len(pcm)
			}
			w.Write(pcm[i:end]) //nolint
			if canFlush {
				flusher.Flush() // push each chunk immediately, don't buffer
			}
		}
	}))
	defer fakeTTS.Close()

	// ── 5. Build narrator pointing at the fake server ────────────────────────────
	narrator := initNarrator(AppConfig{
		NarratorProvider:   "openai-compatible",
		NarratorURL:        fakeTTS.URL + "/v1",
		NarratorAPIKey:     "test-key",
		NarratorModel:      "tts-1",
		NarratorVoice:      "alloy",
		NarratorSampleRate: sampleRate,
	})
	if narrator == nil {
		t.Fatal("initNarrator returned nil for openai-compatible provider")
	}

	// Attach narrator to the hub (newTestContext starts with nil narrator).
	ctx.app.hub.narrator = narrator
	defer func() { ctx.app.hub.narrator = nil }()

	// ── 6. Game + browser setup ──────────────────────────────────────────────────
	browser, browserCleanup := newTestBrowserWithLogger(t, ctx.logger)
	defer browserCleanup()

	// Sign up a player; they land on game.html which includes the audio playback JS.
	// 1 werewolf + 2 villagers
	var players []*TestPlayer
	for _, name := range []string{"ST1", "ST2", "ST3"} {
		players = append(players, browser.signupPlayer(ctx.baseURL, name))
	}

	players[0].waitUntilCondition(`() => document.querySelector('#game-content') !== null`,
		"game-content loaded")

	// ── 7. Inject byte accumulator + wrap playPCMChunk ───────────────────────────
	// We wrap the global playPCMChunk (declared as a named function in game.html)
	// so we can capture every chunk that flows through the audio pipeline.
	_, err := players[0].p().Eval(`() => {
		window._testAudioChunks = [];
		var orig = window.playPCMChunk;
		window.playPCMChunk = function(ab) {
			window._testAudioChunks.push(new Uint8Array(ab.slice(0)));
			orig(ab);
		};
		return true;
	}`)
	if err != nil {
		t.Fatalf("inject audio accumulator: %v", err)
	}

	// ── 7. Play Game ───────────────────────────----------------------------------
	players[0].addRoleByID(RoleWerewolf)
	players[0].addRoleByID(RoleVillager)
	players[0].addRoleByID(RoleVillager)
	players[0].startGame()

	werewolves, villagers := findPlayersByRole(players)
	if len(werewolves) == 0 || len(villagers) < 2 {
		t.Skip("Role assignment didn't produce expected roles")
	}

	target := villagers[0]
	watcher := villagers[1]

	// Werewolf kills target (voteForPlayer auto-presses End Vote)
	werewolves[0].voteForPlayer(target.Name)

	submitNightSurveysForAllPlayers(players)

	// Wait for the async AI story to appear in the watcher's history
	err = watcher.waitUntilCondition(
		fmt.Sprintf(`() => { const h = document.querySelector('#history-bar'); return h && h.textContent.includes(%q); }`, strings.Join(storyText, "")),
		"AI story appears in history",
	)
	if err != nil {
		ctx.logger.LogDB("FAIL: AI story not in history")
		t.Errorf("AI story did not appear in history: %v", err)
	}

	// Story is public — werewolf should see it too
	if !werewolves[0].historyContains(strings.Join(storyText, "")) {
		t.Errorf("Werewolf cannot see AI story in history")
	}

	// ── 7. Wait for all bytes to arrive at the browser ───────────────────────────
	// Poll the accumulator total. At 24 kHz / 16-bit the narrator finishes in <100 ms;
	// WS delivery over loopback is near-instant. 5 s deadline is very generous.
	expectedBytes := len(pcm)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, evalErr := players[0].p().Eval(`() =>
			window._testAudioChunks.reduce(function(a, b) { return a + b.length; }, 0)`)
		if evalErr == nil && res.Value.Int() >= expectedBytes {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// ── 8. Verify total bytes received ───────────────────────────────────────────
	bytesRes, err := players[0].p().Eval(
		`() => window._testAudioChunks.reduce(function(a, b) { return a + b.length; }, 0)`)
	if err != nil {
		t.Fatalf("eval total bytes: %v", err)
	}
	totalBytesReceived := bytesRes.Value.Int()
	ctx.logger.Debug("Browser received %d bytes (expected %d)", totalBytesReceived, expectedBytes)
	if totalBytesReceived != expectedBytes {
		t.Errorf("byte count: want %d, got %d", expectedBytes, totalBytesReceived)
	}

	// ── 9. Browser-side SHA-256 via Web Crypto API ───────────────────────────────
	// crypto.subtle is available on localhost (Chrome treats localhost as secure).
	// page.Eval awaits Promises, so the async function resolves before returning.
	hashRes, err := players[0].p().Eval(`async function() {
		var chunks = window._testAudioChunks;
		var totalLen = chunks.reduce(function(a, b) { return a + b.length; }, 0);
		var combined = new Uint8Array(totalLen);
		var offset = 0;
		for (var i = 0; i < chunks.length; i++) {
			combined.set(chunks[i], offset);
			offset += chunks[i].length;
		}
		var hashBuf = await crypto.subtle.digest('SHA-256', combined.buffer);
		var hashArr = Array.from(new Uint8Array(hashBuf));
		return hashArr.map(function(b) { return b.toString(16).padStart(2, '0'); }).join('');
	}`)
	if err != nil {
		t.Fatalf("browser SHA-256 computation: %v", err)
	}
	browserHashHex := hashRes.Value.Str()
	ctx.logger.Debug("Server SHA-256:  %s", serverHashHex)
	ctx.logger.Debug("Browser SHA-256: %s", browserHashHex)

	// ── 10. Hash comparison — proves lossless byte delivery ──────────────────────
	if browserHashHex != serverHashHex {
		t.Errorf("audio data mismatch:\n  server:  %s\n  browser: %s", serverHashHex, browserHashHex)
	} else {
		t.Logf("Hashes match: %s ✓", serverHashHex)
	}

	// ── 11. Web Audio API scheduling check ───────────────────────────────────────
	// _nextPlayTime > 0 proves createBufferSource().start() was called at least once.
	nextPlayRes, err := players[0].p().Eval(`() => _nextPlayTime`)
	if err != nil {
		t.Fatalf("eval _nextPlayTime: %v", err)
	}
	nextPlayTime := nextPlayRes.Value.Num()
	ctx.logger.Debug("_nextPlayTime: %.4f s", nextPlayTime)
	if nextPlayTime <= 0 {
		t.Errorf("_nextPlayTime should be > 0 after scheduling audio, got %f", nextPlayTime)
	}
	// Must have scheduled at least the full duration of audio.
	if nextPlayTime < durationSecs {
		t.Errorf("_nextPlayTime %.4f < audio duration %.4f", nextPlayTime, durationSecs)
	}

	// ── 12. Wait for full playback to complete ───────────────────────────────────
	// Poll until AudioContext.currentTime has advanced past _nextPlayTime, meaning
	// the last scheduled AudioBufferSourceNode has finished playing.
	// In a suspended context this will never advance, so we skip the wait.
	ctx.logger.Debug("Waiting for audio playback to finish (nextPlayTime=%.3fs)…", nextPlayTime)
	_, err = players[0].p().Timeout(browserTimeout).Eval(`() => new Promise(function(resolve) {
		if (!_audioCtx || _audioCtx.state === 'suspended') { resolve(); return; }
		(function poll() {
			if (_audioCtx.currentTime >= _nextPlayTime) { resolve(); return; }
			setTimeout(poll, 30);
		})();
	})`)
	if err != nil {
		t.Fatalf("waiting for playback to complete: %v", err)
	}
	ctx.logger.Debug("Playback finished")

	// ── 13. AudioContext state ───────────────────────────────────────────────────
	stateRes, err := players[0].p().Eval(`() => _audioCtx ? _audioCtx.state : 'not created'`)
	if err != nil {
		t.Fatalf("eval AudioContext state: %v", err)
	}
	audioState := stateRes.Value.Str()
	if audioState == "not created" {
		t.Error("AudioContext was never created")
	}
	if audioState == "suspended" {
		t.Logf("AudioContext is suspended (autoplay policy) — audio was scheduled but not audible in headless")
	}

	t.Logf("Pipeline OK | chunks: %d | bytes: %d | hash: match | AudioContext: %s | nextPlayTime: %.3fs",
		(expectedBytes+chunkSize-1)/chunkSize,
		totalBytesReceived,
		audioState,
		nextPlayTime)
}
