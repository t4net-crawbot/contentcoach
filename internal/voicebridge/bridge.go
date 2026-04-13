package voicebridge

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"github.com/gorilla/websocket"
)

// Bridge connects a Twilio Media Stream to an OpenAI Realtime API session.
// Audio flows: Twilio → Bridge → OpenAI → Bridge → Twilio
type Bridge struct {
	twilioConn  *websocket.Conn
	openaiConn  *websocket.Conn
	mu          sync.Mutex
	streamSID   string // Twilio stream SID (for sending audio back)
	callSID     string
	clientName  string
	businessName string
	niche       string
	callHistory string
}

// NewBridge creates a new voice bridge session.
func NewBridge() *Bridge {
	return &Bridge{}
}

// WithClient sets the client context for the call.
func (b *Bridge) WithClient(name, business, niche, history string) *Bridge {
	b.clientName = name
	b.businessName = business
	b.niche = niche
	b.callHistory = history
	return b
}

// Start begins the bidirectional audio bridge.
// It blocks until the context is cancelled or a connection drops.
func (b *Bridge) Start(ctx context.Context, twilioConn, openaiConn *websocket.Conn) error {
	b.twilioConn = twilioConn
	b.openaiConn = openaiConn

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)

	// Configure OpenAI session, then start conversation
	go func() {
		if err := b.configureSession(); err != nil {
			errCh <- err
			cancel()
			return
		}
		if err := b.triggerResponse(); err != nil {
			errCh <- err
			cancel()
			return
		}
	}()

	// Twilio → OpenAI: forward client audio
	go func() {
		if err := b.forwardFromTwilio(ctx); err != nil {
			errCh <- err
			cancel()
		}
	}()

	// OpenAI → Twilio: forward AI audio
	go func() {
		if err := b.forwardFromOpenAI(ctx); err != nil {
			errCh <- err
			cancel()
		}
	}()

	// Wait for context or error
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// configureSession sends the session.update event to OpenAI.
func (b *Bridge) configureSession() error {
	config := map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"modalities":          []string{"text", "audio"},
			"instructions":        SamSystemPrompt(b.clientName, b.businessName, b.niche, b.callHistory),
			"voice":               "alloy",
			"input_audio_format":  audioFormat,
			"output_audio_format": audioFormat,
			"turn_detection": map[string]any{
				"type":               "server_vad",
				"threshold":          vadThreshold,
				"prefix_padding_ms":  vadPrefixPaddingMs,
				"silence_duration_ms": vadSilenceDurationMs,
			},
			"temperature": responseTemperature,
		},
	}

	b.mu.Lock()
	err := b.openaiConn.WriteJSON(config)
	b.mu.Unlock()

	if err != nil {
		return err
	}

	log.Println("OpenAI session configured")
	return nil
}

// triggerResponse kicks off the first AI response (greeting).
func (b *Bridge) triggerResponse() error {
	b.mu.Lock()
	err := b.openaiConn.WriteJSON(map[string]any{
		"type": "response.create",
		"response": map[string]any{
			"modalities": []string{"text", "audio"},
		},
	})
	b.mu.Unlock()
	return err
}

// forwardFromTwilio reads Twilio Media Stream events and forwards audio to OpenAI.
func (b *Bridge) forwardFromTwilio(ctx context.Context) error {
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
			log.Printf("Call started: callSid=%s streamSid=%s client=%s", b.callSID, b.streamSID, b.clientName)

		case "media":
			payload, _ := msg["media"].(map[string]any)
			audioData, _ := payload["payload"].(string)

			b.mu.Lock()
			writeErr := b.openaiConn.WriteJSON(map[string]any{
				"type":  "input_audio_buffer.append",
				"audio": audioData,
			})
			b.mu.Unlock()

			if writeErr != nil {
				return writeErr
			}

		case "stop":
			log.Println("Call ended by Twilio")
			return nil
		}
	}
}

// forwardFromOpenAI reads OpenAI Realtime events and forwards audio to Twilio.
func (b *Bridge) forwardFromOpenAI(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, message, err := b.openaiConn.ReadMessage()
		if err != nil {
			return err
		}

		var msg map[string]any
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		eventType, _ := msg["type"].(string)

		switch eventType {
		case "response.audio.delta":
			delta, _ := msg["delta"].(string)
			b.mu.Lock()
			writeErr := b.twilioConn.WriteJSON(map[string]any{
				"event":     "media",
				"streamSid": b.streamSID,
				"media":     map[string]any{"payload": delta},
			})
			b.mu.Unlock()
			if writeErr != nil {
				return writeErr
			}

		case "response.audio.done":
			b.mu.Lock()
			b.twilioConn.WriteJSON(map[string]any{
				"event":     "mark",
				"streamSid": b.streamSID,
				"mark":      map[string]any{"name": "responseEnd"},
			})
			b.mu.Unlock()

		case "input_audio_buffer.speech_started":
			log.Println("Client started speaking (barge-in)")
			b.mu.Lock()
			b.twilioConn.WriteJSON(map[string]any{
				"event":     "clear",
				"streamSid": b.streamSID,
			})
			b.mu.Unlock()

		case "conversation.item.input_audio_transcription.completed":
			transcript, _ := msg["transcript"].(string)
			log.Printf("Client: %s", transcript)

		case "response.audio_transcript.done":
			transcript, _ := msg["transcript"].(string)
			log.Printf("Sam: %s", transcript)

		case "response.done":
			log.Println("AI response complete")

		case "error":
			errMsg, _ := msg["error"].(map[string]any)
			log.Printf("OpenAI error: %v", errMsg)
		}
	}
}
