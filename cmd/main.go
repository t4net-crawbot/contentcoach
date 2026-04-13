package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/tetranet/social-media-manager/internal/agent"
	"github.com/tetranet/social-media-manager/internal/content"
	"github.com/tetranet/social-media-manager/internal/notify"
	"github.com/tetranet/social-media-manager/internal/store"
)

// Server is the social media manager API
type Server struct {
	store   store.Store
	agent   *agent.Agent
	notify  *notify.Manager
	content *content.Generator
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Init store (SQLite for now — single binary, no deps)
	db, err := store.NewSQLite("data/smm.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Init agent (the conversational coach)
	agentCfg := agent.Config{
		OpenAIKey:  os.Getenv("OPENAI_API_KEY"),
		SystemPrompt: agent.DefaultSystemPrompt,
	}
	coach := agent.New(agentCfg)

	// Init notifier (Twilio SMS + email)
	twilio := notify.TwilioConfig{
		AccountSID: os.Getenv("TWILIO_ACCOUNT_SID"),
		AuthToken:  os.Getenv("TWILIO_AUTH_TOKEN"),
		FromPhone:  os.Getenv("TWILIO_PHONE"),
	}
	mailCfg := notify.EmailConfig{
		From: "alex@fournet.win",
	}
	notifier := notify.NewManager(twilio, mailCfg)

	// Init content generator
	gen := content.New(os.Getenv("OPENAI_API_KEY"))

	srv := &Server{
		store:   db,
		agent:   coach,
		notify:  notifier,
		content: gen,
	}

	mux := http.NewServeMux()

	// Serve static frontend
	mux.HandleFunc("GET /", srv.handleIndex)
	mux.HandleFunc("GET /dashboard", srv.handleDashboard)
	mux.HandleFunc("GET /signup", srv.handleDashboard)

	// Health check
	mux.HandleFunc("GET /health", srv.handleHealth)

	// Client CRUD
	mux.HandleFunc("POST /api/clients", srv.handleCreateClient)
	mux.HandleFunc("GET /api/clients", srv.handleListClients)
	mux.HandleFunc("GET /api/clients/{id}", srv.handleGetClient)

	// Conversation (chat with the coach)
	mux.HandleFunc("POST /api/clients/{id}/chat", srv.handleChat)

	// Content generation
	mux.HandleFunc("POST /api/clients/{id}/generate", srv.handleGenerate)

	// Webhook: incoming SMS reply from client
	mux.HandleFunc("POST /webhooks/sms", srv.handleSMSWebhook)

	// Webhook: Stripe payment events
	mux.HandleFunc("POST /webhooks/stripe", srv.handleStripeWebhook)

	// Client lookup by email/phone (for login)
	mux.HandleFunc("POST /api/clients/lookup", srv.handleLookupClient)

	// Chat history
	mux.HandleFunc("GET /api/clients/{id}/chat/history", srv.handleChatHistory)

	// Nudge scheduler (called by cron)
	mux.HandleFunc("POST /internal/nudge", srv.handleNudge)

	log.Printf("Social Media Manager starting on :%s", port)
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second, // long for AI generation
	}
	log.Fatal(server.ListenAndServe())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// --- Static file handlers ---

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/index.html")
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/dashboard.html")
}

// --- Client handlers ---

type CreateClientRequest struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	BusinessName string `json:"business_name"`
	Niche        string `json:"niche"`
	Platforms    []string `json:"platforms"` // facebook, instagram, youtube, tiktok
	Timezone     string `json:"timezone"`
	NudgeDay     string `json:"nudge_day"`   // e.g. "tuesday"
	NudgeTime    string `json:"nudge_time"`  // e.g. "09:00"
	Plan         string `json:"plan"`        // free, pro, enterprise
}

