// Package webpush owns browser push subscriptions, durable VAPID identity,
// and standards-based Web Push delivery.
package webpush

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	pushlib "github.com/marknefedov/go-webpush/v2"

	"github.com/chrisgreg/boop/server/internal/ids"
)

const (
	maxEndpoint  = 4096
	maxName      = 100
	maxUserAgent = 500
	defaultName  = "Web app"
	defaultTTL   = 3600
	maxPayload   = 3500
)

var (
	// ErrInvalid is returned when a browser subscription is malformed.
	ErrInvalid = errors.New("invalid web push subscription")
	// ErrSubscriptionExpired marks a 404/410 response from a push service.
	ErrSubscriptionExpired = errors.New("web push subscription expired")
)

// Keys are the browser-generated encryption keys for a PushSubscription.
type Keys struct {
	P256DH string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// Input is accepted from PushSubscription.toJSON().
type Input struct {
	Endpoint string `json:"endpoint"`
	Keys     Keys   `json:"keys"`
	Name     string `json:"name"`
}

// Subscription is a stored browser push target. Private encryption material
// is intentionally omitted from its JSON representation.
type Subscription struct {
	ID            string `json:"id"`
	Endpoint      string `json:"-"`
	P256DH        string `json:"-"`
	Auth          string `json:"-"`
	Name          string `json:"name"`
	UserAgent     string `json:"user_agent,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// Notification is the safe payload displayed by the service worker.
type Notification struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	EventID   string `json:"event_id"`
	ProjectID string `json:"project_id"`
	Level     string `json:"level"`
	URL       string `json:"url"`
	Tag       string `json:"tag"`
	Icon      string `json:"icon"`
	Badge     string `json:"badge"`
}

// Store persists Web Push state in the same SQLite backup boundary as events.
type Store struct {
	db *sql.DB
}

// NewStore returns a Web Push store.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// LoadOrCreateVAPIDKeys returns the server's stable VAPID identity. The
// private key is generated once and retained by normal database backups.
func (s *Store) LoadOrCreateVAPIDKeys(ctx context.Context) (*pushlib.VAPIDKeys, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT keys_json FROM web_push_configuration WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		keys, err := pushlib.GenerateVAPIDKeys()
		if err != nil {
			return nil, fmt.Errorf("generate VAPID keys: %w", err)
		}
		body, err := json.Marshal(keys)
		if err != nil {
			return nil, fmt.Errorf("encode VAPID keys: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO web_push_configuration (id, keys_json, created_at) VALUES (1, ?, ?)`, string(body), ids.Now()); err != nil {
			return nil, fmt.Errorf("store VAPID keys: %w", err)
		}
		if err := s.db.QueryRowContext(ctx, `SELECT keys_json FROM web_push_configuration WHERE id = 1`).Scan(&raw); err != nil {
			return nil, fmt.Errorf("reload VAPID keys: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("load VAPID keys: %w", err)
	}
	var keys pushlib.VAPIDKeys
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("decode VAPID keys: %w", err)
	}
	return &keys, nil
}

// Upsert validates and stores a browser subscription by endpoint.
func (s *Store) Upsert(ctx context.Context, in Input, userAgent string) (Subscription, error) {
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	in.Name = strings.TrimSpace(in.Name)
	userAgent = strings.TrimSpace(userAgent)
	if in.Name == "" {
		in.Name = defaultName
	}
	if err := validate(in, userAgent); err != nil {
		return Subscription{}, err
	}
	now := ids.Now()
	row := s.db.QueryRowContext(ctx, `INSERT INTO web_push_subscriptions
		(id, endpoint, p256dh, auth, name, user_agent, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(endpoint) DO UPDATE SET
			p256dh = excluded.p256dh,
			auth = excluded.auth,
			name = excluded.name,
			user_agent = excluded.user_agent,
			updated_at = excluded.updated_at
		RETURNING id, endpoint, p256dh, auth, name, user_agent, COALESCE(last_success_at, ''), created_at, updated_at`,
		ids.New("wps"), in.Endpoint, in.Keys.P256DH, in.Keys.Auth, in.Name, userAgent, now, now)
	return scanSubscription(row)
}

func validate(in Input, userAgent string) error {
	if in.Endpoint == "" || len(in.Endpoint) > maxEndpoint {
		return fmt.Errorf("%w: endpoint is required and must be at most %d characters", ErrInvalid, maxEndpoint)
	}
	u, err := url.Parse(in.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("%w: endpoint must be an HTTPS URL without credentials", ErrInvalid)
	}
	if len(in.Name) > maxName {
		return fmt.Errorf("%w: name must be at most %d characters", ErrInvalid, maxName)
	}
	if len(userAgent) > maxUserAgent {
		return fmt.Errorf("%w: user agent must be at most %d characters", ErrInvalid, maxUserAgent)
	}
	if _, err := pushlib.DecodeSubscriptionKeys(in.Keys.Auth, in.Keys.P256DH); err != nil {
		return fmt.Errorf("%w: invalid subscription keys: %v", ErrInvalid, err)
	}
	return nil
}

