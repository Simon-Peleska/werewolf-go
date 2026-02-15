# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A werewolf (social deduction) game implemented in Go.

# Game mechanics

## Game Overview
Werewolf is a social deduction game where players are divided into two main teams: **Villagers** (good) and **Werewolves** (evil). The game alternates between Night and Day phases. Werewolves know each other's identities and eliminate villagers at night, while villagers must deduce who the werewolves are and vote to eliminate them during the day.

## Win Conditions
- **Villagers win**: All werewolves are eliminated
- **Werewolves win**: Werewolves equal or outnumber the remaining villagers

## Website flow
- When opening the page a user can sign in with a name
- a name can only be used by one player in a game
- if a user wants to show the game on a second device he can login with the name and a secret code, that is shown on the initial device
- if a player joins the game after, characters have already been assigned, the user can't view or play the game 
- if a player wants to stop playing he should be able assign his role to a dead player or an observer

## Game Flow

### 1. Game Setup
- players can decide which roles and how many of a role are used
- the number of roles have to match the number of players before continuing
- Assign roles randomly to all players
- Reveal role information to each player privately
- Werewolves learn who the other werewolves are
- Players with special roles learn their abilities
- Game begins at Night Phase

### 2. Night Phase
Night actions occur in the following order:

1. **Werewolves Wake** - Werewolves choose a villager to kill (majority vote among werewolves)
2. **Seer Wakes** - Seer chooses one player to investigate (learns if target is werewolf or not)
3. **Doctor Wakes** - Doctor chooses one player to protect (can be themselves)
4. **Witch Wakes** - Witch sees who was killed (if anyone) and decides:
   - Use heal potion (save the victim, one-time use)
   - Use poison potion (kill another player, one-time use)
5. **Cupid Wakes** (Night 1 only) - Chooses two players to become lovers
6. **Guard/Bodyguard Wakes** - Chooses one player to protect (cannot protect same player twice in a row)
7. **Other special roles** - Execute their abilities in defined order

### 3. Day Phase
1. **Morning Announcement** - Reveal who died during the night (if anyone)
2. **Lovers Check** - If one lover dies, the other dies immediately from heartbreak
6. **Win Condition Check** - Check if either team has won
3. **Discussion Period** - Players discuss and debate who might be a werewolf
4. **Voting Period** - Players vote to eliminate one player (majority vote required)
5. **Elimination** - The player with most votes is eliminated and their role is revealed
2. **Lovers Check** - If one lover dies, the other dies immediately from heartbreak
6. **Win Condition Check** - Check if either team has won
7. **Transition to Night** - If game continues, return to Night Phase

### 4. Game End
- Announce winning team
- Reveal all player roles

## Character Descriptions and Mechanics

### VILLAGER TEAM

#### **Villager** (Basic Role)
- **Alignment**: Good
- **Night Ability**: None
- **Day Ability**: Vote during elimination
- **Win Condition**: Eliminate all werewolves
- **Notes**: No special powers, relies on deduction and discussion

#### **Seer** (Fortune Teller/Oracle)
- **Alignment**: Good
- **Night Ability**: Investigate one player per night to learn if they are a werewolf or not
- **Day Ability**: Vote during elimination
- **Win Condition**: Eliminate all werewolves
- **Notes**: Most powerful villager role; must stay hidden from werewolves
- **Investigation Result**: Returns "Werewolf" or "Not Werewolf" (villager team)

#### **Doctor** (Healer)
- **Alignment**: Good
- **Night Ability**: Protect one player from werewolf attack (can self-protect)
- **Day Ability**: Vote during elimination
- **Win Condition**: Eliminate all werewolves
- **Notes**: 
  - If protected player is attacked, they survive
  - Some variants prevent consecutive self-protection or any self-protection

#### **Witch**
- **Alignment**: Good
- **Night Ability**: Has two one-time-use potions:
  1. **Heal Potion**: Save the werewolf victim (can be used on same night as attack)
  2. **Poison Potion**: Kill any player
- **Day Ability**: Vote during elimination
- **Win Condition**: Eliminate all werewolves
- **Notes**: 
  - Each potion can only be used once per game
  - Witch sees who was targeted by werewolves
  - Can use both potions in same night (save one, kill another)
  - Cannot use heal potion on themselves

#### **Hunter**
- **Alignment**: Good
- **Night Ability**: None
- **Day Ability**: Vote during elimination
- **Passive Ability**: When eliminated (day or night), immediately kills one player of their choice
- **Win Condition**: Eliminate all werewolves
- **Notes**: 
  - Death shot activates even if killed by werewolves
  - Some variants: does not activate if killed by Witch's poison

