package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chrisgreg/boop/server/internal/apns"
	"github.com/chrisgreg/boop/server/internal/auth"
	"github.com/chrisgreg/boop/server/internal/config"
	"github.com/chrisgreg/boop/server/internal/database"
	"github.com/chrisgreg/boop/server/internal/delivery"
	"github.com/chrisgreg/boop/server/internal/devices"
	"github.com/chrisgreg/boop/server/internal/events"
	"github.com/chrisgreg/boop/server/internal/pairing"
	"github.com/chrisgreg/boop/server/internal/projects"
	"github.com/chrisgreg/boop/server/internal/settings"
	"github.com/chrisgreg/boop/server/internal/silences"
	"github.com/chrisgreg/boop/server/internal/webhooks"
)

// fakeSender records notifications instead of talking to APNs.
type fakeSender struct {
	sent []sentPush
	err  error
}

type sentPush struct {
	token string
	n     apns.Notification
}

func (f *fakeSender) Send(_ context.Context, token string, n apns.Notification) (string, error) {
	f.sent = append(f.sent, sentPush{token, n})
	if f.err != nil {
		return "", f.err
	}
	return "apns-id-" + fmt.Sprint(len(f.sent)), nil
}

type env struct {
	t      *testing.T
	srv    *httptest.Server
	server *Server
	sender *fakeSender
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "boop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dev := devices.New(db)
	sender := &fakeSender{}
	webhookStore := webhooks.New(db)
	dispatcher := delivery.New(db, dev, sender, log)
	dispatcher.ConfigureWebhooks(webhookStore, nil)
	s := &Server{
		Config:     config.Config{DatabasePath: "test.db", RetentionDays: 30, APNS: config.APNS{Environment: "sandbox"}},
		DB:         db,
		Log:        log,
		Settings:   settings.New(db),
		Projects:   projects.New(db),
		Devices:    dev,
		Pairing:    pairing.New(db, dev),
		Events:     events.New(db),
		Silences:   silences.New(db),
		Webhooks:   webhookStore,
		Dispatcher: dispatcher,
		Admin:      auth.NewAdmin("", ""),
		StartedAt:  time.Now(),
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, server: s, sender: sender}
}

type resp struct {
	status int
	body   map[string]any
	raw    []byte
}

func (e *env) do(method, path, bearer string, body any) resp {
	e.t.Helper()
	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rdr = strings.NewReader(b)
		default:
			buf, _ := json.Marshal(b)
			rdr = bytes.NewReader(buf)
		}
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, raw: raw}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}

func (e *env) createProject(name string) (id, key string) {
	e.t.Helper()
	r := e.do("POST", "/api/v1/projects", "", map[string]string{"name": name})
	if r.status != 201 {
		e.t.Fatalf("create project: %d %s", r.status, r.raw)
	}
	return r.body["id"].(string), r.body["api_key"].(string)
}

func (e *env) pairDevice(name string) (id, cred string) {
	e.t.Helper()
	r := e.do("POST", "/api/v1/pairing", "", nil)
	if r.status != 201 {
		e.t.Fatalf("create pairing: %d %s", r.status, r.raw)
	}
	tok := r.body["token"].(string)
	r = e.do("POST", "/api/v1/pairing/exchange", "", map[string]string{"token": tok, "name": name})
	if r.status != 201 {
		e.t.Fatalf("exchange: %d %s", r.status, r.raw)
	}
	return r.body["device"].(map[string]any)["id"].(string), r.body["credential"].(string)
}

func TestHealth(t *testing.T) {
	e := newEnv(t)
	r := e.do("GET", "/health", "", nil)
	if r.status != 200 || r.body["status"] != "ok" {
		t.Fatalf("health: %d %s", r.status, r.raw)
	}
}

