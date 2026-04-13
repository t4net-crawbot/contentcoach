package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/tetranet/social-media-manager/internal/voicebridge"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	gemKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENROUTER_API_KEY required")
	}
	log.Println("API keys configured ✓")

	publicWS := os.Getenv("PUBLIC_WS_URL")

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	mux.HandleFunc("/call/start", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		wsURL := publicWS
		if wsURL == "" {
			wsURL = "wss://" + r.Host + "/media-stream"
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response><Connect><Stream url="` + wsURL + `"><Parameter name="clientName" value="` + name + `" /></Stream></Connect></Response>`))
	})

	mux.HandleFunc("/media-stream", func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("Upgrade failed: %v", err)
			return
		}
		defer ws.Close()

		bridge := voicebridge.NewORBridge(apiKey, gemKey)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		log.Printf("New call incoming...")
		if err := bridge.Start(ctx, ws); err != nil {
			log.Printf("Bridge ended: %v", err)
		}
		log.Printf("Call done: %+v", bridge.GetCallLog())
	})

	server := &http.Server{Addr: ":" + port, Handler: mux}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; server.Close() }()

	log.Printf("Sam voice bridge (Gemini Live) starting on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
