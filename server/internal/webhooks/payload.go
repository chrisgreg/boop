package webhooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"text/template"

	"github.com/chrisgreg/boop/server/internal/events"
	"github.com/chrisgreg/boop/server/internal/projects"
)

// Payload is the stable native webhook representation of an event.
type Payload struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Level       string          `json:"level"`
	Source      string          `json:"source"`
	Fingerprint string          `json:"fingerprint"`
	CreatedAt   string          `json:"created_at"`
	Project     PayloadProject  `json:"project"`
	Actions     []events.Action `json:"actions"`
	Data        json.RawMessage `json:"data"`
}

type PayloadProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Render builds a webhook body and headers. Custom templates receive Payload
// fields as their dot value and may use json to safely quote a value.
func Render(w Webhook, e events.Event, p projects.Project) ([]byte, map[string]string, error) {
	payload := Payload{
		ID: e.ID, Title: e.Title, Body: e.Body, Level: e.Level, Source: e.Source, Fingerprint: e.Fingerprint, CreatedAt: e.CreatedAt,
		Project: PayloadProject{ID: p.ID, Name: p.Name}, Actions: e.Actions, Data: e.Data,
	}
	if len(payload.Data) == 0 {
		payload.Data = json.RawMessage(`{}`)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	for key, value := range w.Headers {
		headers[http.CanonicalHeaderKey(key)] = value
	}
	if w.PayloadMode == PayloadModeJSON {
		body, err := json.Marshal(payload)
		return body, headers, err
	}
	if w.PayloadMode != PayloadModeCustom {
		return nil, nil, fmt.Errorf("unsupported payload mode %q", w.PayloadMode)
	}
	tpl, err := template.New("webhook").Option("missingkey=error").Funcs(template.FuncMap{
		"json": func(value any) (string, error) {
			encoded, err := json.Marshal(value)
			return string(encoded), err
		},
	}).Parse(w.BodyTemplate)
	if err != nil {
		return nil, nil, err
	}
	var body bytes.Buffer
	if err := tpl.Execute(&body, payload); err != nil {
		return nil, nil, err
	}
	return body.Bytes(), headers, nil
}
