package voicebridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	orURL         = "https://openrouter.ai/api/v1/chat/completions"
	orModel       = "openai/gpt-4o-audio-preview"
	orVoice       = "alloy"
	orAudioFormat = "pcm16"
	orTimeout     = 30 * time.Second
)

type ORBridge struct {
	twilio    *websocket.Conn
	apiKey    string
	geminiKey string
	audioBuf  []string
	mu        sync.Mutex
	streamSID string
	callSID   string
	client    string
	msgs      []map[string]any
}

func NewORBridge(key, gemKey string) *ORBridge { return &ORBridge{apiKey: key, geminiKey: gemKey} }
func (b *ORBridge) WithClient(name, biz, niche, hist string) *ORBridge { b.client = name; return b }

func (b *ORBridge) Start(ctx context.Context, ws *websocket.Conn) error {
	b.twilio = ws

	// First: wait for Twilio to connect and send "start" event
	log.Println("Waiting for Twilio stream...")
	if err := b.waitForStart(); err != nil {
		return fmt.Errorf("waiting for start: %w", err)
	}
	log.Printf("Twilio ready, streamSID=%s, client=%s", b.streamSID, b.client)

	// Now generate greeting
	b.msgs = []map[string]any{
		{"role": "system", "content": SamSystemPrompt(b.client, "", "", "")},
		{"role": "user", "content": "Greet the client warmly by name. Ask how their week was. 1-2 short sentences."},
	}

	log.Println("Generating greeting...")
	audio, text, err := b.gen()
	if err != nil {
		return fmt.Errorf("greeting: %w", err)
	}
	b.msgs = append(b.msgs, map[string]any{"role": "assistant", "content": text})
	log.Printf("Sam: %s", text)
	b.play(audio)

	return b.listen(ctx)
}

func (b *ORBridge) waitForStart() error {
	for {
		_, msg, err := b.twilio.ReadMessage()
		if err != nil {
			return err
		}
		var m map[string]any
		json.Unmarshal(msg, &m)
		switch m["event"] {
		case "connected":
			log.Println("Twilio stream connected")
		case "start":
			if s, ok := m["start"].(map[string]any); ok {
				b.callSID, _ = s["callSid"].(string)
				b.streamSID, _ = s["streamSid"].(string)
				if p, ok := s["customParameters"].(map[string]any); ok {
					if n, ok := p["clientName"].(string); ok && n != "" {
						b.client = n
					}
				}
			}
			return nil
		}
	}
}

