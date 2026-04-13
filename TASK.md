# Task: Build the Phone Agent — "Sam"

## Context
We're building an AI social media manager that calls clients weekly. The voice bridge (Twilio ↔ OpenAI Realtime API) is scaffolded but was written without review. We need to refactor and build it properly using the agent system.

## What to Build

### 1. Phone Agent Persona (`agents/sam/`)
Create a dedicated phone agent named **Sam** (short for Samantha). She's the one who calls clients.

Files needed:
- `agents/sam/SOUL.md` — Her personality: warm, approachable, Southern-adjacent charm, genuinely curious about people's businesses. Not corporate. Not robotic. Like a smart friend who's great at marketing. She listens more than she talks.
- `agents/sam/CLIENTS/` — Directory for per-client memory files. Each client gets a markdown file with their profile, call history, preferences, voice notes.
- `agents/sam/CALL_PREP.md` — Template for what Sam reviews before each call (client profile, last call notes, content delivered, seasonal relevance)
- `agents/sam/CALL_NOTES.md` — Template for what Sam writes after each call (topics discussed, content requests, action items)

### 2. Refactor Voice Bridge (`cmd/voice-bridge/`)
The current `main.go` has issues:
- Everything in one file — break into proper packages
- No tests
- Hard-coded config — should read from env/flags
- No logging structure
- The `callSID` field is used as both CallSID and StreamSID (bug)
- `configureOpenAI` uses a race-y `time.Sleep` instead of waiting for the session.created event

Refactor into:
- `internal/voicebridge/bridge.go` — Core bridge logic
- `internal/voicebridge/twilio.go` — Twilio Media Stream handling
- `internal/voicebridge/openai.go` — OpenAI Realtime API handling
- `cmd/voice-bridge/main.go` — Just entry point + config

### 3. Agent Context System
Before each call, Sam should read:
- Client profile from `agents/sam/CLIENTS/{id}.md`
- Last 3 call notes
- Any pending content requests

After each call, Sam should write:
- Call summary to client's memory file
- Content brief (what to generate)

## Constraints
- **Go for all backend code** — no exceptions
- Follow Cody's LESSONS.md — Bob will review
- Keep it cheap: gpt-4o-mini for text, realtime API only during live calls
- Single binary deployment
- Every function under 40 lines
- Named constants, no magic numbers
- Proper error handling everywhere

## Files to Read First
- `BRIEF.md` — project overview
- `cmd/voice-bridge/main.go` — existing voice bridge code
- `cmd/main.go` — existing API server
- `internal/` — existing packages

## Output
- Write all code to `projects/social-media-manager/`
- Run `go build ./...` to verify it compiles
- Write a summary of what you built
