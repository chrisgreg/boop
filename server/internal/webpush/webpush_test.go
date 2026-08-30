package webpush

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	pushlib "github.com/marknefedov/go-webpush/v2"

	"github.com/chrisgreg/boop/server/internal/database"
)

const (
	testP256DH = "BNNL5ZaTfK81qhXOx23-wewhigUeFb632jN6LvRWCFH1ubQr77FE_9qV1FuojuRmHP42zmf34rXgW80OvUVDgTk"
	testAuth   = "zqbxT6JKstKSY9JKibZLSQ"
)

func TestVAPIDIdentitySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boop.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewStore(db).LoadOrCreateVAPIDKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	second, err := NewStore(db).LoadOrCreateVAPIDKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(second) {
		t.Fatal("VAPID identity changed after reopening the database")
	}
}

func TestSubscriptionLifecycle(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "boop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := NewStore(db)
	ctx := context.Background()

	if _, err := store.Upsert(ctx, Input{Endpoint: "http://push.example.test", Keys: Keys{P256DH: testP256DH, Auth: testAuth}}, "test"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid endpoint error = %v", err)
	}
	first, err := store.Upsert(ctx, Input{Endpoint: "https://push.example.test/subscription", Keys: Keys{P256DH: testP256DH, Auth: testAuth}, Name: "iPhone"}, "Safari")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Upsert(ctx, Input{Endpoint: first.Endpoint, Keys: Keys{P256DH: testP256DH, Auth: testAuth}, Name: "Home Screen"}, "Mobile Safari")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Name != "Home Screen" {
		t.Fatalf("upsert = %+v, first = %+v", second, first)
	}
	if count, err := store.Count(ctx); err != nil || count != 1 {
		t.Fatalf("count = %d, err = %v", count, err)
	}
	if err := store.DeleteEndpoint(ctx, first.Endpoint); err != nil {
		t.Fatal(err)
	}
	if count, err := store.Count(ctx); err != nil || count != 0 {
		t.Fatalf("count after delete = %d, err = %v", count, err)
	}
}

func TestClientSendAndExpiredClassification(t *testing.T) {
	status := http.StatusCreated
	var gotUrgency string
	pushService := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUrgency = r.Header.Get("Urgency")
		if r.Header.Get("Authorization") == "" || r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Error("missing Web Push request headers")
		}
		if body, _ := io.ReadAll(r.Body); len(body) == 0 {
			t.Error("encrypted body is empty")
		}
		w.Header().Set("Location", "/messages/1")
		w.WriteHeader(status)
	}))
	defer pushService.Close()

	db, err := database.Open(filepath.Join(t.TempDir(), "boop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewStore(db)
	keys, err := store.LoadOrCreateVAPIDKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		client:  pushlib.NewClient(pushlib.Config{HTTPClient: pushService.Client()}),
		keys:    keys,
		subject: pushService.URL,
	}
	sub := Subscription{Endpoint: pushService.URL, P256DH: testP256DH, Auth: testAuth}
	messageID, err := client.Send(context.Background(), sub, Notification{Title: "Boop", EventID: "evt_123", Level: "critical"})
	if err != nil {
		t.Fatal(err)
	}
	if messageID != pushService.URL+"/messages/1" || gotUrgency != "high" {
		t.Fatalf("message id = %q, urgency = %q", messageID, gotUrgency)
	}

	status = http.StatusGone
	if _, err := client.Send(context.Background(), sub, Notification{Title: "Boop", EventID: "evt_124"}); !errors.Is(err, ErrSubscriptionExpired) {
		t.Fatalf("expired error = %v", err)
	}
}

func TestNotificationPayloadIsBoundedAndValidUTF8(t *testing.T) {
	input := Notification{Title: "Boop", Body: strings.Repeat("quoted \"世界\" ", 500), EventID: "evt_123", URL: "/events/evt_123"}
	body, err := marshalNotification(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > maxPayload {
		t.Fatalf("payload is %d bytes, want <= %d", len(body), maxPayload)
	}
	var decoded Notification
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(decoded.Body, "…") {
		t.Fatalf("truncated body = %q", decoded.Body)
	}
}
