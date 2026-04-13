package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Client struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	Phone        string    `json:"phone"`
	BusinessName string    `json:"business_name"`
	Niche        string    `json:"niche"`
	Platforms    []string  `json:"platforms"`
	Timezone     string    `json:"timezone"`
	NudgeDay     string    `json:"nudge_day"`
	NudgeTime    string    `json:"nudge_time"`
	Plan         string    `json:"plan"`
	VoiceProfile string    `json:"voice_profile"`
	LastNudgeAt  *time.Time `json:"last_nudge_at"`
	CreatedAt    time.Time `json:"created_at"`
}

type Message struct {
	ID        string    `json:"id"`
	ClientID  string    `json:"client_id"`
	Role      string    `json:"role"` // "client" or "coach"
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type Content struct {
	ID        string    `json:"id"`
	ClientID  string    `json:"client_id"`
	Type      string    `json:"type"`  // blog, video_script, instagram_caption, etc.
	Topic     string    `json:"topic"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type Store interface {
	CreateClient(ctx context.Context, c *Client) error
	GetClient(ctx context.Context, id string) (*Client, error)
	GetClientByPhone(ctx context.Context, phone string) (*Client, error)
	GetClientByContact(ctx context.Context, contact string) (*Client, error)
	ListClients(ctx context.Context) ([]*Client, error)
	ListClientsDueNudge(ctx context.Context) ([]*Client, error)
	UpdateLastNudge(ctx context.Context, id string) error

	SaveMessage(ctx context.Context, clientID, role, content string) error
	GetRecentMessages(ctx context.Context, clientID string, limit int) ([]*Message, error)

	SaveContent(ctx context.Context, clientID, typ, topic, body string) error
}

// --- SQLite Implementation ---

type SQLite struct {
	db *sql.DB
}

func NewSQLite(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration: %w", err)
	}

	return &SQLite{db: db}, nil
}

func (s *SQLite) Close() error {
	return s.db.Close()
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS clients (
		id TEXT PRIMARY KEY DEFAULT(lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)),2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' || hex(randomblob(6)))),
		name TEXT NOT NULL,
		email TEXT,
		phone TEXT,
		business_name TEXT,
		niche TEXT,
		platforms TEXT DEFAULT '[]',
		timezone TEXT DEFAULT 'America/Chicago',
		nudge_day TEXT DEFAULT 'tuesday',
		nudge_time TEXT DEFAULT '09:00',
		plan TEXT DEFAULT 'free',
		voice_profile TEXT DEFAULT '',
		last_nudge_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		client_id TEXT NOT NULL REFERENCES clients(id),
		role TEXT NOT NULL CHECK(role IN ('client', 'coach')),
		content TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS content (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		client_id TEXT NOT NULL REFERENCES clients(id),
		type TEXT NOT NULL,
		topic TEXT NOT NULL,
		body TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_messages_client ON messages(client_id, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_clients_phone ON clients(phone);
	CREATE INDEX IF NOT EXISTS idx_clients_nudge ON clients(nudge_day, nudge_time);
	`
	_, err := db.Exec(schema)
	return err
}

func (s *SQLite) CreateClient(ctx context.Context, c *Client) error {
	platforms, _ := json.Marshal(c.Platforms)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO clients (name, email, phone, business_name, niche, platforms, timezone, nudge_day, nudge_time, plan)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Email, c.Phone, c.BusinessName, c.Niche, string(platforms),
		c.Timezone, c.NudgeDay, c.NudgeTime, c.Plan,
	)
	return err
}

func (s *SQLite) GetClient(ctx context.Context, id string) (*Client, error) {
	return s.getClient(ctx, "SELECT * FROM clients WHERE id = ?", id)
}

func (s *SQLite) GetClientByPhone(ctx context.Context, phone string) (*Client, error) {
	return s.getClient(ctx, "SELECT * FROM clients WHERE phone = ?", phone)
}