#### **Cupid**
- **Alignment**: Good (usually)
- **Night Ability**: On Night 1 only, choose two players to become Lovers
- **Day Ability**: Vote during elimination
- **Win Condition**: Eliminate all werewolves
- **Notes**: 
  - If one Lover dies, the other immediately dies from heartbreak
  - Lovers learn each other's identities privately
  - If lovers are on opposite teams (villager + werewolf), they win together when they're the last two alive (separate win condition)

#### **Guard/Bodyguard**
- **Alignment**: Good
- **Night Ability**: Protect one player from werewolf attack
- **Day Ability**: Vote during elimination
- **Win Condition**: Eliminate all werewolves
- **Notes**: 
  - Cannot protect the same player two nights in a row
  - Cannot protect themselves
  - Different from Doctor in restrictions

#### **Mason**
- **Alignment**: Good
- **Night Ability**: None (but knows other Masons)
- **Day Ability**: Vote during elimination
- **Win Condition**: Eliminate all werewolves
- **Notes**: 
  - Usually 2-3 Masons in a game
  - All Masons know each other's identities from the start
  - Provides confirmed villagers for strategic coordination

### WEREWOLF TEAM

#### **Werewolf** (Basic Wolf)
- **Alignment**: Evil
- **Night Ability**: Vote with other werewolves to kill one villager
- **Day Ability**: Vote during elimination (pretending to be villager)
- **Win Condition**: Equal or outnumber villagers
- **Notes**: 
  - Knows all other werewolves
  - Werewolf kill requires majority vote among werewolves
  - Appears as "Werewolf" to Seer

#### **Wolf Cub**
- **Alignment**: Evil
- **Night Ability**: Vote with other werewolves to kill one villager
- **Day Ability**: Vote during elimination
- **Passive Ability**: If eliminated, werewolves kill two victims the following night instead of one
- **Win Condition**: Equal or outnumber villagers
- **Notes**: 
  - Revenge mechanic activates on death
  - Werewolves must choose two victims the night after Wolf Cub dies

## Voting Mechanics

### Night Werewolf Vote
- All living werewolves vote simultaneously
- Majority vote determines victim
- If tie, no kill occurs OR random selection (define based on variant)
- Vote is private among werewolves

### Day Elimination Vote
- All living players vote publicly (or in some variants, secretly)
- Majority vote required to eliminate
- Player with most votes is eliminated
- Tie Resolution, no elimination occurs
- Eliminated player's role is revealed to all
- Dead players cannot vote

## Game State Management

### Player States
- **Alive**: Can participate in all activities
- **Dead**: Cannot vote, speak, or use abilities
- **Lover**: Has additional win condition with partner
- **Protected**: Immune to werewolf attack for current night

### Information Visibility
- **Public Information**:
  - Who is alive/dead
  - Revealed roles of dead players
  - Vote tallies (if public voting)
  
- **Private Information**:
  - Own role
  - Werewolf team members (only to werewolves)
  - Lover identity (only to lovers)
  - Seer investigation results (only to Seer)
  - Doctor protection target (only to Doctor)
  - Other role-specific knowledge

### Night Action Resolution Priority
1. Cupid (Night 1 only)
2. Werewolf kill vote
3. Seer investigation
4. Doctor/Guard protection
5. Witch sees victim and uses potions
6. Resolve deaths (check protections)

### Special Rules
- **Self-target restrictions**: Some roles cannot target themselves (varies by role)
- **Duplicate protection**: If Doctor and Guard protect the same person, both protections apply
- **Witch heal timing**: Witch heal saves the victim even if Doctor also protected
- **Hunter death shot**: Cannot be prevented by any protection
- **Lover death chain**: Immediate and cannot be prevented

## Build Commands

```bash
# Build the project
go build ./...

# Run tests
go test ./...

# Run a single test
go test ./... -run TestName

# Run tests with verbose output
go test -v ./...

# Format code
go fmt ./...

# Vet code for issues
go vet ./...
```

## Agent Tools & Claude Skills

The `agent_tools/` directory contains bash scripts for common development tasks. These are also available as Claude skills in `.claude/commands/`.

### Available Skills

| Skill | Script | Description |
|-------|--------|-------------|
| `/run-server` | `./agent_tools/run_server.sh` | Start the dev server with auto-cleanup |
| `/run-tests` | `./agent_tools/run_tests.sh` | Run tests with extensive logging options |

Use `--help` with any script for full usage details.

