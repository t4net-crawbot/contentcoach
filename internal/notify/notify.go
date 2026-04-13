package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"net/smtp"
	"strings"
)

type TwilioConfig struct {
	AccountSID string
	AuthToken  string
	FromPhone  string
}

type EmailConfig struct {
	From    string
	SMTPHost string
	SMTPPort string
	User     string
	Password string
}

type Manager struct {
	twilio TwilioConfig
	email  EmailConfig
}

func NewManager(twilio TwilioConfig, email EmailConfig) *Manager {
	if email.SMTPHost == "" {
		email.SMTPHost = "smtp.gmail.com"
	}
	if email.SMTPPort == "" {
		email.SMTPPort = "587"
	}
	return &Manager{twilio: twilio, email: email}
}

func (m *Manager) SendSMS(to, body string) error {
	if m.twilio.AccountSID == "" {
		return fmt.Errorf("twilio not configured")
	}

	endpoint := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json",
		m.twilio.AccountSID)

	data := url.Values{}
	data.Set("From", m.twilio.FromPhone)
	data.Set("To", to)
	data.Set("Body", body)

	req, _ := http.NewRequest("POST", endpoint, strings.NewReader(data.Encode()))
	req.SetBasicAuth(m.twilio.AccountSID, m.twilio.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("twilio request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var errResp map[string]any
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("twilio error (%d): %v", resp.StatusCode, errResp)
	}

	log.Printf("SMS sent to %s (%d chars)", to, len(body))
	return nil
}

func (m *Manager) SendEmail(to, subject, body string) error {
	if m.email.SMTPHost == "" || m.email.User == "" {
		// Fallback: use Cloudflare Worker email API
		return m.sendViaWorker(to, subject, body)
	}

	// SMTP send
	msg := fmt.Sprintf("From: %s\nTo: %s\nSubject: %s\nMIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8\n\n%s",
		m.email.From, to, subject, body)

	auth := smtp.PlainAuth("", m.email.User, m.email.Password, m.email.SMTPHost)
	addr := m.email.SMTPHost + ":" + m.email.SMTPPort

	if err := smtp.SendMail(addr, auth, m.email.From, []string{to}, []byte(msg)); err != nil {
		return fmt.Errorf("email send failed: %w", err)
	}

	log.Printf("Email sent to %s: %s", to, subject)
	return nil
}

// sendViaWorker uses a Cloudflare Worker endpoint to send email
// (since we don't have SMTP credentials, we can POST to our worker)
func (m *Manager) sendViaWorker(to, subject, body string) error {
	// POST to our email worker's /send endpoint
	payload := map[string]string{
		"to":      to,
		"from":    m.email.From,
		"subject": subject,
		"body":    body,
	}
	data, _ := json.Marshal(payload)

	// This would be our Cloudflare Worker endpoint
	resp, err := http.DefaultClient.Post(
		"https://fournet.win/api/send-email",
		"application/json",
		bytes.NewReader(data),
	)
	if err != nil {
		return fmt.Errorf("worker email failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("worker email error: %d", resp.StatusCode)
	}

	return nil
}
