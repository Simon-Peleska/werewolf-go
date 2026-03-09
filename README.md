# Werewolf Go

A web-based Werewolf (social deduction) game where each player joins from their own device. No app install needed — open the browser, pick a name, and play.

## The Game

Werewolf is a hidden-role game. Players are secretly assigned roles. Each night, the werewolves silently vote to eliminate a villager. Each day, all surviving players discuss and vote to eliminate whoever they suspect is a werewolf.

**Villagers win** when all werewolves are eliminated.
**Werewolves win** when they equal or outnumber the villagers.

### Roles

| Role | Team | What they do |
|------|------|--------------|
| Villager | Good | No special ability — deduce and vote |
| Werewolf | Evil | Vote each night to kill a villager |
| Wolf Cub | Evil | Werewolves get two kills the night after it dies |
| Seer | Good | Each night: learn if one player is a werewolf or not |
| Doctor | Good | Each night: protect one player from being killed (can self-protect) |
| Guard | Good | Each night: protect one player (no self-protect, can't protect same player twice in a row) |
| Witch | Good | One heal potion (save tonight's victim) + one poison potion (kill any player), each usable once |
| Hunter | Good | When eliminated for any reason, immediately shoots one player of their choice |
| Mason | Good | Knows who the other Masons are from the start |
| Cupid | Good | Night 1 only: links two players as lovers — if one dies, the other dies too |

## Running

### Without Nix

**Dependencies:** Go 1.21+, GCC, pkg-config, libsqlite3-dev (CGO is required for go-sqlite3)

```bash
# Build
go build ./...

# Run
./werewolf

# Or directly
go run .
```

### With Nix

```bash
# Run directly (no install)
nix run

# Or build first
nix build
./result/bin/werewolf

# Dev shell with all tools (Go, GCC, sqlite, chromium, inotify-tools)
nix develop
```

### Docker (via Nix)

```bash
nix build .#docker
docker load < result
docker run -p 8080:8080 werewolf
```

Then open [http://localhost:8080](http://localhost:8080).

### Configuration

Key options (can be set as env vars, in `config.json`, or as CLI flags):

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `-addr` | `ADDR` | `:8080` | Listen address |
| `-db` | `DB` | in-memory SQLite | Database file path |
| `-dev` | `DEV` | `false` | Dev mode: verbose logging + DB dumps on errors |
| `-storyteller-provider` | `STORYTELLER_PROVIDER` | — | AI narrator: `ollama`, `openai`, `claude`, `gemini`, `groq` |
| `-storyteller-model` | `STORYTELLER_MODEL` | — | Model name for the AI narrator |

```bash
# Persistent database + dev mode
./werewolf -db ./game.db -dev

# With AI narrator (Ollama)
./werewolf -storyteller-provider ollama -storyteller-model llama3
```

## Dev Tools

Scripts in `./tools/` cover the common dev workflows:

### `run_server.sh` — Start the dev server

```bash
./tools/run_server.sh                   # Run until Ctrl+C
./tools/run_server.sh --watch           # Auto-restart on .go/.html/.css changes
./tools/run_server.sh --all-logs        # Enable all request/WS/DB logging
./tools/run_server.sh --port 9090       # Use a different port
./tools/run_server.sh --test-db         # Persistent temp DB (survives restarts, deleted on exit)
```

### `run_tests.sh` — Run tests

```bash
./tools/run_tests.sh                              # Run all tests
./tools/run_tests.sh --test TestWerewolfCanVote   # Run one test
./tools/run_tests.sh --all-logs --keep-logs       # Full logging, keep logs on pass
./tools/run_tests.sh -v                           # Verbose output
```

### `start_chromium.sh` — Open browser windows for manual testing

Opens multiple Chromium windows pointed at the local server — useful for testing multi-player flows manually.

```bash
./tools/start_chromium.sh               # Open 5 windows at http://localhost:8080
./tools/start_chromium.sh -n 3          # Open 3 windows
./tools/start_chromium.sh -u http://localhost:9090   # Different URL
```

## Tech Stack

- **Backend**: Go, SQLite (CGO via go-sqlite3), gorilla/websocket
- **Frontend**: HTMX over WebSocket, Pico.css — no JavaScript framework
- **Tests**: go-rod browser automation (integration tests against a real server)
- **Packaging**: Nix flake (binary build + Docker image)