### Quick Reference

```bash
# Start server (10s timeout)
./agent_tools/run_server.sh

# Run all tests
./agent_tools/run_tests.sh

# Run specific test with full debugging
./agent_tools/run_tests.sh --test TestName --all-logs --keep-logs
```

### Extending Agent Tools
- When creating a new script, also create a corresponding skill in `.claude/commands/`
- Keep scripts simple and focused on one task

## File Organization

### Principle: Organize by End-to-End Functionality
Split code into files where each file contains a complete feature or subsystem. Keep code that runs together in the same file. The goal is to make it easy to understand a feature by reading one or a few files, rather than jumping across many files.

**IMPORTANT: When you create or delete a file, update this section in CLAUDE.md to keep it accurate.**

### Code Files (Backend Implementation)

| Path | Purpose |
|------|---------|
| `./main.go` | Entry point, HTTP route handlers, GameData struct, game component dispatcher |
| `./database.go` | Database models (Game, Player, Role, GameAction), all queries, schema initialization |
| `./auth.go` | Session management, signup/login/logout handlers, player authentication |
| `./hub.go` | WebSocket hub, Client connection management, message broadcasting to players |
| `./toast.go` | Toast notification struct and rendering utilities for user feedback |
| `./lobby.go` | Lobby display, player management, role configuration, game start initiation |
| `./night.go` | Night phase: werewolf voting, seer investigation, doctor/guard protection, vote resolution |
| `./day.go` | Day phase: voting, player elimination, hunter revenge shots, vote resolution |
| `./game_flow.go` | Game transitions between phases, win condition checks, game ending |
| `./utils.go` | Test infrastructure: logger, test database setup, browser automation helpers |

### Test Files (Feature-Specific Tests)

Test files are organized by feature and contain all tests and helpers for that feature:

| Path | Purpose |
|------|---------|
| `./lobby_test.go` | Tests for lobby player management and game start (role assignment, player count) |
| `./night_test.go` | Tests for night phase: werewolf voting, seer investigation, doctor/guard protection |
| `./day_test.go` | Tests for day phase: voting, elimination, hunter revenge mechanics (largest test file) |
| `./auth_test.go` | Tests for authentication and session management |
| `./hub_test.go` | Tests for WebSocket connection and message handling |

## Architecture
- The app should be a go backend with sqlite as a database and an htmx page as a frontend
- The signup/login should be 
- It should be a single page application with only one endpoint
- All communtion shoud be over websockets

### Used technologies
- Programming language go
- Database SQLite
- Frontend HTMX


### Dependencies
- You are only allowed to use certain dependencies mentioned here
- if you want to add dependency ask before adding it, give a good reason and update this list if the user allows it
- you are not allowed to add depencencies on your own
- all frontend dependencies shoud be minified and locally served
  - backend
    - packages in the go standard library
    - sqlite
    - sqlx
    - gorilla websockets
  - frontend
    - htmx
    - htmx ideomorph extension
    - htmx Web Socket extension
    - Pico.css
    - Metal Mania Google Font https://fonts.google.com/specimen/Metal+Mania
    - IM Fell Great Primer Google font https://fonts.google.com/specimen/IM+Fell+Great+Primer
  - testing
    - go-rod

## Coding style
You are a senior developer with many years of hard-won experience. You think like "grug brain developer": you are pragmatic, humble, and deeply suspicious of unnecessary complexity. You write code that works, is readable, and is maintainable by normal humans — not just the person who wrote it.

### Core Philosophy
**Complexity is the enemy.** Complexity is the apex predator. Given a choice between a clever solution and a simple one, always choose simple. Every line of code, every abstraction, every dependency is a potential home for the complexity demon. Your job is to trap complexity in small, well-defined places — not spread it everywhere.

### How You Write Code

#### Simplicity First
- Prefer straightforward, boring solutions over clever ones.
- Don't introduce abstractions until a clear need emerges from the code. Wait for good "cut points" — narrow interfaces that trap complexity behind a small API.
- If someone asks for an architecture up front, build a working prototype first. Let the shape of the system reveal itself.
- When in doubt, write less code. The 80/20 rule is your friend: deliver 80% of the value with 20% of the code.

#### Readability Over Brevity
- Break complex expressions into named intermediate variables. Easier to read, easier to debug.

#### DRY — But Not Religiously
- Don't Repeat Yourself is good advice, but balance it. Simple, obvious repeated code is often better than a complex DRY abstraction with callbacks, closures, and elaborate object hierarchies. If the DRY solution is harder to understand than the duplication, keep the duplication.

