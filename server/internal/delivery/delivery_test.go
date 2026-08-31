package delivery

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrisgreg/boop/server/internal/database"
	"github.com/chrisgreg/boop/server/internal/devices"
	"github.com/chrisgreg/boop/server/internal/events"
	"github.com/chrisgreg/boop/server/internal/projects"
	"github.com/chrisgreg/boop/server/internal/webhooks"
)

func TestWebhookDeliveryIgnoresPhoneNotifyAndRecordsOutcome(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(filepath.Join(t.TempDir(), "boop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, _, err := projects.New(db).Create(ctx, projects.Input{Name: stringPtr("Alerts")})
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan struct{}, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	store := webhooks.New(db)
	if _, err := store.Create(ctx, p.ID, webhooks.Input{URL: stringPtr(receiver.URL)}); err != nil {
		t.Fatal(err)
	}
	e, err := events.New(db).Create(ctx, p.ID, events.Input{Title: "Alert", Level: "error"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Notify = false
	d := New(db, devices.New(db), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.ConfigureWebhooks(store, nil)
	deliveries := d.deliver(ctx, e, p, true)
	if len(deliveries) != 1 || deliveries[0].Status != StatusSent || deliveries[0].TargetType != TargetWebhook || deliveries[0].HTTPStatus != http.StatusOK {
		t.Fatalf("deliveries = %+v", deliveries)
	}
	select {
	case <-received:
	default:
		t.Fatal("receiver did not receive a webhook")
	}
	stored, err := d.ForEvent(ctx, e.ID)
	if err != nil || len(stored) != 1 || stored[0].WebhookHost == "" || stored[0].WebhookID == "" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	encoded, _ := json.Marshal(stored[0])
	if strings.Contains(string(encoded), receiver.URL) {
		t.Fatalf("delivery leaks webhook URL: %s", encoded)
	}
}

func TestWebhookFailureAndLevelSkipAreRecorded(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(filepath.Join(t.TempDir(), "boop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, _, _ := projects.New(db).Create(ctx, projects.Input{Name: stringPtr("Alerts"), MinLevel: stringPtr("info")})
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer receiver.Close()
	store := webhooks.New(db)
	if _, err := store.Create(ctx, p.ID, webhooks.Input{URL: stringPtr(receiver.URL)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, p.ID, webhooks.Input{URL: stringPtr(receiver.URL + "/skip"), MinLevel: stringPtr("critical")}); err != nil {
		t.Fatal(err)
	}
	e, err := events.New(db).Create(ctx, p.ID, events.Input{Title: "Alert", Level: "error"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := New(db, devices.New(db), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.ConfigureWebhooks(store, nil)
	deliveries := d.deliver(ctx, e, p, true)
	if len(deliveries) != 2 || deliveries[0].Status != StatusFailed || deliveries[0].HTTPStatus != http.StatusInternalServerError || deliveries[1].Status != StatusSkipped {
		t.Fatalf("deliveries = %+v", deliveries)
	}
}

func stringPtr(s string) *string { return &s }
