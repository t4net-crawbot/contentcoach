package content

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tetranet/social-media-manager/internal/store"
)

type Request struct {
	Client *store.Client
	Type   string // blog, video_script, instagram_caption, facebook_post, tiktok_script
	Topic  string
	Target string // platform
	History []*store.Message
}

type Generator struct {
	apiKey string
	model  string
}

func New(apiKey string) *Generator {
	return &Generator{
		apiKey: apiKey,
		model:  "gpt-4o-mini",
	}
}

func (g *Generator) Generate(ctx context.Context, req Request) (string, error) {
	prompt := g.buildPrompt(req)

	payload := map[string]any{
		"model":       g.model,
		"messages":    prompt,
		"max_tokens":  2000,
		"temperature": 0.7,
	}

	body, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("content generation failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no content generated")
	}

	return result.Choices[0].Message.Content, nil
}

func (g *Generator) buildPrompt(req Request) []map[string]any {
	// Gather voice/style from recent conversations
	voiceNotes := ""
	if req.Client.VoiceProfile != "" {
		voiceNotes = "\n\nVoice profile: " + req.Client.VoiceProfile
	}

	// Recent conversation context
	contextStr := ""
	if len(req.History) > 0 {
		recent := req.History
		if len(recent) > 10 {
			recent = recent[len(recent)-10:]
		}
		parts := make([]string, 0, len(recent))
		for _, m := range recent {
			role := "Client"
			if m.Role == "coach" {
				role = "Coach"
			}
			parts = append(parts, role+": "+m.Content)
		}
		contextStr = "\n\nRecent conversation (for context and voice):\n" + join(parts, "\n")
	}

	formatInstructions := map[string]string{
		"blog":               "Write a full blog post (800-1200 words) with a catchy title, intro hook, 3-5 sections with subheadings, and a call to action. Write in their voice — like they're talking to a client.",
		"video_script":       "Write a video script (2-4 minutes) with a hook in the first 5 seconds, clear talking points, and a call to action. Include stage directions in [brackets]. Conversational tone.",
		"instagram_caption":  "Write an Instagram caption (150-300 words) with a strong opening line, value-packed middle, and engaging question at the end. Include 10-15 relevant hashtags.",
		"facebook_post":      "Write a Facebook post (200-400 words) that tells a story or shares expertise. End with a question to drive engagement. Friendly and personal tone.",
		"tiktok_script":      "Write a TikTok script (30-60 seconds) with a pattern interrupt opening, quick value delivery, and call to action. Fast-paced, energetic. Include text overlay suggestions.",
		"youtube_script":     "Write a YouTube video script (5-10 minutes) with intro, 3-5 main sections, and outro with subscribe CTA. Include thumbnail suggestions and title options.",
		"email_newsletter":   "Write an email newsletter (300-500 words) with subject line options, personal opening, one main teaching point, and soft CTA. Warm, expert tone.",
	}

	instruction, ok := formatInstructions[req.Type]
	if !ok {
		instruction = "Write engaging social media content about the given topic in the client's voice."
	}

	system := fmt.Sprintf(`You are a social media content writer for %s (%s, niche: %s).%s

%s

IMPORTANT: Write in the first person as if the client is speaking directly. Match their expertise level and personality. Be authentic — no generic marketing speak.`,
		req.Client.Name, req.Client.BusinessName, req.Client.Niche,
		voiceNotes, instruction,
	)

	user := fmt.Sprintf("Topic: %s\nPlatform: %s\n\nBased on our conversation, write this content now.", req.Topic, req.Target)

	return []map[string]any{
		{"role": "system", "content": system},
		{"role": "user", "content": user + contextStr},
	}
}

func join(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