func (s *Server) handleCreateClient(w http.ResponseWriter, r *http.Request) {
	var req CreateClientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	client := &store.Client{
		Name:         req.Name,
		Email:        req.Email,
		Phone:        req.Phone,
		BusinessName: req.BusinessName,
		Niche:        req.Niche,
		Platforms:    req.Platforms,
		Timezone:     req.Timezone,
		NudgeDay:     req.NudgeDay,
		NudgeTime:    req.NudgeTime,
		Plan:         req.Plan,
		VoiceProfile: "", // learned over time
		CreatedAt:    time.Now(),
	}

	if err := s.store.CreateClient(r.Context(), client); err != nil {
		http.Error(w, fmt.Sprintf("create failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Fetch back the auto-generated ID
	created, _ := s.store.GetClientByContact(r.Context(), client.Email)
	if created != nil {
		client.ID = created.ID
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(client)
}

func (s *Server) handleListClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.store.ListClients(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(clients)
}

func (s *Server) handleGetClient(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	client, err := s.store.GetClient(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(client)
}

// --- Chat handler ---

type ChatRequest struct {
	Message string `json:"message"`
}

type ChatResponse struct {
	Reply string `json:"reply"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// Load client context
	client, err := s.store.GetClient(r.Context(), id)
	if err != nil {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	// Load recent conversation history
	history, _ := s.store.GetRecentMessages(r.Context(), id, 20)

	// Get AI reply
	reply, err := s.agent.Chat(r.Context(), client, history, req.Message)
	if err != nil {
		http.Error(w, fmt.Sprintf("agent error: %v", err), http.StatusInternalServerError)
		return
	}

	// Save both messages
	s.store.SaveMessage(r.Context(), id, "client", req.Message)
	s.store.SaveMessage(r.Context(), id, "coach", reply)

	json.NewEncoder(w).Encode(ChatResponse{Reply: reply})
}

// --- Content generation ---

type GenerateRequest struct {
	Type   string `json:"type"`   // blog, video_script, instagram_caption, etc.
	Topic  string `json:"topic"`
	Target string `json:"target"` // platform name
}

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	client, err := s.store.GetClient(r.Context(), id)
	if err != nil {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	history, _ := s.store.GetRecentMessages(r.Context(), id, 30)

	output, err := s.content.Generate(r.Context(), content.Request{
		Client:     client,
		Type:       req.Type,
		Topic:      req.Topic,
		Target:     req.Target,
		History:    history,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("generation error: %v", err), http.StatusInternalServerError)
		return
	}

	// Save generated content
	s.store.SaveContent(r.Context(), id, req.Type, req.Topic, output)

	json.NewEncoder(w).Encode(map[string]string{
		"type":    req.Type,
		"content": output,
	})
}

// --- Client lookup ---

type LookupRequest struct {
	Contact string `json:"contact"` // email or phone
}

func (s *Server) handleLookupClient(w http.ResponseWriter, r *http.Request) {
	var req LookupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	client, err := s.store.GetClientByContact(r.Context(), req.Contact)
	if err != nil {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(client)
}

// --- Chat history ---

func (s *Server) handleChatHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	messages, err := s.store.GetRecentMessages(r.Context(), id, 50)
	if err != nil {
		json.NewEncoder(w).Encode([]*store.Message{})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

// --- SMS Webhook (client replies to nudge) ---

func (s *Server) handleSMSWebhook(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	from := r.FormValue("From")
	body := r.FormValue("Body")

	// Look up client by phone
	client, err := s.store.GetClientByPhone(r.Context(), from)
	if err != nil {
		log.Printf("SMS from unknown number: %s", from)
		// Still respond politely
		fmt.Fprintf(w, "Thanks for your message! A coach will follow up soon.")
		return
	}

	// Get AI reply
	history, _ := s.store.GetRecentMessages(r.Context(), client.ID, 20)
	reply, err := s.agent.Chat(r.Context(), client, history, body)
	if err != nil {
		log.Printf("Agent error for client %s: %v", client.ID, err)
		return
	}

	// Save messages
	s.store.SaveMessage(r.Context(), client.ID, "client", body)
	s.store.SaveMessage(r.Context(), client.ID, "coach", reply)

	// Send reply via SMS
	s.notify.SendSMS(client.Phone, reply)

	fmt.Fprintf(w, "OK")
}

// --- Stripe Webhook ---

func (s *Server) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	// TODO: verify Stripe signature
	// Handle: checkout.session.completed, customer.subscription.* events
	fmt.Fprintf(w, "OK")
}

// --- Nudge scheduler ---

func (s *Server) handleNudge(w http.ResponseWriter, r *http.Request) {
	// Find all clients whose nudge day/time matches now
	clients, err := s.store.ListClientsDueNudge(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, client := range clients {
		// Generate personalized nudge
		history, _ := s.store.GetRecentMessages(r.Context(), client.ID, 10)
		nudge, err := s.agent.GenerateNudge(r.Context(), client, history)
		if err != nil {
			log.Printf("Failed to generate nudge for %s: %v", client.ID, err)
			continue
		}

		// Send via SMS and/or email
		if client.Phone != "" {
			s.notify.SendSMS(client.Phone, nudge)
		}
		if client.Email != "" {
			s.notify.SendEmail(client.Email, "Weekly content check-in!", nudge)
		}

		// Save nudge as coach message
		s.store.SaveMessage(r.Context(), client.ID, "coach", nudge)
		s.store.UpdateLastNudge(r.Context(), client.ID)
	}

	json.NewEncoder(w).Encode(map[string]int{"nudged": len(clients)})
}
