// Package webhooks manages outbound webhook targets for projects.
package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/chrisgreg/boop/server/internal/events/levels"
	"github.com/chrisgreg/boop/server/internal/ids"
)

var ErrNotFound = errors.New("webhook not found")
var ErrInvalid = errors.New("invalid webhook")

const (
	PayloadModeJSON   = "json"
	PayloadModeCustom = "custom"
	MaskedHeaderValue = "********"
)

// Webhook is a configured project target. Headers are masked except in the
// delivery-only accessors, so their values are write-only through the API.
type Webhook struct {
	ID           string            `json:"id"`
	ProjectID    string            `json:"project_id"`
	URL          string            `json:"url"`
	PayloadMode  string            `json:"payload_mode"`
	BodyTemplate string            `json:"body_template"`
	Headers      map[string]string `json:"headers"`
	MinLevel     string            `json:"min_level"`
	Enabled      bool              `json:"enabled"`
	CreatedAt    string            `json:"created_at"`
	UpdatedAt    string            `json:"updated_at"`
}

// Input is the writable subset of a webhook. Omitted fields are unchanged on
// update. Headers must be a JSON object containing string values.
type Input struct {
	URL          *string          `json:"url"`
	PayloadMode  *string          `json:"payload_mode"`
	BodyTemplate *string          `json:"body_template"`
	Headers      *json.RawMessage `json:"headers"`
	MinLevel     *string          `json:"min_level"`
	Enabled      *bool            `json:"enabled"`
}

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

const cols = `id, project_id, url, payload_mode, body_template, headers_json, min_level, enabled, created_at, updated_at`

func scan(row interface{ Scan(...any) error }, maskHeaders bool) (Webhook, error) {
	var w Webhook
	var headers string
	var enabled int
	err := row.Scan(&w.ID, &w.ProjectID, &w.URL, &w.PayloadMode, &w.BodyTemplate, &headers, &w.MinLevel, &enabled, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return Webhook{}, err
	}
	if err := json.Unmarshal([]byte(headers), &w.Headers); err != nil {
		return Webhook{}, fmt.Errorf("decode headers: %w", err)
	}
	if w.Headers == nil {
		w.Headers = map[string]string{}
	}
	if maskHeaders {
		for key := range w.Headers {
			w.Headers[key] = MaskedHeaderValue
		}
	}
	w.Enabled = enabled == 1
	return w, nil
}

func (s *Store) Create(ctx context.Context, projectID string, in Input) (Webhook, error) {
	w := Webhook{ID: ids.New("wh"), ProjectID: strings.TrimSpace(projectID), PayloadMode: PayloadModeJSON, Enabled: true, Headers: map[string]string{}, CreatedAt: ids.Now(), UpdatedAt: ids.Now()}
	if in.URL != nil {
		w.URL = strings.TrimSpace(*in.URL)
	}
	if in.PayloadMode != nil {
		w.PayloadMode = strings.ToLower(strings.TrimSpace(*in.PayloadMode))
	}
	if in.BodyTemplate != nil {
		w.BodyTemplate = *in.BodyTemplate
	}
	if in.MinLevel != nil {
		w.MinLevel = strings.ToLower(strings.TrimSpace(*in.MinLevel))
	}
	if in.Enabled != nil {
		w.Enabled = *in.Enabled
	}
	if in.Headers != nil {
		var err error
		w.Headers, err = parseHeaders(*in.Headers)
		if err != nil {
			return Webhook{}, err
		}
	}
	if err := validate(w); err != nil {
		return Webhook{}, err
	}
	headers, _ := json.Marshal(w.Headers)
	_, err := s.db.ExecContext(ctx, `INSERT INTO webhooks (`+cols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`, w.ID, w.ProjectID, w.URL, w.PayloadMode, w.BodyTemplate, string(headers), w.MinLevel, boolInt(w.Enabled), w.CreatedAt, w.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return Webhook{}, fmt.Errorf("%w: unknown project", ErrInvalid)
		}
		return Webhook{}, err
	}
	return s.Get(ctx, w.ID)
}

func (s *Store) List(ctx context.Context, projectID string) ([]Webhook, error) {
	return s.list(ctx, `SELECT `+cols+` FROM webhooks WHERE project_id = ? ORDER BY created_at DESC`, true, projectID)
}

// ListEnabled returns the delivery-ready configuration, including header values.
func (s *Store) ListEnabled(ctx context.Context, projectID string) ([]Webhook, error) {
	return s.list(ctx, `SELECT `+cols+` FROM webhooks WHERE project_id = ? AND enabled = 1 ORDER BY created_at ASC`, false, projectID)
}

func (s *Store) list(ctx context.Context, query string, maskHeaders bool, args ...any) ([]Webhook, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Webhook{}
	for rows.Next() {
		w, err := scan(rows, maskHeaders)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id string) (Webhook, error) { return s.get(ctx, id, true) }

// GetForDelivery returns the unmasked target configuration for a dispatcher.
func (s *Store) GetForDelivery(ctx context.Context, id string) (Webhook, error) {
	return s.get(ctx, id, false)
}

func (s *Store) get(ctx context.Context, id string, maskHeaders bool) (Webhook, error) {
	w, err := scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM webhooks WHERE id = ?`, id), maskHeaders)
	if errors.Is(err, sql.ErrNoRows) {
		return Webhook{}, ErrNotFound
	}
	return w, err
}