func (s *SQLite) GetClientByContact(ctx context.Context, contact string) (*Client, error) {
	// Try phone first, then email
	c, err := s.getClient(ctx, "SELECT * FROM clients WHERE phone = ? OR email = ?", contact, contact)
	if err != nil {
		// Also try normalized phone
		normalized := strings.ReplaceAll(contact, " ", "")
		normalized = strings.ReplaceAll(normalized, "-", "")
		normalized = strings.ReplaceAll(normalized, "(", "")
		normalized = strings.ReplaceAll(normalized, ")", "")
		c, err = s.getClient(ctx, "SELECT * FROM clients WHERE phone LIKE ? OR email = ?",
			"%"+normalized+"%", contact)
	}
	return c, err
}

func (s *SQLite) getClient(ctx context.Context, query string, args ...any) (*Client, error) {
	row := s.db.QueryRowContext(ctx, query, args...)
	var c Client
	var platformsJSON string
	var lastNudge sql.NullTime

	err := row.Scan(&c.ID, &c.Name, &c.Email, &c.Phone, &c.BusinessName, &c.Niche,
		&platformsJSON, &c.Timezone, &c.NudgeDay, &c.NudgeTime, &c.Plan,
		&c.VoiceProfile, &lastNudge, &c.CreatedAt)
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(platformsJSON), &c.Platforms)
	if lastNudge.Valid {
		c.LastNudgeAt = &lastNudge.Time
	}
	return &c, nil
}

func (s *SQLite) ListClients(ctx context.Context) ([]*Client, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT * FROM clients ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clients []*Client
	for rows.Next() {
		var c Client
		var platformsJSON string
		var lastNudge sql.NullTime
		err := rows.Scan(&c.ID, &c.Name, &c.Email, &c.Phone, &c.BusinessName, &c.Niche,
			&platformsJSON, &c.Timezone, &c.NudgeDay, &c.NudgeTime, &c.Plan,
			&c.VoiceProfile, &lastNudge, &c.CreatedAt)
		if err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(platformsJSON), &c.Platforms)
		if lastNudge.Valid {
			c.LastNudgeAt = &lastNudge.Time
		}
		clients = append(clients, &c)
	}
	return clients, nil
}

func (s *SQLite) ListClientsDueNudge(ctx context.Context) ([]*Client, error) {
	now := time.Now()
	dayName := strings.ToLower(now.Format("Monday"))
	timeStr := now.Format("15:04")

	// Find clients whose nudge_day is today and nudge_time is within last hour
	// and who haven't been nudged in the last 6 days
	rows, err := s.db.QueryContext(ctx, `
		SELECT * FROM clients
		WHERE nudge_day = ? AND nudge_time <= ?
		AND (last_nudge_at IS NULL OR last_nudge_at < datetime('now', '-6 days'))
		AND plan != 'cancelled'`,
		dayName, timeStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clients []*Client
	for rows.Next() {
		var c Client
		var platformsJSON string
		var lastNudge sql.NullTime
		rows.Scan(&c.ID, &c.Name, &c.Email, &c.Phone, &c.BusinessName, &c.Niche,
			&platformsJSON, &c.Timezone, &c.NudgeDay, &c.NudgeTime, &c.Plan,
			&c.VoiceProfile, &lastNudge, &c.CreatedAt)
		json.Unmarshal([]byte(platformsJSON), &c.Platforms)
		if lastNudge.Valid {
			c.LastNudgeAt = &lastNudge.Time
		}
		clients = append(clients, &c)
	}
	return clients, nil
}

func (s *SQLite) UpdateLastNudge(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE clients SET last_nudge_at = CURRENT_TIMESTAMP WHERE id = ?", id)
	return err
}

func (s *SQLite) SaveMessage(ctx context.Context, clientID, role, content string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO messages (client_id, role, content) VALUES (?, ?, ?)",
		clientID, role, content)
	return err
}

func (s *SQLite) GetRecentMessages(ctx context.Context, clientID string, limit int) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, client_id, role, content, created_at FROM messages WHERE client_id = ? ORDER BY created_at DESC LIMIT ?",
		clientID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		var m Message
		rows.Scan(&m.ID, &m.ClientID, &m.Role, &m.Content, &m.CreatedAt)
		msgs = append(msgs, &m)
	}
	// Reverse to get chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func (s *SQLite) SaveContent(ctx context.Context, clientID, typ, topic, body string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO content (client_id, type, topic, body) VALUES (?, ?, ?, ?)",
		clientID, typ, topic, body)
	return err
}
