package voicebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Gemini Live API
	geminiLiveURL = "generativelanguage.googleapis.com"
	geminiModel   = "gemini-2.5-flash-native-audio-latest"

	// Voice
	geminiVoice = "Orus" // warm, professional, slightly deep — approachable

	// Twilio audio format
	twilioAudioFormat = "audio/l16;rate=16000" // 16-bit PCM at 16kHz

	// VAD / timing
	silenceTimeoutMs = 3000
	maxCallDuration  = 5 * time.Minute
)

// GeminiBridge connects Twilio Media Streams ↔ Gemini Live API
type GeminiBridge struct {
	twilioConn   *websocket.Conn
	geminiConn   *websocket.Conn
	mu           sync.Mutex
	streamSID    string
	callSID      string
	clientName   string
	businessName string
	niche        string
	callHistory  string
	apiKey       string
}

// NewGeminiBridge creates a new Gemini-based voice bridge.
func NewGeminiBridge(apiKey string) *GeminiBridge {
	return &GeminiBridge{apiKey: apiKey}
}

// WithClient sets client context.
func (b *GeminiBridge) WithClient(name, business, niche, history string) *GeminiBridge {
	b.clientName = name
	b.businessName = business
	b.niche = niche
	b.callHistory = history
	return b
}

// Start begins the bidirectional audio bridge.
func (b *GeminiBridge) Start(ctx context.Context, twilioConn *websocket.Conn) error {
	b.twilioConn = twilioConn

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)

	// Connect to Gemini Live API
	if err := b.connectGemini(); err != nil {
		return fmt.Errorf("gemini connect: %w", err)
	}
	defer b.geminiConn.Close()

	// Twilio → Gemini: forward client audio
	go func() {
		if err := b.forwardFromTwilio(ctx); err != nil {
			errCh <- fmt.Errorf("twilio→gemini: %w", err)
			cancel()
		}
	}()

	// Gemini → Twilio: forward AI audio
	go func() {
		if err := b.forwardFromGemini(ctx); err != nil {
			errCh <- fmt.Errorf("gemini→twilio: %w", err)
			cancel()
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (b *GeminiBridge) connectGemini() error {
	url := fmt.Sprintf("wss://%s/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent?key=%s",
		geminiLiveURL, b.apiKey)

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	b.geminiConn = conn

	// Send setup message
	setup := map[string]any{
		"setup": map[string]any{
			"model": "models/" + geminiModel,
			"generation_config": map[string]any{
				"response_modalities": []string{"AUDIO"},
				"speech_config": map[string]any{
					"voice_config": map[string]any{
						"prebuilt_voice_config": map[string]any{
							"voice_name": geminiVoice,
						},
					},
				},
			},
			"system_instruction": map[string]any{
				"parts": []map[string]any{
					{"text": SamSystemPrompt(b.clientName, b.businessName, b.niche, b.callHistory)},
				},
			},
		},
	}

	if err := b.geminiConn.WriteJSON(setup); err != nil {
		return fmt.Errorf("setup write: %w", err)
	}

	// Wait for setupComplete
	_, msg, err := b.geminiConn.ReadMessage()
	if err != nil {
		return fmt.Errorf("setup read: %w", err)
	}

	var resp map[string]any
	json.Unmarshal(msg, &resp)
	if _, ok := resp["setupComplete"]; !ok {
		return fmt.Errorf("unexpected setup response: %s", string(msg)[:200])
	}

	log.Println("Gemini Live API connected and configured")

	// Trigger initial greeting
	return b.sendText("Start the call by greeting the client warmly. Ask how their week has been.")
}

func (b *GeminiBridge) sendText(text string) error {
	msg := map[string]any{
		"client_content": map[string]any{
			"turns": []map[string]any{
				{
					"role": "user",
					"parts": []map[string]any{
						{"text": text},
					},
				},
			},
			"turn_complete": true,
		},
	}
	b.mu.Lock()
	err := b.geminiConn.WriteJSON(msg)
	b.mu.Unlock()
	return err
}

func (b *GeminiBridge) sendAudio(base64Audio string) error {
	// TODO: convert μ-law → PCM16 before sending to Gemini
	// For now, skip audio forwarding so Sam can at least speak
	return nil
}

func (b *GeminiBridge) forwardFromTwilio(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, message, err := b.twilioConn.ReadMessage()
		if err != nil {
			return err
		}

		var msg map[string]any
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		event, _ := msg["event"].(string)

		switch event {
		case "connected":
			log.Println("Twilio stream connected")

		case "start":
			if start, ok := msg["start"].(map[string]any); ok {
				b.callSID, _ = start["callSid"].(string)
				b.streamSID, _ = start["streamSid"].(string)
				if params, ok := start["customParameters"].(map[string]any); ok {
					if name, ok := params["clientName"].(string); ok && name != "" {
						b.clientName = name
					}
				}
			}
			log.Printf("Call started: %s for client: %s", b.callSID, b.clientName)

		case "media":
			// TODO: Convert Twilio μ-law audio to PCM16, then forward to Gemini
			// For now, Sam speaks but doesn't listen (audio input skipped)
			// payload, _ := msg["media"].(map[string]any)
			// audioData, _ := payload["payload"].(string)
			// b.sendAudio(audioData)

		case "stop":
			log.Println("Call ended")
			return nil
		}
	}
}

func (b *GeminiBridge) forwardFromGemini(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, message, err := b.geminiConn.ReadMessage()
		if err != nil {
			return err
		}

		var msg map[string]any
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		// Handle server content
		serverContent, ok := msg["serverContent"].(map[string]any)
		if !ok {
			continue
		}

		modelTurn, _ := serverContent["modelTurn"].(map[string]any)
		if modelTurn == nil {
			continue
		}

		parts, _ := modelTurn["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)

			// Handle audio data
			if inlineData, ok := part["inlineData"].(map[string]any); ok {
				audioB64, _ := inlineData["data"].(string)
				if audioB64 != "" {
					b.mu.Lock()
					b.twilioConn.WriteJSON(map[string]any{
						"event":     "media",
						"streamSid": b.streamSID,
						"media":     map[string]any{"payload": audioB64},
					})
					b.mu.Unlock()
				}
			}

			// Log text transcripts
			if text, ok := part["text"].(string); ok && text != "" {
				log.Printf("Sam: %s", text[:min(len(text), 100)])
			}
		}

		// Handle turn complete
		if tc, _ := serverContent["turnComplete"].(bool); tc {
			b.mu.Lock()
			b.twilioConn.WriteJSON(map[string]any{
				"event":     "mark",
				"streamSid": b.streamSID,
				"mark":      map[string]any{"name": "responseEnd"},
			})
			b.mu.Unlock()
			log.Println("Sam's turn complete")
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// HealthCheck verifies the Gemini API key works.
func HealthCheck(apiKey string) error {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s", apiKey)
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("API returned %d", resp.StatusCode)
	}
	return nil
}

// GetCallLog returns the last known call info for logging.
func (b *GeminiBridge) GetCallLog() map[string]string {
	return map[string]string{
		"callSid":  b.callSID,
		"client":   b.clientName,
		"business": b.businessName,
	}
}

// helper for parsing int from string
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