// List returns all active browser subscriptions.
func (s *Store) List(ctx context.Context) ([]Subscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, endpoint, p256dh, auth, name, user_agent, COALESCE(last_success_at, ''), created_at, updated_at
		FROM web_push_subscriptions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// Count returns the number of active browser subscriptions.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM web_push_subscriptions`).Scan(&n)
	return n, err
}

// Delete removes a subscription by id. It is idempotent.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM web_push_subscriptions WHERE id = ?`, id)
	return err
}

// DeleteEndpoint removes a browser subscription. It is idempotent so a user
// can disable notifications even after the push service has expired it.
func (s *Store) DeleteEndpoint(ctx context.Context, endpoint string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM web_push_subscriptions WHERE endpoint = ?`, strings.TrimSpace(endpoint))
	return err
}

// MarkSuccess records that the push service accepted the subscription.
func (s *Store) MarkSuccess(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE web_push_subscriptions SET last_success_at = ?, updated_at = ? WHERE id = ?`, ids.Now(), ids.Now(), id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSubscription(row scanner) (Subscription, error) {
	var sub Subscription
	err := row.Scan(&sub.ID, &sub.Endpoint, &sub.P256DH, &sub.Auth, &sub.Name, &sub.UserAgent, &sub.LastSuccessAt, &sub.CreatedAt, &sub.UpdatedAt)
	return sub, err
}

// Client sends encrypted notifications through browser push services.
type Client struct {
	client  *pushlib.Client
	keys    *pushlib.VAPIDKeys
	subject string
}

// NewClient loads the durable VAPID identity and constructs a bounded sender.
func NewClient(ctx context.Context, store *Store, subject string) (*Client, error) {
	keys, err := store.LoadOrCreateVAPIDKeys(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(subject) == "" {
		subject = "mailto:boop@localhost"
	}
	return &Client{
		client:  pushlib.NewClient(pushlib.Config{HTTPClient: &http.Client{Timeout: 10 * time.Second}}),
		keys:    keys,
		subject: subject,
	}, nil
}

// PublicKey is safe to expose to the browser when subscribing.
func (c *Client) PublicKey() string { return c.keys.PublicKeyString() }

// Send encrypts and sends one notification.
func (c *Client) Send(ctx context.Context, sub Subscription, n Notification) (string, error) {
	keys, err := pushlib.DecodeSubscriptionKeys(sub.Auth, sub.P256DH)
	if err != nil {
		return "", fmt.Errorf("decode subscription keys: %w", err)
	}
	body, err := marshalNotification(n)
	if err != nil {
		return "", err
	}
	result, err := c.client.Send(ctx, body, &pushlib.Subscription{Endpoint: sub.Endpoint, Keys: keys}, pushlib.SendOptions{
		Subject:   c.subject,
		VAPIDKeys: c.keys,
		TTL:       defaultTTL,
		Urgency:   urgency(n.Level),
		Topic:     topic(n.EventID),
	})
	if result != nil && result.Response != nil {
		result.Response.Body.Close()
	}
	if err != nil {
		var serviceErr *pushlib.PushServiceError
		if errors.As(err, &serviceErr) && serviceErr.SubscriptionExpired {
			return "", fmt.Errorf("%w: %v", ErrSubscriptionExpired, err)
		}
		return "", err
	}
	if result.MessageURL != "" {
		return result.MessageURL, nil
	}
	return fmt.Sprintf("HTTP %d", result.StatusCode), nil
}

func marshalNotification(n Notification) ([]byte, error) {
	body, err := json.Marshal(n)
	if err != nil || len(body) <= maxPayload {
		return body, err
	}
	runes := []rune(n.Body)
	low, high := 0, len(runes)
	best := ""
	for low <= high {
		middle := low + (high-low)/2
		candidate := string(runes[:middle])
		if middle < len(runes) {
			candidate += "…"
		}
		n.Body = candidate
		encoded, err := json.Marshal(n)
		if err != nil {
			return nil, err
		}
		if len(encoded) <= maxPayload {
			best = candidate
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	n.Body = best
	return json.Marshal(n)
}

func urgency(level string) pushlib.Urgency {
	switch level {
	case "critical", "error":
		return pushlib.UrgencyHigh
	case "warning":
		return pushlib.UrgencyNormal
	default:
		return pushlib.UrgencyLow
	}
}

func topic(eventID string) string {
	// Event ids contain only the generated prefix and URL-safe random bytes.
	// Keep the topic comfortably below the Web Push 32-byte limit.
	if len(eventID) > 28 {
		eventID = eventID[len(eventID)-28:]
	}
	return eventID
}
