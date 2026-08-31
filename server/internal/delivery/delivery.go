// Package delivery fans an event out to every pushable device, records the
// outcome in the deliveries table, and logs push.sent / push.failed.
package delivery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chrisgreg/boop/server/internal/apns"
	"github.com/chrisgreg/boop/server/internal/devices"
	"github.com/chrisgreg/boop/server/internal/events"
	"github.com/chrisgreg/boop/server/internal/events/levels"
	"github.com/chrisgreg/boop/server/internal/ids"
	"github.com/chrisgreg/boop/server/internal/projects"
	"github.com/chrisgreg/boop/server/internal/webhooks"
)

// Sender is the subset of *apns.Client the dispatcher needs.
type Sender interface {
	Send(ctx context.Context, deviceToken string, n apns.Notification) (string, error)
}

// WebhookSender sends an already-rendered webhook request and returns the HTTP
// status and a short response-body snippet.
type WebhookSender interface {
	Send(ctx context.Context, target string, body []byte, headers map[string]string) (status int, response string, err error)
}

type httpWebhookSender struct{ client *http.Client }

func (s *httpWebhookSender) Send(ctx context.Context, target string, body []byte, headers map[string]string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		return 0, "", err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	snippet, err := io.ReadAll(io.LimitReader(res.Body, 1024))
	if err != nil {
		return res.StatusCode, "", err
	}
	return res.StatusCode, strings.TrimSpace(string(snippet)), nil
}

// Statuses recorded in deliveries.status.
const (
	StatusSent    = "sent"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
	TargetDevice  = "device"
	TargetWebhook = "webhook"
)

// Delivery is one delivery attempt to either a device or a webhook target.
type Delivery struct {
	ID          string `json:"id"`
	EventID     string `json:"event_id"`
	DeviceID    string `json:"device_id,omitempty"`
	DeviceName  string `json:"device_name,omitempty"`
	TargetType  string `json:"target_type"`
	WebhookID   string `json:"webhook_id,omitempty"`
	WebhookHost string `json:"webhook_host,omitempty"`
	Status      string `json:"status"`
	APNSID      string `json:"apns_id,omitempty"`
	HTTPStatus  int    `json:"http_status,omitempty"`
	Error       string `json:"error,omitempty"`
	AttemptedAt string `json:"attempted_at"`
}

// Dispatcher sends notifications for events.
type Dispatcher struct {
	db            *sql.DB
	devices       *devices.Store
	sender        Sender // nil when APNs is not configured
	webhooks      *webhooks.Store
	webhookSender WebhookSender
	log           *slog.Logger
	timeout       time.Duration

	queue chan job
	wg    sync.WaitGroup
}

type job struct {
	event   events.Event
	project projects.Project
}

// New returns a Dispatcher. sender may be nil, in which case deliveries are
// recorded as skipped.
func New(db *sql.DB, d *devices.Store, sender Sender, log *slog.Logger) *Dispatcher {
	return &Dispatcher{db: db, devices: d, sender: sender, log: log, timeout: 10 * time.Second, queue: make(chan job, 1024)}
}

// ConfigureWebhooks enables webhook fan-out. A nil sender uses the standard
// HTTP client bounded by the dispatcher's timeout.
func (d *Dispatcher) ConfigureWebhooks(store *webhooks.Store, sender WebhookSender) {
	d.webhooks = store
	if store == nil {
		d.webhookSender = nil
		return
	}
	if sender == nil {
		sender = &httpWebhookSender{client: &http.Client{Timeout: d.timeout}}
	}
	d.webhookSender = sender
}

// Start runs the background worker until ctx is cancelled.
func (d *Dispatcher) Start(ctx context.Context) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case j := <-d.queue:
				d.deliver(context.Background(), j.event, j.project, true)
			}
		}
	}()
}

// Wait blocks until the worker has stopped.
func (d *Dispatcher) Wait() { d.wg.Wait() }

// Enqueue schedules delivery without blocking the caller. If the queue is
// full the event is delivered synchronously instead.
func (d *Dispatcher) Enqueue(e events.Event, p projects.Project) {
	select {
	case d.queue <- job{event: e, project: p}:
	default:
		d.deliver(context.Background(), e, p, true)
	}
}

