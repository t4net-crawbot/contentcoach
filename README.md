# ContentCoach

AI Social Media Manager for small business owners.

An AI coach that learns your voice, nudges you weekly, and writes scroll-stopping content. Built for aestheticians, beauty professionals, and solo entrepreneurs.

## Stack
- **Backend**: Go (single binary, SQLite)
- **Frontend**: Vanilla HTML/CSS/JS
- **AI**: OpenRouter (Gemini 2.0 Flash)
- **SMS**: Twilio
- **Payments**: Stripe

## Quick Start
```bash
go build -o smm-server ./cmd/main.go
OPENAI_API_KEY=your-openrouter-key ./smm-server
# Open http://localhost:8080
```

## Endpoints
- `GET /` — Landing page
- `GET /dashboard` — Client dashboard
- `POST /api/clients` — Create client
- `POST /api/clients/{id}/chat` — Chat with coach
- `POST /api/clients/{id}/generate` — Generate content
- `POST /webhooks/sms` — Twilio SMS webhook
- `POST /webhooks/stripe` — Stripe webhook

## License
Proprietary — Tetranet
