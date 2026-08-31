package database_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/chrisgreg/boop/server/internal/database"
	"github.com/chrisgreg/boop/server/migrations"
)

func TestWebhookMigrationPreservesExistingDeviceDeliveries(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "boop.db")+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_initial.sql", "0002_silences.sql", "0003_actions_groups.sql"} {
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, '2026-08-31T00:00:00Z')`, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO projects (id, name, slug, icon, api_key_hash, notify, min_level, created_at, updated_at) VALUES ('prj_1', 'Alerts', 'alerts', '', 'hash', 1, 'info', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO devices (id, name, credential_hash, created_at, updated_at) VALUES ('dev_1', 'Phone', 'credential', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events (id, project_id, level, title, occurred_at, created_at) VALUES ('evt_1', 'prj_1', 'info', 'Alert', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO deliveries (id, event_id, device_id, status, apns_id, attempted_at, created_at) VALUES ('dlv_1', 'evt_1', 'dev_1', 'sent', 'apns_1', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}

	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var targetType, deviceID, apnsID string
	if err := db.QueryRow(`SELECT target_type, device_id, apns_id FROM deliveries WHERE id = 'dlv_1'`).Scan(&targetType, &deviceID, &apnsID); err != nil {
		t.Fatal(err)
	}
	if targetType != "device" || deviceID != "dev_1" || apnsID != "apns_1" {
		t.Fatalf("migrated delivery = %q, %q, %q", targetType, deviceID, apnsID)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check reported a violation")
	}
}