func TestProjectLifecycle(t *testing.T) {
	e := newEnv(t)
	r := e.do("POST", "/api/v1/projects", "", map[string]string{"name": "Uini"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	id := r.body["id"].(string)
	key := r.body["api_key"].(string)
	if !strings.HasPrefix(key, "boop_proj_") || len(key) < 30 {
		t.Errorf("api key %q", key)
	}
	if r.body["slug"] != "uini" || r.body["notify"] != true || r.body["min_level"] != "info" {
		t.Errorf("project = %v", r.body)
	}
	if icon, _ := r.body["icon"].(string); !strings.Contains(icon, ":") {
		t.Errorf("a default shape icon should be assigned, got %q", icon)
	}

	// Duplicate names get distinct slugs.
	r = e.do("POST", "/api/v1/projects", "", map[string]string{"name": "Uini"})
	if r.status != 201 || r.body["slug"] != "uini-2" {
		t.Errorf("second project: %d %v", r.status, r.body)
	}

	// The raw key is never returned again.
	r = e.do("GET", "/api/v1/projects/"+id, "", nil)
	if r.status != 200 {
		t.Fatalf("get: %d", r.status)
	}
	if _, ok := r.body["api_key"]; ok {
		t.Error("api_key must not be returned on GET")
	}
	if strings.Contains(string(r.raw), "hash") {
		t.Error("hash leaked in response")
	}

	r = e.do("GET", "/api/v1/projects", "", nil)
	if r.status != 200 || len(r.body["projects"].([]any)) != 2 {
		t.Errorf("list: %d %s", r.status, r.raw)
	}

	r = e.do("PATCH", "/api/v1/projects/"+id, "", map[string]any{"name": "Uini Prod", "icon": "🚀", "notify": false, "min_level": "error"})
	if r.status != 200 || r.body["name"] != "Uini Prod" || r.body["notify"] != false || r.body["min_level"] != "error" {
		t.Errorf("patch: %d %s", r.status, r.raw)
	}
	r = e.do("PATCH", "/api/v1/projects/"+id, "", map[string]any{"min_level": "fatal"})
	if r.status != 422 {
		t.Errorf("bad level should be 422, got %d", r.status)
	}
	if r := e.do("PATCH", "/api/v1/projects/"+id, "", map[string]any{"icon": "triangle:mint"}); r.status != 200 || r.body["icon"] != "triangle:mint" {
		t.Errorf("shape icon: %d %s", r.status, r.raw)
	}
	if r := e.do("PATCH", "/api/v1/projects/"+id, "", map[string]any{"icon": "star:red"}); r.status != 422 {
		t.Errorf("unknown shape should be 422, got %d", r.status)
	}
	if r := e.do("GET", "/api/v1/projects/icons", "", nil); r.status != 200 || len(r.body["shapes"].([]any)) == 0 {
		t.Errorf("icons catalogue: %d %s", r.status, r.raw)
	}
	r = e.do("POST", "/api/v1/projects", "", map[string]string{"name": "   "})
	if r.status != 422 {
		t.Errorf("empty name should be 422, got %d", r.status)
	}
	r = e.do("POST", "/api/v1/projects", "", "{not json")
	if r.status != 400 {
		t.Errorf("bad json should be 400, got %d", r.status)
	}

	// Rotate: old key stops working, new key works.
	r = e.do("POST", "/api/v1/projects/"+id+"/rotate-key", "", nil)
	if r.status != 200 {
		t.Fatalf("rotate: %d %s", r.status, r.raw)
	}
	newKey := r.body["api_key"].(string)
	if e.do("POST", "/api/v1/events", key, map[string]string{"title": "x"}).status != 401 {
		t.Error("old key should be rejected after rotation")
	}
	if e.do("POST", "/api/v1/events", newKey, map[string]string{"title": "x"}).status != 201 {
		t.Error("new key should work")
	}

	// Delete cascades to events.
	r = e.do("DELETE", "/api/v1/projects/"+id, "", nil)
	if r.status != 204 {
		t.Errorf("delete: %d", r.status)
	}
	if e.do("GET", "/api/v1/projects/"+id, "", nil).status != 404 {
		t.Error("deleted project should 404")
	}
	r = e.do("GET", "/api/v1/events", "", nil)
	if n := len(r.body["events"].([]any)); n != 0 {
		t.Errorf("events should cascade-delete, got %d", n)
	}
}

func TestEventAuth(t *testing.T) {
	e := newEnv(t)
	_, key := e.createProject("Uini")
	body := map[string]string{"title": "Deploy complete"}

	if r := e.do("POST", "/api/v1/events", "", body); r.status != 401 {
		t.Errorf("no key: %d", r.status)
	}
	if r := e.do("POST", "/api/v1/events", "boop_proj_nope", body); r.status != 401 {
		t.Errorf("wrong key: %d", r.status)
	}
	if r := e.do("POST", "/api/v1/events", key, body); r.status != 201 {
		t.Errorf("good key: %d %s", r.status, r.raw)
	}
	// A device credential cannot post events.
	_, cred := e.pairDevice("phone")
	if r := e.do("POST", "/api/v1/events", cred, body); r.status != 401 {
		t.Errorf("device cred posting events: %d", r.status)
	}
	// Neither credential type may perform admin actions.
	if r := e.do("POST", "/api/v1/projects", key, map[string]string{"name": "x"}); r.status != 403 {
		t.Errorf("project key on admin endpoint: %d", r.status)
	}
	if r := e.do("GET", "/api/v1/projects", cred, nil); r.status != 403 {
		t.Errorf("device cred on admin endpoint: %d", r.status)
	}
}

func TestCreateEventValidationAndRedaction(t *testing.T) {
	e := newEnv(t)
	pid, key := e.createProject("Uini")

	// Minimum event.
	r := e.do("POST", "/api/v1/events", key, map[string]string{"title": "Deploy complete"})
	if r.status != 201 || !strings.HasPrefix(r.body["id"].(string), "evt_") || r.body["created_at"] == nil {
		t.Fatalf("minimum event: %d %s", r.status, r.raw)
	}
	id := r.body["id"].(string)
	r = e.do("GET", "/api/v1/events/"+id, "", nil)
	if r.status != 200 || r.body["level"] != "info" || r.body["project_id"] != pid || r.body["project_name"] != "Uini" {
		t.Errorf("get: %d %s", r.status, r.raw)
	}
	if r.body["data"] == nil {
		t.Errorf("data should default to {}: %s", r.raw)
	}

	// Rich event with sensitive data in nested places and unknown fields.
	rich := `{
	  "external_id": "4f9d", "source": "error_tracker", "type": "error", "level": "error",
	  "title": "KeyError", "body": "key :can_palette? not found", "fingerprint": "fp-1",
	  "occurred_at": "2026-08-28T12:51:44Z",
	  "data": {
	    "exception": {"type": "KeyError"},
	    "context": {"user_id": "123", "password": "hunter2", "session": {"access_token": "abc"}},
	    "tags": {"environment": "production"},
	    "totally_custom": {"nested": [1, 2, {"api_key": "leak"}]}
	  }
	}`
	r = e.do("POST", "/api/v1/events", key, rich)
	if r.status != 201 {
		t.Fatalf("rich: %d %s", r.status, r.raw)
	}
	r = e.do("GET", "/api/v1/events/"+r.body["id"].(string), "", nil)
	if r.body["external_id"] != "4f9d" || r.body["source"] != "error_tracker" || r.body["fingerprint"] != "fp-1" || r.body["occurred_at"] != "2026-08-28T12:51:44.000000000Z" {
		t.Errorf("rich fields: %s", r.raw)
	}
	data := r.body["data"].(map[string]any)
	ctxm := data["context"].(map[string]any)
	if ctxm["password"] != "[REDACTED]" || ctxm["session"].(map[string]any)["access_token"] != "[REDACTED]" || ctxm["user_id"] != "123" {
		t.Errorf("context redaction: %v", ctxm)
	}
	if data["totally_custom"].(map[string]any)["nested"].([]any)[2].(map[string]any)["api_key"] != "[REDACTED]" {
		t.Errorf("custom nested redaction: %v", data["totally_custom"])
	}
	if data["exception"].(map[string]any)["type"] != "KeyError" {
		t.Errorf("unknown fields must be preserved: %v", data)
	}

	// Configured extra redaction keys apply.
	if r := e.do("PATCH", "/api/v1/settings", "", map[string]any{"redact_keys": []string{"ssn"}}); r.status != 200 {
		t.Fatalf("settings: %d %s", r.status, r.raw)
	}
	r = e.do("POST", "/api/v1/events", key, `{"title":"x","data":{"ssn":"123","name":"ok"}}`)
	r = e.do("GET", "/api/v1/events/"+r.body["id"].(string), "", nil)
	if d := r.body["data"].(map[string]any); d["ssn"] != "[REDACTED]" || d["name"] != "ok" {
		t.Errorf("custom key redaction: %v", d)
	}

	// Validation failures.
	bad := []string{
		`{}`,
		`{"title":""}`,
		`{"title":"x","level":"fatal"}`,
		`{"title":"x","occurred_at":"yesterday"}`,
		`{"title":"x","data":[1,2]}`,
		`{"title":"` + strings.Repeat("a", 201) + `"}`,
	}
	for _, b := range bad {
		if r := e.do("POST", "/api/v1/events", key, b); r.status != 422 {
			t.Errorf("%s: want 422, got %d %s", b, r.status, r.raw)
		}
	}
	if r := e.do("POST", "/api/v1/events", key, `nope`); r.status != 400 {
		t.Errorf("malformed json: %d", r.status)
	}
	if r := e.do("GET", "/api/v1/events/evt_missing", "", nil); r.status != 404 {
		t.Errorf("missing event: %d", r.status)
	}
}

func TestListEventsFilteringAndCursor(t *testing.T) {
	e := newEnv(t)
	p1, k1 := e.createProject("Uini")
	_, k2 := e.createProject("Infra")
	for i := 0; i < 7; i++ {
		lvl := "info"
		if i%2 == 0 {
			lvl = "error"
		}
		if r := e.do("POST", "/api/v1/events", k1, map[string]string{"title": fmt.Sprintf("u%d", i), "level": lvl, "source": "s1"}); r.status != 201 {
			t.Fatal(r.status)
		}
	}
	for i := 0; i < 3; i++ {
		e.do("POST", "/api/v1/events", k2, map[string]string{"title": fmt.Sprintf("i%d", i), "source": "s2"})
	}

	// Default: newest first, all projects.
	r := e.do("GET", "/api/v1/events", "", nil)
	evs := r.body["events"].([]any)
	if len(evs) != 10 || evs[0].(map[string]any)["title"] != "i2" {
		t.Fatalf("default list: %d %s", len(evs), r.raw)
	}
	if r.body["next_cursor"] != nil {
		t.Errorf("no cursor expected when exhausted")
	}

	// Page through with limit=4.
	var seen []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		url := "/api/v1/events?limit=4"
		if cursor != "" {
			url += "&before=" + cursor
		}
		r = e.do("GET", url, "", nil)
		for _, ev := range r.body["events"].([]any) {
			seen = append(seen, ev.(map[string]any)["title"].(string))
		}
		c, _ := r.body["next_cursor"].(string)
		if c == "" {
			break
		}
		cursor = c
	}
	if len(seen) != 10 || strings.Join(seen, ",") != "i2,i1,i0,u6,u5,u4,u3,u2,u1,u0" {
		t.Errorf("paged order: %v", seen)
	}

	// Filters: by project id, by slug, level, source.
	r = e.do("GET", "/api/v1/events?project="+p1, "", nil)
	if len(r.body["events"].([]any)) != 7 {
		t.Errorf("project filter: %s", r.raw)
	}
	r = e.do("GET", "/api/v1/events?project=infra", "", nil)
	if len(r.body["events"].([]any)) != 3 {
		t.Errorf("slug filter: %s", r.raw)
	}
	r = e.do("GET", "/api/v1/events?level=error", "", nil)
	if len(r.body["events"].([]any)) != 4 {
		t.Errorf("level filter: %s", r.raw)
	}
	r = e.do("GET", "/api/v1/events?source=s2&level=info", "", nil)
	if len(r.body["events"].([]any)) != 3 {
		t.Errorf("source filter: %s", r.raw)
	}
	if r := e.do("GET", "/api/v1/events?level=fatal", "", nil); r.status != 422 {
		t.Errorf("bad level: %d", r.status)
	}
	if r := e.do("GET", "/api/v1/events?before=evt_nope", "", nil); r.status != 422 {
		t.Errorf("bad cursor: %d", r.status)
	}
	if r := e.do("GET", "/api/v1/events?limit=0", "", nil); r.status != 422 {
		t.Errorf("bad limit: %d", r.status)
	}

	// Device credentials can read; junk bearer cannot.
	_, cred := e.pairDevice("phone")
	if r := e.do("GET", "/api/v1/events", cred, nil); r.status != 200 {
		t.Errorf("device read: %d", r.status)
	}
	if r := e.do("GET", "/api/v1/events", "boop_dev_junk", nil); r.status != 401 {
		t.Errorf("junk bearer: %d", r.status)
	}
}

func TestPairingAndDevices(t *testing.T) {
	e := newEnv(t)
	r := e.do("POST", "/api/v1/pairing", "", nil)
	if r.status != 201 {
		t.Fatalf("pairing: %d %s", r.status, r.raw)
	}
	tok := r.body["token"].(string)
	qr := r.body["qr"].(map[string]any)
	if !strings.HasPrefix(tok, "pair_") || qr["version"] != float64(1) || qr["token"] != tok || !strings.HasPrefix(qr["server"].(string), "http://") {
		t.Errorf("pairing response: %s", r.raw)
	}
	pairID := r.body["id"].(string)
	if r := e.do("GET", "/api/v1/pairing", "", nil); len(r.body["pairing_tokens"].([]any)) != 1 {
		t.Errorf("pending list: %s", r.raw)
	}

	// Exchange: works once.
	r = e.do("POST", "/api/v1/pairing/exchange", "", map[string]string{"token": tok, "name": "Chris's iPhone", "platform": "ios"})
	if r.status != 201 {
		t.Fatalf("exchange: %d %s", r.status, r.raw)
	}
	cred := r.body["credential"].(string)
	dev := r.body["device"].(map[string]any)
	devID := dev["id"].(string)
	if !strings.HasPrefix(cred, "boop_dev_") || dev["name"] != "Chris's iPhone" || dev["push_registered"] != false {
		t.Errorf("exchange result: %s", r.raw)
	}
	if r := e.do("POST", "/api/v1/pairing/exchange", "", map[string]string{"token": tok}); r.status != 401 {
		t.Errorf("token reuse should fail: %d", r.status)
	}
	if r := e.do("POST", "/api/v1/pairing/exchange", "", map[string]string{"token": "pair_bogus"}); r.status != 401 {
		t.Errorf("bogus token: %d", r.status)
	}

	// Revoke a fresh token; it cannot be exchanged.
	r = e.do("POST", "/api/v1/pairing", "", nil)
	tok2, id2 := r.body["token"].(string), r.body["id"].(string)
	if r := e.do("DELETE", "/api/v1/pairing/"+id2, "", nil); r.status != 204 {
		t.Errorf("revoke: %d", r.status)
	}
	if r := e.do("POST", "/api/v1/pairing/exchange", "", map[string]string{"token": tok2}); r.status != 401 {
		t.Errorf("revoked token: %d", r.status)
	}
	if r := e.do("DELETE", "/api/v1/pairing/"+pairID, "", nil); r.status != 204 {
		t.Errorf("revoke used token should still succeed as a no-op-ish update: %d", r.status)
	}

	// Register APNs token.
	if r := e.do("POST", "/api/v1/devices", "", map[string]string{"device_token": "abc"}); r.status != 401 {
		t.Errorf("register without cred: %d", r.status)
	}
	r = e.do("POST", "/api/v1/devices", cred, map[string]string{"device_token": "tok-1", "app_bundle_id": "com.example.Boop"})
	if r.status != 200 || r.body["push_registered"] != true || r.body["app_bundle_id"] != "com.example.Boop" {
		t.Fatalf("register: %d %s", r.status, r.raw)
	}
	if strings.Contains(string(r.raw), "tok-1") {
		t.Error("device token must not be echoed back")
	}
	// Repeated registration of the same token: still one device.
	e.do("POST", "/api/v1/devices", cred, map[string]string{"device_token": "tok-1"})
	r = e.do("GET", "/api/v1/devices", "", nil)
	if n := len(r.body["devices"].([]any)); n != 1 {
		t.Errorf("devices after re-register: %d", n)
	}
	if ls := r.body["devices"].([]any)[0].(map[string]any)["last_seen_at"]; ls == nil {
		t.Error("last_seen_at should be set after authenticated call")
	}

	// A second pairing that registers the same APNs token replaces the stale device.
	_, cred2 := e.pairDevice("Same phone, re-paired")
	e.do("POST", "/api/v1/devices", cred2, map[string]string{"device_token": "tok-1"})
	r = e.do("GET", "/api/v1/devices", "", nil)
	if n := len(r.body["devices"].([]any)); n != 1 {
		t.Errorf("devices after re-pair with same token: %d %s", n, r.raw)
	}
	if e.do("GET", "/api/v1/events", cred, nil).status != 401 {
		t.Error("stale device credential should be gone")
	}

	// PATCH: a device may only edit itself; the web UI may edit any.
	newID := r.body["devices"].([]any)[0].(map[string]any)["id"].(string)
	_, cred3 := e.pairDevice("other")
	if r := e.do("PATCH", "/api/v1/devices/"+newID, cred3, map[string]string{"name": "hijack"}); r.status != 403 {
		t.Errorf("cross-device patch: %d", r.status)
	}
	if r := e.do("PATCH", "/api/v1/devices/"+newID, cred2, map[string]string{"name": "Renamed"}); r.status != 200 || r.body["name"] != "Renamed" {
		t.Errorf("self patch: %d %s", r.status, r.raw)
	}
	if r := e.do("PATCH", "/api/v1/devices/"+newID, "", map[string]string{"name": "Admin renamed"}); r.status != 200 {
		t.Errorf("admin patch: %d", r.status)
	}
	if r := e.do("DELETE", "/api/v1/devices/"+newID, "", nil); r.status != 204 {
		t.Errorf("delete: %d", r.status)
	}
	if r := e.do("DELETE", "/api/v1/devices/"+devID, "", nil); r.status != 404 {
		t.Errorf("delete already-replaced device: %d", r.status)
	}
}

func TestPairingTokenExpires(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	e.server.Pairing.SetClock(func() time.Time { return now })
	r := e.do("POST", "/api/v1/pairing", "", nil)
	tok := r.body["token"].(string)
	e.server.Pairing.SetClock(func() time.Time { return now.Add(pairing.TTL + time.Second) })
	if r := e.do("POST", "/api/v1/pairing/exchange", "", map[string]string{"token": tok}); r.status != 401 {
		t.Errorf("expired token should be rejected: %d %s", r.status, r.raw)
	}
	if r := e.do("GET", "/api/v1/pairing", "", nil); len(r.body["pairing_tokens"].([]any)) != 0 {
		t.Errorf("expired token should not be pending")
	}
}

func TestDeliveryFanOut(t *testing.T) {
	e := newEnv(t)
	pid, key := e.createProject("Uini")
	_, c1 := e.pairDevice("phone 1")
	_, c2 := e.pairDevice("phone 2")
	e.pairDevice("unregistered phone")
	e.do("POST", "/api/v1/devices", c1, map[string]string{"device_token": "t1"})
	e.do("POST", "/api/v1/devices", c2, map[string]string{"device_token": "t2"})

	// Test endpoint delivers synchronously.
	r := e.do("POST", "/api/v1/test", "", nil)
	if r.status != 201 {
		t.Fatalf("test: %d %s", r.status, r.raw)
	}
	dl := r.body["deliveries"].([]any)
	if len(dl) != 2 {
		t.Fatalf("deliveries = %d, want 2 (only devices with tokens)", len(dl))
	}
	if dl[0].(map[string]any)["status"] != "sent" || len(e.sender.sent) != 2 {
		t.Errorf("deliveries: %s", r.raw)
	}
	if e.sender.sent[0].n.Title != "Uini · Test boop" || e.sender.sent[0].n.EventID == "" || e.sender.sent[0].n.ProjectID != pid {
		t.Errorf("notification = %+v", e.sender.sent[0].n)
	}
	evID := r.body["event"].(map[string]any)["id"].(string)
	r = e.do("GET", "/api/v1/events/"+evID+"/deliveries", "", nil)
	if len(r.body["deliveries"].([]any)) != 2 {
		t.Errorf("recorded deliveries: %s", r.raw)
	}

	// Ingest path is async; wait for the worker.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.server.Dispatcher.Start(ctx)
	before := len(e.sender.sent)
	e.do("POST", "/api/v1/events", key, map[string]string{"title": "Critical thing", "level": "critical"})
	deadline := time.Now().Add(3 * time.Second)
	for len(e.sender.sent) < before+2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(e.sender.sent) != before+2 {
		t.Fatalf("async delivery: sent %d", len(e.sender.sent)-before)
	}
	if !e.sender.sent[len(e.sender.sent)-1].n.Prominent {
		t.Error("critical should be prominent")
	}

	// Notifications respect project preferences.
	e.do("PATCH", "/api/v1/projects/"+pid, "", map[string]any{"min_level": "error"})
	before = len(e.sender.sent)
	e.do("POST", "/api/v1/events", key, map[string]string{"title": "just info"})
	e.do("POST", "/api/v1/test", "", nil) // success < error
	time.Sleep(100 * time.Millisecond)
	if len(e.sender.sent) != before {
		t.Errorf("min_level ignored: %d new sends", len(e.sender.sent)-before)
	}

	// Status page reflects last push.
	r = e.do("GET", "/api/v1/status", "", nil)
	if r.status != 200 || r.body["devices"] != float64(3) || r.body["pushable_devices"] != float64(2) || r.body["last_push"] == nil {
		t.Errorf("status: %d %s", r.status, r.raw)
	}
	if r.body["apns"].(map[string]any)["configured"] != false {
		t.Errorf("apns should report unconfigured in tests: %s", r.raw)
	}
}

func TestProjectWebhookCRUDAndTestSend(t *testing.T) {
	e := newEnv(t)
	projectID, _ := e.createProject("Alerts")
	otherID, _ := e.createProject("Other")
	if r := e.do("POST", "/api/v1/projects/"+projectID+"/webhooks", "", map[string]any{"url": "/relative"}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid webhook: %d %s", r.status, r.raw)
	}
	called := make(chan struct{}, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer receiver.Close()

	r := e.do("POST", "/api/v1/projects/"+projectID+"/webhooks", "", map[string]any{
		"url": receiver.URL, "headers": map[string]string{"Authorization": "Bearer secret"},
	})
	if r.status != http.StatusCreated {
		t.Fatalf("create webhook: %d %s", r.status, r.raw)
	}
	webhookID := r.body["id"].(string)
	if r.body["headers"].(map[string]any)["Authorization"] != "********" {
		t.Fatalf("headers should be masked: %s", r.raw)
	}

	r = e.do("GET", "/api/v1/projects/"+projectID+"/webhooks", "", nil)
	if r.status != http.StatusOK || len(r.body["webhooks"].([]any)) != 1 {
		t.Fatalf("list webhooks: %d %s", r.status, r.raw)
	}
	if r = e.do("PATCH", "/api/v1/projects/"+otherID+"/webhooks/"+webhookID, "", map[string]any{"enabled": false}); r.status != http.StatusNotFound {
		t.Fatalf("cross-project webhook update: %d %s", r.status, r.raw)
	}

	var before int
	if err := e.server.DB.QueryRow(`SELECT COUNT(*) FROM deliveries`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	r = e.do("POST", "/api/v1/projects/"+projectID+"/webhooks/"+webhookID+"/test", "", nil)
	if r.status != http.StatusOK || r.body["delivery"].(map[string]any)["http_status"] != float64(http.StatusAccepted) {
		t.Fatalf("test webhook: %d %s", r.status, r.raw)
	}
	select {
	case <-called:
	default:
		t.Fatal("test webhook did not send")
	}
	var after int
	if err := e.server.DB.QueryRow(`SELECT COUNT(*) FROM deliveries`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("test webhook should not record a delivery: %d -> %d", before, after)
	}

	r = e.do("PATCH", "/api/v1/projects/"+projectID+"/webhooks/"+webhookID, "", map[string]any{"enabled": false})
	if r.status != http.StatusOK || r.body["enabled"] != false {
		t.Fatalf("update webhook: %d %s", r.status, r.raw)
	}
	if r = e.do("DELETE", "/api/v1/projects/"+projectID+"/webhooks/"+webhookID, "", nil); r.status != http.StatusNoContent {
		t.Fatalf("delete webhook: %d %s", r.status, r.raw)
	}
}

func TestUnregisteredTokenIsCleared(t *testing.T) {
	e := newEnv(t)
	e.createProject("Uini")
	_, c1 := e.pairDevice("phone")
	e.do("POST", "/api/v1/devices", c1, map[string]string{"device_token": "dead"})
	e.sender.err = &apns.Error{Status: 410, Reason: "Unregistered"}
	r := e.do("POST", "/api/v1/test", "", nil)
	dl := r.body["deliveries"].([]any)
	if len(dl) != 1 || dl[0].(map[string]any)["status"] != "failed" {
		t.Fatalf("deliveries: %s", r.raw)
	}
	r = e.do("GET", "/api/v1/devices", "", nil)
	if r.body["devices"].([]any)[0].(map[string]any)["push_registered"] != false {
		t.Errorf("token should be cleared: %s", r.raw)
	}
}

func TestTestNotificationWithoutAPNsOrProject(t *testing.T) {
	e := newEnv(t)
	if r := e.do("POST", "/api/v1/test", "", nil); r.status != 422 {
		t.Errorf("no project: %d %s", r.status, r.raw)
	}
	e.server.Dispatcher = delivery.New(e.server.DB, e.server.Devices, nil, e.server.Log)
	e.createProject("Uini")
	_, c := e.pairDevice("phone")
	e.do("POST", "/api/v1/devices", c, map[string]string{"device_token": "t"})
	r := e.do("POST", "/api/v1/test", "", nil)
	if r.status != 201 || r.body["apns_configured"] != false {
		t.Fatalf("test without apns: %d %s", r.status, r.raw)
	}
	if d := r.body["deliveries"].([]any)[0].(map[string]any); d["status"] != "skipped" {
		t.Errorf("delivery should be skipped: %v", d)
	}
}

func TestSettings(t *testing.T) {
	e := newEnv(t)
	r := e.do("GET", "/api/v1/settings", "", nil)
	if r.status != 200 || r.body["retention_days"] != float64(30) || r.body["setup_completed"] != false {
		t.Fatalf("defaults: %d %s", r.status, r.raw)
	}
	r = e.do("PATCH", "/api/v1/settings", "", map[string]any{"retention_days": 7, "setup_completed": true, "redact_keys": []string{" ssn ", ""}})
	if r.status != 200 || r.body["retention_days"] != float64(7) || r.body["setup_completed"] != true {
		t.Fatalf("patch: %d %s", r.status, r.raw)
	}
	if ks := r.body["redact_keys"].([]any); len(ks) != 1 || ks[0] != "ssn" {
		t.Errorf("redact keys: %v", ks)
	}
	if r := e.do("PATCH", "/api/v1/settings", "", map[string]any{"retention_days": -1}); r.status != 422 {
		t.Errorf("negative retention: %d", r.status)
	}
	r = e.do("GET", "/api/v1/status", "", nil)
	if r.body["retention_days"] != float64(7) || r.body["setup_completed"] != true {
		t.Errorf("status should reflect settings: %s", r.raw)
	}
}

func TestRetentionPrune(t *testing.T) {
	e := newEnv(t)
	_, key := e.createProject("Uini")
	e.do("POST", "/api/v1/events", key, map[string]string{"title": "old"})
	e.do("POST", "/api/v1/events", key, map[string]string{"title": "new"})
	// Backdate the first event's created_at.
	if _, err := e.server.DB.Exec(`UPDATE events SET created_at = '2020-01-01T00:00:00.000000000Z' WHERE title = 'old'`); err != nil {
		t.Fatal(err)
	}
	n, err := e.server.Events.Prune(context.Background(), 0, time.Now())
	if err != nil || n != 0 {
		t.Errorf("retention 0 must not prune: %d %v", n, err)
	}
	n, err = e.server.Events.Prune(context.Background(), 30, time.Now())
	if err != nil || n != 1 {
		t.Errorf("prune: %d %v", n, err)
	}
	r := e.do("GET", "/api/v1/events", "", nil)
	evs := r.body["events"].([]any)
	if len(evs) != 1 || evs[0].(map[string]any)["title"] != "new" {
		t.Errorf("after prune: %s", r.raw)
	}
}

func TestUnknownAPIRouteIsJSON404(t *testing.T) {
	e := newEnv(t)
	r := e.do("GET", "/api/v1/nope", "", nil)
	if r.status != 404 || r.body["error"] != "not_found" {
		t.Errorf("unknown route: %d %s", r.status, r.raw)
	}
}

func TestAdminAuth(t *testing.T) {
	e := newEnv(t)
	e.server.Admin = auth.NewAdmin("chris", "correct horse battery")

	// Unauthenticated: status says login required, admin + reader endpoints refuse.
	r := e.do("GET", "/api/v1/auth/me", "", nil)
	if r.body["auth_required"] != true || r.body["authenticated"] != false {
		t.Fatalf("me: %s", r.raw)
	}
	if r := e.do("GET", "/api/v1/projects", "", nil); r.status != 401 || r.body["error"] != "login_required" {
		t.Errorf("projects without login: %d %s", r.status, r.raw)
	}
	if r := e.do("GET", "/api/v1/events", "", nil); r.status != 401 {
		t.Errorf("events without login: %d", r.status)
	}
	if r := e.do("GET", "/health", "", nil); r.status != 200 {
		t.Errorf("health must stay open: %d", r.status)
	}

	// Wrong password.
	if r := e.do("POST", "/api/v1/auth/login", "", map[string]string{"username": "chris", "password": "nope"}); r.status != 401 {
		t.Errorf("bad login: %d", r.status)
	}

	// Login sets a cookie that authorises admin calls.
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"chris","password":"correct horse battery"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("login: %d", res.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly {
		t.Fatalf("session cookie missing or not HttpOnly: %v", res.Cookies())
	}
	withCookie := func(method, path string, body any) resp {
		var rdr io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, e.srv.URL+path, rdr)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(cookie)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		out := resp{status: res.StatusCode, raw: raw}
		_ = json.Unmarshal(raw, &out.body)
		return out
	}
	if r := withCookie("POST", "/api/v1/projects", map[string]string{"name": "Uini"}); r.status != 201 {
		t.Errorf("create project with session: %d %s", r.status, r.raw)
	}
	if r := withCookie("GET", "/api/v1/events", nil); r.status != 200 {
		t.Errorf("events with session: %d", r.status)
	}
	if r := withCookie("GET", "/api/v1/status", nil); r.body["admin_auth"] != true {
		t.Errorf("status should report admin_auth: %s", r.raw)
	}

	// HTTP Basic also works (for scripts).
	req, _ = http.NewRequest("GET", e.srv.URL+"/api/v1/projects", nil)
	req.SetBasicAuth("chris", "correct horse battery")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("basic auth: %d", res.StatusCode)
	}

	// Client credentials are still refused on admin endpoints and still work for their own purposes.
	pid := withCookie("GET", "/api/v1/projects", nil).body["projects"].([]any)[0].(map[string]any)["id"].(string)
	key := withCookie("POST", "/api/v1/projects/"+pid+"/rotate-key", nil).body["api_key"].(string)
	if r := e.do("POST", "/api/v1/events", key, map[string]string{"title": "x"}); r.status != 201 {
		t.Errorf("project key ingest under admin auth: %d %s", r.status, r.raw)
	}
	if r := e.do("GET", "/api/v1/projects", key, nil); r.status != 403 {
		t.Errorf("project key on admin endpoint: %d", r.status)
	}
	tok := withCookie("POST", "/api/v1/pairing", nil).body["token"].(string)
	r = e.do("POST", "/api/v1/pairing/exchange", "", map[string]string{"token": tok, "name": "phone"})
	if r.status != 201 {
		t.Fatalf("exchange must stay open: %d %s", r.status, r.raw)
	}
	cred := r.body["credential"].(string)
	if r := e.do("GET", "/api/v1/events", cred, nil); r.status != 200 {
		t.Errorf("device read under admin auth: %d", r.status)
	}

	// Logout revokes the session.
	if r := withCookie("POST", "/api/v1/auth/logout", nil); r.status != 204 {
		t.Errorf("logout: %d", r.status)
	}
	if r := withCookie("GET", "/api/v1/projects", nil); r.status != 401 {
		t.Errorf("session should be revoked: %d", r.status)
	}
}

func TestSilences(t *testing.T) {
	e := newEnv(t)
	pid, key := e.createProject("Uini")
	_, key2 := e.createProject("Infra")
	_, c := e.pairDevice("phone")
	e.do("POST", "/api/v1/devices", c, map[string]string{"device_token": "t1"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.server.Dispatcher.Start(ctx)

	// Validation.
	if r := e.do("POST", "/api/v1/silences", "", map[string]string{"field": "regex", "value": "x"}); r.status != 422 {
		t.Errorf("bad field: %d %s", r.status, r.raw)
	}
	if r := e.do("POST", "/api/v1/silences", "", map[string]string{"field": "title", "value": "  "}); r.status != 422 {
		t.Errorf("empty value: %d", r.status)
	}
	if r := e.do("POST", "/api/v1/silences", "", map[string]string{"field": "title", "value": "x", "project_id": "prj_nope"}); r.status != 422 {
		t.Errorf("unknown project: %d %s", r.status, r.raw)
	}

	// A project-scoped fingerprint rule and a global, case-insensitive title rule.
	r := e.do("POST", "/api/v1/silences", "", map[string]string{"field": "fingerprint", "value": "noisy-1", "project_id": pid, "note": "known flake"})
	if r.status != 201 || r.body["project_name"] != "Uini" || r.body["note"] != "known flake" {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	fpRule := r.body["id"].(string)
	r = e.do("POST", "/api/v1/silences", "", map[string]string{"field": "title", "value": "Disk Usage High"})
	if r.status != 201 {
		t.Fatalf("create title rule: %d %s", r.status, r.raw)
	}
	titleRule := r.body["id"].(string)
	if r := e.do("GET", "/api/v1/silences", "", nil); len(r.body["silences"].([]any)) != 2 || len(r.body["fields"].([]any)) != 3 {
		t.Errorf("list: %s", r.raw)
	}

	sent := func() int { return len(e.sender.sent) }
	wait := func(n int) {
		deadline := time.Now().Add(2 * time.Second)
		for sent() < n && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	post := func(k string, body map[string]string) map[string]any {
		r := e.do("POST", "/api/v1/events", k, body)
		if r.status != 201 {
			t.Fatalf("post: %d %s", r.status, r.raw)
		}
		return e.do("GET", "/api/v1/events/"+r.body["id"].(string), "", nil).body
	}

	// Silenced by fingerprint in its project: stored, flagged, not pushed.
	ev := post(key, map[string]string{"title": "Flaky test", "fingerprint": "noisy-1"})
	time.Sleep(100 * time.Millisecond)
	if ev["silenced"] != true || ev["silence_id"] != fpRule || sent() != 0 {
		t.Errorf("fingerprint silence: silenced=%v sent=%d", ev["silenced"], sent())
	}
	// Same fingerprint in another project is not covered by the scoped rule.
	ev = post(key2, map[string]string{"title": "Flaky test", "fingerprint": "noisy-1"})
	wait(1)
	if ev["silenced"] != false || sent() != 1 {
		t.Errorf("scoped rule leaked: silenced=%v sent=%d", ev["silenced"], sent())
	}
	// Global title rule, case-insensitive, any project.
	ev = post(key2, map[string]string{"title": "disk usage HIGH", "level": "warning"})
	time.Sleep(100 * time.Millisecond)
	if ev["silenced"] != true || ev["silence_id"] != titleRule || sent() != 1 {
		t.Errorf("title silence: %v sent=%d", ev, sent())
	}
	// Unrelated event still pushes.
	post(key, map[string]string{"title": "Deploy complete"})
	wait(2)
	if sent() != 2 {
		t.Errorf("unsilenced event should push: sent=%d", sent())
	}
	// The inbox exposes the flag.
	r = e.do("GET", "/api/v1/events?limit=10", "", nil)
	silencedCount := 0
	for _, x := range r.body["events"].([]any) {
		if x.(map[string]any)["silenced"] == true {
			silencedCount++
		}
	}
	if silencedCount != 2 {
		t.Errorf("silenced in list = %d", silencedCount)
	}
	// Test notifications honour silences too.
	r = e.do("POST", "/api/v1/silences", "", map[string]string{"field": "title", "value": "Test boop"})
	testRule := r.body["id"].(string)
	r = e.do("POST", "/api/v1/test", "", nil)
	if r.body["event"].(map[string]any)["silenced"] != true || len(r.body["deliveries"].([]any)) != 0 {
		t.Errorf("test event should be silenced: %s", r.raw)
	}
	// Delete: rule gone, later events push again; history keeps its flag.
	if r := e.do("DELETE", "/api/v1/silences/"+testRule, "", nil); r.status != 204 {
		t.Errorf("delete: %d", r.status)
	}
	if r := e.do("DELETE", "/api/v1/silences/"+testRule, "", nil); r.status != 404 {
		t.Errorf("double delete: %d", r.status)
	}
	e.do("DELETE", "/api/v1/silences/"+fpRule, "", nil)
	before := sent()
	post(key, map[string]string{"title": "Flaky test", "fingerprint": "noisy-1"})
	wait(before + 1)
	if sent() != before+1 {
		t.Errorf("after deleting the rule the event should push: %d", sent()-before)
	}
	if ev := e.do("GET", "/api/v1/events/"+ev["id"].(string), "", nil).body; ev["silenced"] != true {
		t.Errorf("history should keep the silenced flag")
	}
	// Device credentials cannot manage silences.
	if r := e.do("GET", "/api/v1/silences", c, nil); r.status != 403 {
		t.Errorf("device on silences: %d", r.status)
	}

	// Listing silenced events only, and unsilencing one pushes it now.
	r = e.do("GET", "/api/v1/events?silenced=true", "", nil)
	silencedOnly := r.body["events"].([]any)
	if len(silencedOnly) != 3 {
		t.Fatalf("silenced=true list: %d %s", len(silencedOnly), r.raw)
	}
	for _, x := range silencedOnly {
		if x.(map[string]any)["silenced"] != true {
			t.Errorf("non-silenced event in silenced list")
		}
	}
	if r := e.do("GET", "/api/v1/events?silenced=false", "", nil); len(r.body["events"].([]any)) != 3 {
		t.Errorf("silenced=false list: %s", r.raw)
	}
	if r := e.do("GET", "/api/v1/silences", "", nil); r.body["silenced_events"] != float64(3) {
		t.Errorf("silenced_events count: %s", r.raw)
	}
	target := silencedOnly[0].(map[string]any)["id"].(string)
	if r := e.do("GET", "/api/v1/silences/"+titleRule, "", nil); r.status != 200 || r.body["field"] != "title" {
		t.Errorf("get silence: %d %s", r.status, r.raw)
	}
	before = sent()
	r = e.do("POST", "/api/v1/events/"+target+"/unsilence", "", nil)
	if r.status != 200 || r.body["event"].(map[string]any)["silenced"] != false || len(r.body["deliveries"].([]any)) != 1 || sent() != before+1 {
		t.Errorf("unsilence: %d %s sent=%d", r.status, r.raw, sent()-before)
	}
	if r := e.do("POST", "/api/v1/events/"+target+"/unsilence", "", nil); r.status != 422 {
		t.Errorf("unsilence twice: %d", r.status)
	}
	if r := e.do("GET", "/api/v1/events?silenced=true", "", nil); len(r.body["events"].([]any)) != 2 {
		t.Errorf("after unsilence: %s", r.raw)
	}
}

func TestEventActions(t *testing.T) {
	e := newEnv(t)
	_, key := e.createProject("Shop")
	_, c1 := e.pairDevice("phone")
	e.do("POST", "/api/v1/devices", c1, map[string]string{"device_token": "t1"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.server.Dispatcher.Start(ctx)

	body := `{"title":"Payment received","body":"£19.99","level":"success","actions":[{"label":" Open in Stripe ","url":"https://dashboard.stripe.com/payments/pi_1"},{"label":"Open app","url":"myshop://orders/42"}]}`
	r := e.do("POST", "/api/v1/events", key, body)
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	id := r.body["id"].(string)
	r = e.do("GET", "/api/v1/events/"+id, "", nil)
	acts, _ := r.body["actions"].([]any)
	if len(acts) != 2 || acts[0].(map[string]any)["label"] != "Open in Stripe" || acts[1].(map[string]any)["url"] != "myshop://orders/42" {
		t.Fatalf("actions round trip: %s", r.raw)
	}
	// Events without actions omit the key entirely.
	r = e.do("POST", "/api/v1/events", key, `{"title":"plain"}`)
	r = e.do("GET", "/api/v1/events/"+r.body["id"].(string), "", nil)
	if _, ok := r.body["actions"]; ok {
		t.Errorf("plain event should omit actions: %s", r.raw)
	}
	// Grouped/ungrouped listings carry them too.
	r = e.do("GET", "/api/v1/events?level=success", "", nil)
	if ev := r.body["events"].([]any)[0].(map[string]any); len(ev["actions"].([]any)) != 2 {
		t.Errorf("list actions: %s", r.raw)
	}

	// The push carries the actions and asks for the actions category.
	deadline := time.Now().Add(3 * time.Second)
	for len(e.sender.sent) < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(e.sender.sent) < 1 {
		t.Fatal("no push sent")
	}
	n := e.sender.sent[0].n
	if len(n.Actions) != 2 || n.Actions[0].Label != "Open in Stripe" {
		t.Errorf("push actions = %+v", n.Actions)
	}
	payload, _ := apns.Payload(n)
	var p map[string]any
	_ = json.Unmarshal(payload, &p)
	aps := p["aps"].(map[string]any)
	if aps["category"] != apns.CategoryWithActions || aps["mutable-content"] != float64(1) || len(p["actions"].([]any)) != 2 {
		t.Errorf("payload: %s", payload)
	}
	plain, _ := apns.Payload(apns.Notification{Title: "t", Body: "b"})
	if strings.Contains(string(plain), "category") || strings.Contains(string(plain), "actions") {
		t.Errorf("payload without actions must not set a category: %s", plain)
	}

	// Validation.
	bad := []string{
		`{"title":"x","actions":[{"label":"","url":"https://a"}]}`,
		`{"title":"x","actions":[{"label":"a","url":""}]}`,
		`{"title":"x","actions":[{"label":"a","url":"/relative"}]}`,
		`{"title":"x","actions":[{"label":"a","url":"javascript:alert(1)"}]}`,
		`{"title":"x","actions":[{"label":"a","url":"DATA:text/html,hi"}]}`,
		`{"title":"x","actions":[{"label":"` + strings.Repeat("l", 41) + `","url":"https://a"}]}`,
		`{"title":"x","actions":[{"label":"1","url":"https://a"},{"label":"2","url":"https://a"},{"label":"3","url":"https://a"},{"label":"4","url":"https://a"}]}`,
	}
	for _, b := range bad {
		if r := e.do("POST", "/api/v1/events", key, b); r.status != 422 {
			t.Errorf("%s: want 422, got %d %s", b, r.status, r.raw)
		}
	}
}

func TestGroupedListing(t *testing.T) {
	e := newEnv(t)
	p1, k1 := e.createProject("Uini")
	_, k2 := e.createProject("Infra")
	post := func(key, title, level, fp string) string {
		t.Helper()
		r := e.do("POST", "/api/v1/events", key, map[string]string{"title": title, "level": level, "fingerprint": fp})
		if r.status != 201 {
			t.Fatalf("post %s: %d %s", title, r.status, r.raw)
		}
		return r.body["id"].(string)
	}
	var keyErrIDs []string
	for i := 0; i < 5; i++ {
		keyErrIDs = append(keyErrIDs, post(k1, "KeyError", "error", "keyerror"))
	}
	post(k1, "Deploy done", "success", "") // no fingerprint: never grouped
	post(k1, "Timeout", "warning", "timeout")
	post(k1, "Timeout", "info", "timeout")    // same fingerprint, different level, latest
	post(k2, "KeyError", "error", "keyerror") // same fingerprint, other project
	post(k1, "Deploy done", "success", "")

	// Ungrouped: everything.
	r := e.do("GET", "/api/v1/events", "", nil)
	if n := len(r.body["events"].([]any)); n != 10 {
		t.Fatalf("ungrouped: %d", n)
	}
	if _, ok := r.body["events"].([]any)[0].(map[string]any)["group"]; ok {
		t.Errorf("ungrouped rows must not carry group info")
	}

	// Grouped: 5 KeyErrors collapse, the two Timeouts collapse, plain events stay, other project separate.
	r = e.do("GET", "/api/v1/events?grouped=true", "", nil)
	evs := r.body["events"].([]any)
	var titles []string
	byFp := map[string]map[string]any{}
	for _, x := range evs {
		ev := x.(map[string]any)
		titles = append(titles, ev["title"].(string)+"@"+ev["project_name"].(string))
		if fp, _ := ev["fingerprint"].(string); fp != "" {
			byFp[ev["project_id"].(string)+"/"+fp] = ev
		}
	}
	if len(evs) != 5 || strings.Join(titles, ",") != "Deploy done@Uini,KeyError@Infra,Timeout@Uini,Deploy done@Uini,KeyError@Uini" {
		t.Fatalf("grouped rows: %v\n%s", titles, r.raw)
	}
	ke := byFp[p1+"/keyerror"]
	g := ke["group"].(map[string]any)
	if g["count"] != float64(5) || ke["id"] != keyErrIDs[4] {
		t.Errorf("keyerror group: %v (id %v, want %s)", g, ke["id"], keyErrIDs[4])
	}
	if g["first_seen"].(string) >= g["last_seen"].(string) {
		t.Errorf("first_seen %v should precede last_seen %v", g["first_seen"], g["last_seen"])
	}
	// The other project's identical fingerprint is its own group of one.
	for _, x := range evs {
		ev := x.(map[string]any)
		if ev["project_name"] == "Infra" && ev["group"].(map[string]any)["count"] != float64(1) {
			t.Errorf("cross-project grouping: %v", ev["group"])
		}
		if ev["fingerprint"] == "" {
			if _, ok := ev["group"]; ok {
				t.Errorf("unfingerprinted event carries group: %v", ev)
			}
		}
	}
	if byFp[p1+"/timeout"]["group"].(map[string]any)["count"] != float64(2) || byFp[p1+"/timeout"]["level"] != "info" {
		t.Errorf("timeout group should be latest (info) of 2: %v", byFp[p1+"/timeout"])
	}

	// Filters apply inside the group: level=warning shows the warning Timeout with count 1.
	r = e.do("GET", "/api/v1/events?grouped=true&level=warning", "", nil)
	evs = r.body["events"].([]any)
	if len(evs) != 1 || evs[0].(map[string]any)["level"] != "warning" || evs[0].(map[string]any)["group"].(map[string]any)["count"] != float64(1) {
		t.Errorf("filtered group: %s", r.raw)
	}

	// Cursor pagination in grouped mode never repeats a group and reaches everything.
	var seen []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		url := "/api/v1/events?grouped=true&limit=2"
		if cursor != "" {
			url += "&before=" + cursor
		}
		r = e.do("GET", url, "", nil)
		for _, ev := range r.body["events"].([]any) {
			m := ev.(map[string]any)
			seen = append(seen, m["title"].(string)+"@"+m["project_name"].(string))
		}
		c, _ := r.body["next_cursor"].(string)
		if c == "" {
			break
		}
		cursor = c
	}
	if strings.Join(seen, ",") != strings.Join(titles, ",") {
		t.Errorf("paged grouped: %v want %v", seen, titles)
	}

	// Occurrences: filter by fingerprint (and project) in ungrouped mode.
	r = e.do("GET", "/api/v1/events?project="+p1+"&fingerprint=keyerror", "", nil)
	if n := len(r.body["events"].([]any)); n != 5 {
		t.Errorf("occurrences: %d %s", n, r.raw)
	}
	r = e.do("GET", "/api/v1/events?fingerprint=keyerror", "", nil)
	if n := len(r.body["events"].([]any)); n != 6 {
		t.Errorf("occurrences across projects: %d", n)
	}

	// since/until window on created_at.
	mid := byFp[p1+"/keyerror"]["created_at"].(string)
	r = e.do("GET", "/api/v1/events?since="+mid, "", nil)
	if n := len(r.body["events"].([]any)); n != 6 { // the latest KeyError + 5 after it
		t.Errorf("since: %d %s", n, r.raw)
	}
	r = e.do("GET", "/api/v1/events?until="+mid, "", nil)
	if n := len(r.body["events"].([]any)); n != 4 {
		t.Errorf("until: %d", n)
	}
	if r := e.do("GET", "/api/v1/events?since=yesterday", "", nil); r.status != 422 {
		t.Errorf("bad since: %d", r.status)
	}
}

// mcpCall performs a raw JSON-RPC tools/call against /mcp with the given bearer.
func (e *env) mcpCall(bearer, tool string, args map[string]any) resp {
	e.t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, raw: raw}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func TestMCPEndpointAuth(t *testing.T) {
	e := newEnv(t)
	_, projKey := e.createProject("Uini")
	_, devCred := e.pairDevice("phone")
	e.do("POST", "/api/v1/events", projKey, map[string]string{"title": "hello"})
	// Lock the server down: admin login required, MCP token configured.
	e.server.Config.MCPToken = "boop_mcp_test_token_1234"
	e.server.Admin = auth.NewAdmin("admin", "correct horse battery")

	cases := []struct {
		name   string
		bearer string
		want   int
	}{
		{"no credential", "", 401},
		{"junk", "nope", 401},
		{"wrong mcp token", "boop_mcp_test_token_9999", 401},
		{"project key is refused", projKey, 401},
		{"mcp token", "boop_mcp_test_token_1234", 200},
		{"device credential", devCred, 200},
	}
	for _, c := range cases {
		r := e.mcpCall(c.bearer, "list_projects", nil)
		if r.status != c.want {
			t.Errorf("%s: %d %s", c.name, r.status, r.raw)
		}
	}
	// A successful call really returns data.
	r := e.mcpCall("boop_mcp_test_token_1234", "list_events", map[string]any{"limit": 5})
	if !strings.Contains(string(r.raw), `"hello"`) || strings.Contains(string(r.raw), "isError") {
		t.Errorf("mcp result: %s", r.raw)
	}
	// Admin HTTP Basic works on the endpoint when auth is enabled.
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.SetBasicAuth("admin", "correct horse battery")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("basic auth: %d", res.StatusCode)
	}

	// With admin auth off and no token, /mcp is open like the rest of the read API.
	e.server.Admin = auth.NewAdmin("", "")
	e.server.Config.MCPToken = ""
	if r := e.mcpCall("", "list_projects", nil); r.status != 200 {
		t.Errorf("open mode: %d %s", r.status, r.raw)
	}
	if r := e.mcpCall(projKey, "list_projects", nil); r.status != 401 {
		t.Errorf("project key must still be refused in open mode: %d", r.status)
	}

	// The Settings switch turns the endpoint off entirely (even with a valid credential).
	r = e.do("GET", "/api/v1/settings", "", nil)
	if r.body["mcp_enabled"] != true || r.body["mcp_token_set"] != false {
		t.Errorf("settings default: %s", r.raw)
	}
	if r := e.do("PATCH", "/api/v1/settings", "", map[string]any{"mcp_enabled": false}); r.status != 200 || r.body["mcp_enabled"] != false {
		t.Fatalf("disable: %d %s", r.status, r.raw)
	}
	if r := e.mcpCall(devCred, "list_projects", nil); r.status != 404 || r.body["error"] != "mcp_disabled" {
		t.Errorf("disabled: %d %s", r.status, r.raw)
	}
	e.do("PATCH", "/api/v1/settings", "", map[string]any{"mcp_enabled": true})
	if r := e.mcpCall(devCred, "list_projects", nil); r.status != 200 {
		t.Errorf("re-enabled: %d", r.status)
	}
	e.server.Config.MCPToken = "boop_mcp_test_token_1234"
	if r := e.do("GET", "/api/v1/settings", "", nil); r.body["mcp_token_set"] != true || strings.Contains(string(r.raw), "boop_mcp_test_token_1234") {
		t.Errorf("token flag must be reported without the token: %s", r.raw)
	}
}
