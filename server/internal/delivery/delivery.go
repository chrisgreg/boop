// Package delivery fans an event out to every pushable device, records the
// outcome in the deliveries table, and logs push.sent / push.failed.
package delivery

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/chrisgreg/boop/server/internal/apns"
	"github.com/chrisgreg/boop/server/internal/devices"
	"github.com/chrisgreg/boop/server/internal/events"
	"github.com/chrisgreg/boop/server/internal/events/levels"
	"github.com/chrisgreg/boop/server/internal/ids"
	"github.com/chrisgreg/boop/server/internal/projects"
	"github.com/chrisgreg/boop/server/internal/webpush"
)

// Sender is the subset of *apns.Client the dispatcher needs.
type Sender interface {
	Send(ctx context.Context, deviceToken string, n apns.Notification) (string, error)
}

// WebPushSender is the subset of *webpush.Client the dispatcher needs.
type WebPushSender interface {
	Send(ctx context.Context, subscription webpush.Subscription, n webpush.Notification) (string, error)
}

// Statuses recorded in deliveries.status.
const (
	StatusSent    = "sent"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// Delivery is one attempt to push an event to a device.
type Delivery struct {
	ID          string `json:"id"`
	EventID     string `json:"event_id"`
	DeviceID    string `json:"device_id"`
	DeviceName  string `json:"device_name"`
	Transport   string `json:"transport,omitempty"`
	Status      string `json:"status"`
	APNSID      string `json:"apns_id,omitempty"`
	MessageID   string `json:"message_id,omitempty"`
	Error       string `json:"error,omitempty"`
	AttemptedAt string `json:"attempted_at"`
}

// Dispatcher sends notifications for events.
type Dispatcher struct {
	db            *sql.DB
	devices       *devices.Store
	sender        Sender // nil when APNs is not configured
	webPush       *webpush.Store
	webPushSender WebPushSender
	log           *slog.Logger
	timeout       time.Duration

	queue chan job
	wg    sync.WaitGroup
}

type job struct {
	event   events.Event
	project projects.Project
}

// New returns a Dispatcher. Either sender may be nil, in which case attempts
// for that transport are recorded as skipped.
func New(db *sql.DB, d *devices.Store, sender Sender, webStore *webpush.Store, webSender WebPushSender, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		db: db, devices: d, sender: sender, webPush: webStore, webPushSender: webSender,
		log: log, timeout: 10 * time.Second, queue: make(chan job, 1024),
	}
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
				d.Deliver(context.Background(), j.event, j.project)
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
		d.Deliver(context.Background(), e, p)
	}
}

// Deliver pushes e through every configured transport synchronously and
// returns all recorded attempts. A failure in one transport never blocks the
// other.
func (d *Dispatcher) Deliver(ctx context.Context, e events.Event, p projects.Project) []Delivery {
	if !p.Notify || !levels.AtLeast(e.Level, p.MinLevel) {
		return nil
	}
	out := d.deliverAPNS(ctx, e, p)
	out = append(out, d.deliverWebPush(ctx, e, p)...)
	return out
}

func (d *Dispatcher) deliverAPNS(ctx context.Context, e events.Event, p projects.Project) []Delivery {
	devs, err := d.devices.Pushable(ctx)
	if err != nil {
		d.log.Error("push.targets_failed", "transport", "apns", "event_id", e.ID, "error", err.Error())
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
		rec := Delivery{ID: ids.New("dlv"), EventID: e.ID, DeviceID: dev.ID, DeviceName: dev.Name, Transport: "apns", AttemptedAt: ids.Now()}
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
				d.log.Warn("push.failed", "transport", "apns", "event_id", e.ID, "device_id", dev.ID, "error", rec.Error)
			} else {
				rec.Status = StatusSent
				rec.APNSID = apnsID
				d.log.Info("push.sent", "transport", "apns", "event_id", e.ID, "device_id", dev.ID, "apns_id", apnsID)
			}
		}
		if _, err := d.db.ExecContext(ctx, `INSERT INTO deliveries (id, event_id, device_id, status, apns_id, error, attempted_at, created_at) VALUES (?,?,?,?,?,?,?,?)`,
			rec.ID, rec.EventID, rec.DeviceID, rec.Status, rec.APNSID, rec.Error, rec.AttemptedAt, rec.AttemptedAt); err != nil {
			d.log.Error("delivery.record_failed", "error", err.Error())
		}
		out = append(out, rec)
	}
	return out
}