// Deliver sends only device notifications. It is used by explicit phone tests
// and unsilencing; ingestion uses Enqueue and includes webhook fan-out.
func (d *Dispatcher) Deliver(ctx context.Context, e events.Event, p projects.Project) []Delivery {
	return d.deliver(ctx, e, p, false)
}

func (d *Dispatcher) deliver(ctx context.Context, e events.Event, p projects.Project, includeWebhooks bool) []Delivery {
	out := d.deliverDevices(ctx, e, p)
	if includeWebhooks {
		out = append(out, d.deliverWebhooks(ctx, e, p)...)
	}
	return out
}

func (d *Dispatcher) deliverDevices(ctx context.Context, e events.Event, p projects.Project) []Delivery {
	if !p.Notify || !levels.AtLeast(e.Level, p.MinLevel) {
		return nil
	}
	devs, err := d.devices.Pushable(ctx)
	if err != nil {
		d.log.Error("push.failed", "event_id", e.ID, "error", err.Error())
		return nil
	}
	n := apns.Notification{
		Title:     p.Name + " · " + e.Title,
		Body:      e.Body,
		EventID:   e.ID,
		ProjectID: p.ID,
		Prominent: e.Level == levels.Critical,
	}
	for _, a := range e.Actions {
		n.Actions = append(n.Actions, apns.Action{Label: a.Label, URL: a.URL})
	}
	if n.Body == "" {
		n.Body = e.Title
		n.Title = p.Name
	}
	var out []Delivery
	for _, dev := range devs {
		rec := Delivery{ID: ids.New("dlv"), EventID: e.ID, DeviceID: dev.ID, DeviceName: dev.Name, TargetType: TargetDevice, AttemptedAt: ids.Now()}
		if d.sender == nil {
			rec.Status = StatusSkipped
			rec.Error = "APNs is not configured"
		} else {
			sctx, cancel := context.WithTimeout(ctx, d.timeout)
			apnsID, err := d.sender.Send(sctx, *dev.DeviceToken, n)
			cancel()
			if err != nil {
				rec.Status = StatusFailed
				rec.Error = err.Error()
				var ae *apns.Error
				if errors.As(err, &ae) && ae.Unregistered() {
					_ = d.devices.ClearToken(ctx, dev.ID)
				}
				d.log.Warn("push.failed", "event_id", e.ID, "device_id", dev.ID, "error", rec.Error)
			} else {
				rec.Status = StatusSent
				rec.APNSID = apnsID
				d.log.Info("push.sent", "event_id", e.ID, "device_id", dev.ID, "apns_id", apnsID)
			}
		}
		d.record(ctx, rec)
		out = append(out, rec)
	}
	return out
}

func (d *Dispatcher) deliverWebhooks(ctx context.Context, e events.Event, p projects.Project) []Delivery {
	if d.webhooks == nil {
		return nil
	}
	targets, err := d.webhooks.ListEnabled(ctx, p.ID)
	if err != nil {
		d.log.Error("webhook.list_failed", "project_id", p.ID, "error", err.Error())
		return nil
	}
	out := make([]Delivery, 0, len(targets))
	for _, target := range targets {
		minLevel := target.MinLevel
		if minLevel == "" {
			minLevel = p.MinLevel
		}
		rec := Delivery{ID: ids.New("dlv"), EventID: e.ID, TargetType: TargetWebhook, WebhookID: target.ID, WebhookHost: webhookHost(target.URL), AttemptedAt: ids.Now()}
		if !levels.AtLeast(e.Level, minLevel) {
			rec.Status = StatusSkipped
			rec.Error = "event level below webhook minimum"
		} else {
			d.sendWebhook(ctx, &rec, target, e, p)
		}
		d.record(ctx, rec)
		out = append(out, rec)
	}
	return out
}

// TestWebhook sends a synthetic event to one target without recording a
// delivery, because the event has no corresponding database row.
func (d *Dispatcher) TestWebhook(ctx context.Context, target webhooks.Webhook, e events.Event, p projects.Project) Delivery {
	rec := Delivery{ID: ids.New("dlv"), TargetType: TargetWebhook, WebhookID: target.ID, WebhookHost: webhookHost(target.URL), AttemptedAt: ids.Now()}
	d.sendWebhook(ctx, &rec, target, e, p)
	return rec
}