func (b *ORBridge) gen() (audioData, transcript string, err error) {
	payload, _ := json.Marshal(map[string]any{
		"model":      orModel,
		"modalities": []string{"text", "audio"},
		"audio":      map[string]string{"voice": orVoice, "format": orAudioFormat},
		"stream":     true,
		"messages":   b.msgs,
	})

	req, _ := http.NewRequest("POST", orURL, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: orTimeout}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("API %d: %.300s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	b.audioBuf = nil
	var tr strings.Builder
	var sseChunks []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]
		if data == "[DONE]" {
			break
		}

		var ev struct {
			Choices []struct {
				Delta struct {
					Audio *struct {
						Data string `json:"data"`
						Text string `json:"transcript"`
					} `json:"audio"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		if len(ev.Choices) > 0 && ev.Choices[0].Delta.Audio != nil {
			a := ev.Choices[0].Delta.Audio
			if a.Data != "" {
				sseChunks = append(sseChunks, a.Data)
			}
			tr.WriteString(a.Text)
		}
	}

	return strings.Join(sseChunks, ""), tr.String(), nil
}

func (b *ORBridge) play(base64PCM string) {
	if base64PCM == "" {
		return
	}
	pcm, err := base64.StdEncoding.DecodeString(base64PCM)
	if err != nil {
		log.Printf("base64 decode: %v", err)
		return
	}
	ulaw := toMulaw(pcm, 24000, 8000)
	b64 := base64.StdEncoding.EncodeToString(ulaw)

	b.mu.Lock()
	b.twilio.WriteJSON(map[string]any{"event": "media", "streamSid": b.streamSID, "media": map[string]any{"payload": b64}})
	b.twilio.WriteJSON(map[string]any{"event": "mark", "streamSid": b.streamSID, "mark": map[string]any{"name": "done"}})
	b.mu.Unlock()
}

func (b *ORBridge) listen(ctx context.Context) error {
	b.audioBuf = nil
	last := time.Now()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		b.twilio.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		_, msg, err := b.twilio.ReadMessage()
		if err != nil {
			if _, ok := err.(*websocket.CloseError); ok {
				return nil
			}
			if len(b.audioBuf) > 0 && time.Since(last) > 2*time.Second {
				log.Printf("Silence (%d chunks), responding", len(b.audioBuf))
				b.respond()
				b.audioBuf = nil
			}
			continue
		}

		var m map[string]any
		json.Unmarshal(msg, &m)
		switch m["event"] {
		case "connected":
			log.Println("Twilio connected")
		case "start":
			if s, ok := m["start"].(map[string]any); ok {
				b.callSID, _ = s["callSid"].(string)
				b.streamSID, _ = s["streamSid"].(string)
				if p, ok := s["customParameters"].(map[string]any); ok {
					if n, ok := p["clientName"].(string); ok {
						b.client = n
					}
				}
			}
			log.Printf("Call: %s client: %s", b.callSID, b.client)
		case "media":
			if med, ok := m["media"].(map[string]any); ok {
				log.Printf("Audio chunk #%d", len(b.audioBuf)); b.audioBuf = append(b.audioBuf, med["payload"].(string))
				last = time.Now()
			}
		case "stop":
			if len(b.audioBuf) > 0 {
				b.respond()
			}
			log.Println("Call ended")
			return nil
		}
	}
}

func (b *ORBridge) respond() {
	// Try to transcribe what the client said
	userText := ""
	if b.geminiKey != "" {
		transcript, err := TranscribeSpeech(b.geminiKey, b.audioBuf)
		if err != nil {
			log.Printf("STT error: %v", err)
		} else if transcript != "" {
			userText = transcript
			log.Printf("Client said: %s", userText)
		}
	}
	if userText == "" {
		userText = "The client just spoke. Continue naturally."
	}
	b.msgs = append(b.msgs, map[string]any{"role": "user", "content": userText})

	audio, text, err := b.gen()
	if err != nil {
		log.Printf("Error: %v", err)
		return
	}
	b.msgs = append(b.msgs, map[string]any{"role": "assistant", "content": text})
	log.Printf("Sam: %s", text)
	b.play(audio)
}

func (b *ORBridge) GetCallLog() map[string]string {
	return map[string]string{"callSid": b.callSID, "client": b.client}
}

// toMulaw converts PCM16 at srcHz to μ-law at dstHz
func toMulaw(pcm []byte, srcHz, dstHz int) []byte {
	ratio := srcHz / dstHz
	if ratio < 1 {
		ratio = 1
	}
	n := len(pcm) / 2
	out := make([]byte, 0, n/ratio+1)
	for i := 0; i < n; i += ratio {
		j := i * 2
		if j+1 >= len(pcm) {
			break
		}
		s := int16(pcm[j]) | int16(pcm[j+1])<<8
		out = append(out, mulaw(s))
	}
	return out
}

func mulaw(s int16) byte {
	const bias = 0x84
	var sign byte
	v := int(s)
	if v < 0 {
		sign = 0x80
		v = -v
	}
	v += bias
	if v > 0x7FFF {
		v = 0x7FFF
	}
	v -= bias + 1

	var exp byte = 7
	for i := byte(0); i < 8; i++ {
		if v <= (0x3F << i) {
			exp = i
			break
		}
	}
	return ^(sign | (exp << 4) | byte((v>>(exp+1))&0x0F))
}

// TranscribeSpeech converts μ-law base64 audio chunks to text via Gemini
func TranscribeSpeech(apiKey string, mulawChunks []string) (string, error) {
	// Combine μ-law chunks and convert to PCM16 for Gemini
	var allMulaw []byte
	for _, chunk := range mulawChunks {
		data, err := base64.StdEncoding.DecodeString(chunk)
		if err != nil {
			continue
		}
		allMulaw = append(allMulaw, data...)
	}

	if len(allMulaw) == 0 {
		return "", fmt.Errorf("no audio data")
	}

	// Convert μ-law → PCM16 at 16kHz (Gemini expects 16kHz)
	pcm := mulawToPCM16(allMulaw, 8000, 16000)
	pcmB64 := base64.StdEncoding.EncodeToString(pcm)

	// Send to Gemini for transcription
	payload, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]any{
					{
						"inline_data": map[string]any{
							"mime_type": "audio/pcm;rate=16000",
							"data":      pcmB64,
						},
					},
					{"text": "Transcribe what was said. Only return the exact words spoken, nothing else. If nothing was spoken, return empty string."},
				},
			},
		},
	})

	req, _ := http.NewRequest("POST",
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key="+apiKey,
		bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	json.Unmarshal(body, &result)

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return strings.TrimSpace(result.Candidates[0].Content.Parts[0].Text), nil
	}
	return "", nil
}

// mulawToPCM16 converts μ-law at srcHz to PCM16 at dstHz
func mulawToPCM16(mulaw []byte, srcHz, dstHz int) []byte {
	ratio := dstHz / srcHz
	if ratio < 1 {
		ratio = 1
	}

	result := make([]byte, 0, len(mulaw)*ratio*2)
	for _, b := range mulaw {
		sample := mulawDecode(b)
		// Upsample: repeat each sample `ratio` times
		for i := 0; i < ratio; i++ {
			result = append(result, byte(sample), byte(sample>>8))
		}
	}
	return result
}

// μ-law decode lookup table
var mulawTable = func() [256]int16 {
	var table [256]int16
	for i := 0; i < 256; i++ {
		table[i] = mulawDecodeByte(byte(i))
	}
	return table
}()

func mulawDecode(b byte) int16 {
	return mulawTable[b]
}

func mulawDecodeByte(b byte) int16 {
	b = ^b
	sign := int16(1 - (int(b)>>7)*2)
	segment := int((b >> 4) & 0x07)
	mantissa := int(b & 0x0F)

	value := (mantissa*2 + 33) << (segment + 2)
	return sign * int16(value - 33)
}