func (d *Dispatcher) deliverWebPush(ctx context.Context, e events.Event, p projects.Project) []Delivery {
	if d.webPush == nil {
		return nil
	}
	subs, err := d.webPush.List(ctx)
	if err != nil {
		d.log.Error("push.targets_failed", "transport", "web_push", "event_id", e.ID, "error", err.Error())
		return nil
	}
	n := webpush.Notification{
		Title: p.Name + " · " + e.Title, Body: e.Body, EventID: e.ID, ProjectID: p.ID, Level: e.Level,
		URL: "/events/" + e.ID, Tag: "boop-" + e.ID, Icon: "/icons/icon-192.png", Badge: "/icons/badge-96.png",
	}
	if n.Body == "" {
		n.Body = e.Title
		n.Title = p.Name
	}
	out := make([]Delivery, 0, len(subs))
	for _, sub := range subs {
		rec := Delivery{ID: ids.New("dlv"), EventID: e.ID, DeviceID: sub.ID, DeviceName: sub.Name, Transport: "web_push", AttemptedAt: ids.Now()}
		expired := false
		if d.webPushSender == nil {
			rec.Status = StatusSkipped
			rec.Error = "Web Push is not configured"
		} else {
			sctx, cancel := context.WithTimeout(ctx, d.timeout)
			messageID, err := d.webPushSender.Send(sctx, sub, n)
			cancel()
			if err != nil {
				rec.Status = StatusFailed
				rec.Error = err.Error()
				expired = errors.Is(err, webpush.ErrSubscriptionExpired)
				d.log.Warn("push.failed", "transport", "web_push", "event_id", e.ID, "subscription_id", sub.ID, "expired", expired, "error", rec.Error)
			} else {
				rec.Status = StatusSent
				rec.MessageID = messageID
				if err := d.webPush.MarkSuccess(ctx, sub.ID); err != nil {
					d.log.Warn("push.subscription_update_failed", "subscription_id", sub.ID, "error", err.Error())
				}
				d.log.Info("push.sent", "transport", "web_push", "event_id", e.ID, "subscription_id", sub.ID)
			}
		}
		if _, err := d.db.ExecContext(ctx, `INSERT INTO web_push_deliveries
			(id, event_id, subscription_id, target_name, status, message_id, error, attempted_at, created_at)
			VALUES (?,?,?,?,?,?,?,?,?)`, rec.ID, rec.EventID, sub.ID, rec.DeviceName, rec.Status, rec.MessageID, rec.Error, rec.AttemptedAt, rec.AttemptedAt); err != nil {
			d.log.Error("delivery.record_failed", "transport", "web_push", "error", err.Error())
		}
		if expired {
			if err := d.webPush.Delete(ctx, sub.ID); err != nil {
				d.log.Warn("push.subscription_delete_failed", "subscription_id", sub.ID, "error", err.Error())
			}
		}
		out = append(out, rec)
	}
	return out
}

// ForEvent lists deliveries recorded for an event.
func (d *Dispatcher) ForEvent(ctx context.Context, eventID string) ([]Delivery, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, event_id, target_id, target_name, transport, status, apns_id, message_id, error, attempted_at FROM (
		SELECT dl.id, dl.event_id, dl.device_id AS target_id, COALESCE(dv.name, '') AS target_name, 'apns' AS transport,
			dl.status, dl.apns_id, '' AS message_id, dl.error, dl.attempted_at
		FROM deliveries dl LEFT JOIN devices dv ON dv.id = dl.device_id WHERE dl.event_id = ?
		UNION ALL
		SELECT wd.id, wd.event_id, COALESCE(wd.subscription_id, '') AS target_id, wd.target_name, 'web_push' AS transport,
			wd.status, '' AS apns_id, wd.message_id, wd.error, wd.attempted_at
		FROM web_push_deliveries wd WHERE wd.event_id = ?
	) ORDER BY attempted_at DESC`, eventID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var r Delivery
		if err := scanDelivery(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Last returns the most recent delivery attempt, if any.
func (d *Dispatcher) Last(ctx context.Context) (*Delivery, error) {
	var r Delivery
	err := scanDelivery(d.db.QueryRowContext(ctx, `SELECT id, event_id, target_id, target_name, transport, status, apns_id, message_id, error, attempted_at FROM (
		SELECT dl.id, dl.event_id, dl.device_id AS target_id, COALESCE(dv.name, '') AS target_name, 'apns' AS transport,
			dl.status, dl.apns_id, '' AS message_id, dl.error, dl.attempted_at
		FROM deliveries dl LEFT JOIN devices dv ON dv.id = dl.device_id
		UNION ALL
		SELECT wd.id, wd.event_id, COALESCE(wd.subscription_id, '') AS target_id, wd.target_name, 'web_push' AS transport,
			wd.status, '' AS apns_id, wd.message_id, wd.error, wd.attempted_at
		FROM web_push_deliveries wd
	) ORDER BY attempted_at DESC LIMIT 1`), &r)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}

func scanDelivery(row scanner, r *Delivery) error {
	return row.Scan(&r.ID, &r.EventID, &r.DeviceID, &r.DeviceName, &r.Transport, &r.Status, &r.APNSID, &r.MessageID, &r.Error, &r.AttemptedAt)
}

type scanner interface {
	Scan(dest ...any) error
}