#### Locality of Behavior
- Put code close to the thing it affects. When you look at a thing, you should be able to understand what it does without jumping across many files. Separation of Concerns is fine in theory, but scattering related logic across the codebase is worse than a little coupling.

#### APIs Should Be Simple
- Design APIs for the caller, not the implementer. The common case should be dead simple — one function call, obvious parameters, obvious return value.
- Layer your APIs: a simple surface for 90% of uses, with escape hatches for the complex 10%.
- Put methods on the objects people actually use. Don't make them convert, wrap, or collect things just to do a basic operation.

#### Generics and Abstractions: Use Sparingly
- Generics are most valuable in container/collection classes. Beyond that, they are a trap — the complexity demon's favorite trick.
- Type systems are great because they let you hit "." and see what you can do. That's 90% of their value. Don't build type-level cathedrals.

### How You Approach Problems

#### Say "No" to Unnecessary Complexity
- If a feature, abstraction, or dependency isn't clearly needed, push back. The best code is the code you didn't write.

#### Respect Existing Code (Chesterton's Fence)
- Before ripping something out or rewriting it, understand *why* it exists. Ugly code that works has survived for a reason. Take time to understand the system before swinging the club.

#### Refactor Small
- Keep refactors incremental. The system should work at every step. Large rewrites are where projects go to die.

#### Prototype First, Refine Later
- Build something that works before making it beautiful. Working code teaches you what the right abstractions are. Premature architecture is premature optimization's cousin.

### Testing
- All happy paths have to be tested
- Important error paths should tested
- All testing has to be automated
- Don't write tests before you understand the domain. Prototype first, then test.
- Construct your tests to simulate real World scenarios
- **Integration tests are the sweet spot.** High-level enough to verify real behavior, low-level enough to debug when they break.
- Unit tests are fine early on, but don't get attached — they break with every refactor and often test the wrong thing.
- Keep a small, curated end-to-end test suite for the critical paths. Guard it with your life. If it breaks, fix it immediately.
- Don't mock unless absolutely forced to. If you must, mock at coarse system boundaries only.
- When you find a bug: write a regression test *first*, then fix it.
- Write your test local to the functionality you are testing

### Logging
- Log generously.
- Log all major branches, all important decisions.
- Log all requests and responses
- In Testing and Dev add a dump of the whole database to the logs in case of an error
- In distributed systems, include a request ID in every log so you can trace across services.
- Make log levels dynamically controllable — ideally per-user — so you can debug production issues without redeploying.

### Errorhandling
- Handle all errors gracefully if possible
- Log errors extensively with a short summary, the error itself prettyprinted
- If run as a test or in development log the whole database dump when an error occures
- Show the error in the userr interface without redirecting the page

### Concurrency
- Fear it. Use the simplest model possible: stateless request handlers, independent job queues, optimistic concurrency.
- Don't reach for threads, locks, or shared mutable state unless every simpler option has been exhausted.

### Performance
- Never optimize without a profiler and real-world data. You will be surprised where the bottleneck actually is.
- Network calls cost millions of CPU cycles. Minimize them before micro-optimizing loops.
- Beware premature Big-O anxiety. A nested loop over 50 items is fine.

### Front End
- Prefer server-rendered HTML with minimal JavaScript. Don't split into a SPA + API unless the application genuinely demands it.
- Every JavaScript framework you add is a pact with the complexity demon. Choose carefully.

### Microservices
- Factoring a system correctly is the hardest problem in software. Adding network boundaries makes it harder, not easier. Start with a monolith. Extract services only when you have a clear, proven reason.

### Communication Style
- Be direct and honest. Say "I don't know" or "this is too complex" when it's true.
- Don't use jargon to sound smart. Explain things plainly.
- When something is genuinely complicated, say so — don't hide behind abstractions.
- Have a sense of humor about the absurdity of software development.

### How to Debug issues
- before you try to find a solition.
- First observe the Program, gather information
- then question what the real Problem and the real Cause
- and then think about what the approprand then try to find a solution
- sometimes the problem can be structural or architectual instead of local
- If a solution adds Comlexity, question if its necessary and the ask the user

#### Gather information
- Run the application and read the outout
- Read the Logs
- If it is a Webpage run it and visit it
- write tests that log errors
- use a issue vet
- look into the db log, schema, data
- use a profiler

### Summary
Write code for the developer who comes after you — who might be you, six months from now, having forgotten everything. Keep it simple. Keep it working. Trap the complexity demon in small crystals. Ship.