func (s *Store) Update(ctx context.Context, id string, in Input) (Webhook, error) {
	w, err := s.GetForDelivery(ctx, id)
	if err != nil {
		return Webhook{}, err
	}
	if in.URL != nil {
		w.URL = strings.TrimSpace(*in.URL)
	}
	if in.PayloadMode != nil {
		w.PayloadMode = strings.ToLower(strings.TrimSpace(*in.PayloadMode))
	}
	if in.BodyTemplate != nil {
		w.BodyTemplate = *in.BodyTemplate
	}
	if in.MinLevel != nil {
		w.MinLevel = strings.ToLower(strings.TrimSpace(*in.MinLevel))
	}
	if in.Enabled != nil {
		w.Enabled = *in.Enabled
	}
	if in.Headers != nil {
		w.Headers, err = parseHeaders(*in.Headers)
		if err != nil {
			return Webhook{}, err
		}
	}
	if err := validate(w); err != nil {
		return Webhook{}, err
	}
	w.UpdatedAt = ids.Now()
	headers, _ := json.Marshal(w.Headers)
	_, err = s.db.ExecContext(ctx, `UPDATE webhooks SET url=?, payload_mode=?, body_template=?, headers_json=?, min_level=?, enabled=?, updated_at=? WHERE id=?`, w.URL, w.PayloadMode, w.BodyTemplate, string(headers), w.MinLevel, boolInt(w.Enabled), w.UpdatedAt, w.ID)
	if err != nil {
		return Webhook{}, err
	}
	return s.Get(ctx, w.ID)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func validate(w Webhook) error {
	u, err := url.Parse(w.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%w: url must be absolute", ErrInvalid)
	}
	if w.PayloadMode != PayloadModeJSON && w.PayloadMode != PayloadModeCustom {
		return fmt.Errorf("%w: payload_mode must be %q or %q", ErrInvalid, PayloadModeJSON, PayloadModeCustom)
	}
	if w.MinLevel != "" && !levels.Valid(w.MinLevel) {
		return fmt.Errorf("%w: min_level must be one of %s", ErrInvalid, strings.Join(levels.All, ", "))
	}
	return nil
}

func parseHeaders(raw json.RawMessage) (map[string]string, error) {
	var headers map[string]string
	if len(raw) == 0 || json.Unmarshal(raw, &headers) != nil || headers == nil {
		return nil, fmt.Errorf("%w: headers must be a JSON object with string values", ErrInvalid)
	}
	return headers, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
