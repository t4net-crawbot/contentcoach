# Social Media Manager — Phone Agent

## Product
AI social media manager for small businesses. A phone agent calls clients weekly, interviews them about their business, then generates blog posts, video scripts, and social media content.

## Architecture
- **Phone Agent** (dedicated AI persona) — handles all client calls, maintains memory
- **Voice Bridge** — Go server bridging Twilio Media Streams ↔ OpenAI Realtime API
- **Backend API** — Go HTTP server for client CRUD, chat, content generation, nudge scheduling
- **Store** — SQLite for persistence (single binary, zero deps)
- **Twilio** — SMS nudges + voice calls
- **Stripe** — monthly subscription billing

## Phone Agent Requirements
1. Dedicated identity — name, personality, female voice (approachable)
2. Client memory — reads profile + call history before every call
3. Writes notes after every call into client's memory file
4. Gets better over time — learns client's voice, audience, preferences
5. Can do real-time conversations (needs OpenAI Realtime API)
6. Can do SMS check-ins between calls

## What Needs Building
1. The phone agent's persona + memory system
2. Refactor voice-bridge to use the phone agent's context
3. Client onboarding flow
4. Web UI for client dashboard
5. Content generation pipeline (blog/video/social)
6. Deploy to smm.fournet.win

## Constraints
- Go for all backend code
- Node.js for scripts only
- Keep it cheap — gpt-4o-mini for content gen, realtime API only for calls
- Single binary deployment where possible
