package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/chrisgreg/boop/server/internal/database"
	"github.com/chrisgreg/boop/server/internal/projects"
)

func TestStoreCreateMasksHeadersAndListsEnabled(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(filepath.Join(t.TempDir(), "boop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ps := projects.New(db)
	p, _, err := ps.Create(ctx, projects.Input{Name: ptr("Alerts")})
	if err != nil {
		t.Fatal(err)
	}
	store := New(db)
	w, err := store.Create(ctx, p.ID, Input{
		URL:          ptr("https://hooks.example.test/abc"),
		PayloadMode:  ptr("custom"),
		BodyTemplate: ptr(`{"text": {{json .Title}}}`),
		Headers:      raw(`{"Authorization":"Bearer secret"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.ID == "" || w.PayloadMode != PayloadModeCustom || !w.Enabled {
		t.Fatalf("created webhook = %+v", w)
	}
	if w.Headers["Authorization"] != MaskedHeaderValue {
		t.Fatalf("headers should be masked: %#v", w.Headers)
	}

	full, err := store.GetForDelivery(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if full.Headers["Authorization"] != "Bearer secret" {
		t.Fatalf("delivery headers = %#v", full.Headers)
	}
	list, err := store.ListEnabled(ctx, p.ID)
	if err != nil || len(list) != 1 || list[0].Headers["Authorization"] != "Bearer secret" {
		t.Fatalf("enabled list = %#v, %v", list, err)
	}

	disabled := false
	if _, err := store.Update(ctx, w.ID, Input{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	list, err = store.ListEnabled(ctx, p.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("disabled list = %#v, %v", list, err)
	}
}

func TestStoreRejectsInvalidWebhookInput(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(filepath.Join(t.TempDir(), "boop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, _, err := projects.New(db).Create(ctx, projects.Input{Name: ptr("Alerts")})
	if err != nil {
		t.Fatal(err)
	}
	store := New(db)
	for _, in := range []Input{
		{URL: ptr("/relative")},
		{URL: ptr("https://hooks.example.test"), PayloadMode: ptr("xml")},
		{URL: ptr("https://hooks.example.test"), MinLevel: ptr("loud")},
		{URL: ptr("https://hooks.example.test"), Headers: raw(`[]`)},
	} {
		if _, err := store.Create(ctx, p.ID, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%+v) error = %v, want ErrInvalid", in, err)
		}
	}
}

func ptr(s string) *string { return &s }

func raw(s string) *json.RawMessage {
	v := json.RawMessage(s)
	return &v
}
