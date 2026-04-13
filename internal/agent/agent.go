package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tetranet/social-media-manager/internal/store"
)

const DefaultSystemPrompt = `You are Alex, an AI social media manager and content coach. Your job is to help small business owners — especially aestheticians, beauty professionals, and solo entrepreneurs — create consistent, authentic social media content.

Your personality:
- Friendly but direct. You're their coach, not their assistant.
- You nudge. You follow up. You hold them accountable.
- You ask questions to draw out their expertise and personality.
- You never sound robotic or corporate.
- You use their name. You remember what they told you.
- You're encouraging but honest — if their idea needs work, you say so kindly.

How you work:
1. When nudging: Ask what's on their mind this week. Suggest a topic if they're stuck based on their niche and the season.
2. When they share an idea: Ask 2-3 follow-up questions to flesh it out. You need specifics before you can write.
3. When you have enough: Offer to write a blog post, video script, or social post. Ask which format they want.
4. When they pick a format: Generate the content in their voice. Match their tone from previous conversations.
5. After delivering: Schedule the next check-in. "Great work! I'll check in next Tuesday. Same time?"

You learn their voice over time. Pay attention to:
- How formal or casual they are
- Industry jargon they use naturally
- Topics they're passionate about
- What their audience responds to

Always end with a question or next step. Never leave the conversation hanging.`

type Config struct {
	OpenAIKey    string
	SystemPrompt string
	Model        string // defaults to gpt-4o-mini for cost
}

type Agent struct {
	cfg Config
}

func New(cfg Config) *Agent {
	if cfg.Model == "" {
		cfg.Model = "gpt-4o-mini"
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	return &Agent{cfg: cfg}
}

// Chat sends a message and returns the coach's reply
func (a *Agent) Chat(ctx context.Context, client *store.Client, history []*store.Message, userMsg string) (string, error) {
	// Build messages array for the LLM
	messages := a.buildMessages(client, history, userMsg)
	return a.callLLM(ctx, messages)
}

// GenerateNudge creates a personalized check-in message
func (a *Agent) GenerateNudge(ctx context.Context, client *store.Client, history []*store.Message) (string, error) {
	prompt := fmt.Sprintf(
		"It's time for the weekly check-in with %s. They run %s (niche: %s). "+
			"Generate a short, friendly text message asking what content they want to create this week. "+
			"If they were working on something last week, reference it. Keep it under 160 characters for SMS.",
		client.Name, client.BusinessName, client.Niche,
	)

	// Add recent context if available
	if len(history) > 0 {
		recent := history[len(history)-3:]
		contextParts := make([]string, 0, len(recent))
		for _, m := range recent {
			contextParts = append(contextParts, m.Role+": "+m.Content)
		}
		prompt += "\n\nRecent conversation context:\n" + strings.Join(contextParts, "\n")
	}

	messages := []map[string]any{
		{"role": "system", "content": a.cfg.SystemPrompt},
		{"role": "user", "content": prompt},
	}

	return a.callLLM(ctx, messages)
}

func (a *Agent) buildMessages(client *store.Client, history []*store.Message, userMsg string) []map[string]any {
	clientContext := fmt.Sprintf(
		"Client: %s | Business: %s | Niche: %s | Platforms: %v | Voice profile: %s",
		client.Name, client.BusinessName, client.Niche, client.Platforms, client.VoiceProfile,
	)

	messages := []map[string]any{
		{"role": "system", "content": a.cfg.SystemPrompt + "\n\nCurrent client context: " + clientContext},
	}

	// Add history (last N messages)
	maxHistory := 16
	start := 0
	if len(history) > maxHistory {
		start = len(history) - maxHistory
	}
	for _, m := range history[start:] {
		role := "user"
		if m.Role == "coach" {
			role = "assistant"
		}
		messages = append(messages, map[string]any{
			"role":    role,
			"content": m.Content,
		})
	}

	// Add current user message
	messages = append(messages, map[string]any{
		"role":    "user",
		"content": userMsg,
	})

	return messages
}

func (a *Agent) callLLM(ctx context.Context, messages []map[string]any) (string, error) {
	model := a.cfg.Model
	if model == "" {
		model = "google/gemini-2.0-flash-001" // cheap + good
	}

	payload := map[string]any{
		"model":       model,
		"messages":    messages,
		"max_tokens":  500,
		"temperature": 0.8,
	}

	body, _ := json.Marshal(payload)

	// Use OpenRouter as the API gateway (OpenAI-compatible)
	apiBase := "https://openrouter.ai/api/v1/chat/completions"
	apiKey := a.cfg.OpenAIKey // actually OpenRouter key

	req, err := http.NewRequestWithContext(ctx, "POST", apiBase, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://smm.fournet.win")
	req.Header.Set("X-Title", "Tetranet Social Media Manager")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("LLM request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("LLM response decode failed: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("LLM error: %s", result.Error.Message)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("LLM returned no choices")
	}

	return result.Choices[0].Message.Content, nil
}