func (d *Dispatcher) sendWebhook(ctx context.Context, rec *Delivery, target webhooks.Webhook, e events.Event, p projects.Project) {
	body, headers, err := webhooks.Render(target, e, p)
	if err != nil {
		rec.Status = StatusFailed
		rec.Error = err.Error()
		return
	}
	if d.webhookSender == nil {
		rec.Status = StatusSkipped
		rec.Error = "webhooks are not configured"
		return
	}
	sctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	status, response, err := d.webhookSender.Send(sctx, target.URL, body, headers)
	rec.HTTPStatus = status
	if err != nil {
		rec.Status = StatusFailed
		rec.Error = err.Error()
		d.log.Warn("webhook.failed", "event_id", e.ID, "webhook_id", target.ID, "error", rec.Error)
		return
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		rec.Status = StatusFailed
		rec.Error = fmt.Sprintf("webhook returned HTTP %d", status)
		if response != "" {
			rec.Error += ": " + response
		}
		d.log.Warn("webhook.failed", "event_id", e.ID, "webhook_id", target.ID, "status", status)
		return
	}
	rec.Status = StatusSent
	d.log.Info("webhook.sent", "event_id", e.ID, "webhook_id", target.ID, "status", status)
}

func (d *Dispatcher) record(ctx context.Context, rec Delivery) {
	var deviceID, webhookID any
	if rec.DeviceID != "" {
		deviceID = rec.DeviceID
	}
	if rec.WebhookID != "" {
		webhookID = rec.WebhookID
	}
	var httpStatus any
	if rec.HTTPStatus != 0 {
		httpStatus = rec.HTTPStatus
	}
	if _, err := d.db.ExecContext(ctx, `INSERT INTO deliveries (id, event_id, device_id, target_type, webhook_id, webhook_host, status, apns_id, http_status, error, attempted_at, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.ID, rec.EventID, deviceID, rec.TargetType, webhookID, rec.WebhookHost, rec.Status, rec.APNSID, httpStatus, rec.Error, rec.AttemptedAt, rec.AttemptedAt); err != nil {
		d.log.Error("delivery.record_failed", "error", err.Error())
	}
}

func webhookHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// ForEvent lists deliveries recorded for an event.
func (d *Dispatcher) ForEvent(ctx context.Context, eventID string) ([]Delivery, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT dl.id, dl.event_id, COALESCE(dl.device_id, ''), COALESCE(dv.name, ''), dl.target_type, COALESCE(dl.webhook_id, ''), dl.webhook_host, dl.status, dl.apns_id, COALESCE(dl.http_status, 0), dl.error, dl.attempted_at
		FROM deliveries dl LEFT JOIN devices dv ON dv.id = dl.device_id WHERE dl.event_id = ? ORDER BY dl.attempted_at DESC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var r Delivery
		if err := rows.Scan(&r.ID, &r.EventID, &r.DeviceID, &r.DeviceName, &r.TargetType, &r.WebhookID, &r.WebhookHost, &r.Status, &r.APNSID, &r.HTTPStatus, &r.Error, &r.AttemptedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Last returns the most recent delivery attempt, if any.
func (d *Dispatcher) Last(ctx context.Context) (*Delivery, error) {
	var r Delivery
	err := d.db.QueryRowContext(ctx, `SELECT dl.id, dl.event_id, COALESCE(dl.device_id, ''), COALESCE(dv.name, ''), dl.target_type, COALESCE(dl.webhook_id, ''), dl.webhook_host, dl.status, dl.apns_id, COALESCE(dl.http_status, 0), dl.error, dl.attempted_at
		FROM deliveries dl LEFT JOIN devices dv ON dv.id = dl.device_id WHERE dl.target_type = ? ORDER BY dl.attempted_at DESC LIMIT 1`, TargetDevice).
		Scan(&r.ID, &r.EventID, &r.DeviceID, &r.DeviceName, &r.TargetType, &r.WebhookID, &r.WebhookHost, &r.Status, &r.APNSID, &r.HTTPStatus, &r.Error, &r.AttemptedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}
