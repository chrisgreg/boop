package webhooks

import (
	"encoding/json"
	"testing"

	"github.com/chrisgreg/boop/server/internal/events"
	"github.com/chrisgreg/boop/server/internal/projects"
)

func TestRenderJSONPayloadAndHeaderPrecedence(t *testing.T) {
	body, headers, err := Render(Webhook{PayloadMode: PayloadModeJSON, Headers: map[string]string{"Authorization": "Bearer token", "Content-Type": "application/vnd.test+json"}}, sampleEvent(), sampleProject())
	if err != nil {
		t.Fatal(err)
	}
	if headers["Authorization"] != "Bearer token" || headers["Content-Type"] != "application/vnd.test+json" {
		t.Fatalf("headers = %#v", headers)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("body is not JSON: %q: %v", body, err)
	}
	if payload["id"] != "evt_1" || payload["project"].(map[string]any)["name"] != "Alerts" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRenderCustomTemplateUsesJSONHelper(t *testing.T) {
	e := sampleEvent()
	e.Title = "quoted \"title\"\nnext line"
	body, _, err := Render(Webhook{PayloadMode: PayloadModeCustom, BodyTemplate: `{"text": {{json .Title}}}`}, e, sampleProject())
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("custom body is not JSON: %q: %v", body, err)
	}
	if payload["text"] != e.Title {
		t.Fatalf("text = %q, want %q", payload["text"], e.Title)
	}
}

func TestRenderRejectsInvalidTemplate(t *testing.T) {
	if _, _, err := Render(Webhook{PayloadMode: PayloadModeCustom, BodyTemplate: `{{.Missing}}`}, sampleEvent(), sampleProject()); err == nil {
		t.Fatal("Render should reject an unknown template field")
	}
}

func sampleEvent() events.Event {
	return events.Event{ID: "evt_1", Title: "Alert", Body: "Something happened", Level: "error", Source: "app", Fingerprint: "fp", CreatedAt: "2026-08-31T00:00:00Z", Actions: []events.Action{{Label: "Open", URL: "https://example.test"}}, Data: json.RawMessage(`{"key":"value"}`)}
}

func sampleProject() projects.Project { return projects.Project{ID: "prj_1", Name: "Alerts"} }
