package api

import (
	"errors"
	"net/http"

	"github.com/chrisgreg/boop/server/internal/events"
	"github.com/chrisgreg/boop/server/internal/events/levels"
	"github.com/chrisgreg/boop/server/internal/ids"
	"github.com/chrisgreg/boop/server/internal/webhooks"
)

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.webhookProject(w, r) {
		return
	}
	list, err := s.Webhooks.List(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": list})
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.webhookProject(w, r) {
		return
	}
	var in webhooks.Input
	if !readJSON(w, r, &in) {
		return
	}
	target, err := s.Webhooks.Create(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Info("webhook.created", "webhook_id", target.ID, "project_id", target.ProjectID)
	writeJSON(w, http.StatusCreated, target)
}

func (s *Server) updateWebhook(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.webhookForProject(w, r); !ok {
		return
	}
	var in webhooks.Input
	if !readJSON(w, r, &in) {
		return
	}
	target, err := s.Webhooks.Update(r.Context(), r.PathValue("webhook_id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, target)
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.webhookForProject(w, r); !ok {
		return
	}
	if err := s.Webhooks.Delete(r.Context(), r.PathValue("webhook_id")); err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Info("webhook.deleted", "webhook_id", r.PathValue("webhook_id"), "project_id", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request) {
	target, ok := s.webhookForProject(w, r)
	if !ok {
		return
	}
	p, err := s.Projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	e := events.Event{ID: ids.New("test"), ProjectID: p.ID, Title: "Test webhook", Body: "If you received this, Boop webhook delivery is working.", Level: levels.Success, Source: "boop", CreatedAt: ids.Now()}
	delivery := s.Dispatcher.TestWebhook(r.Context(), target, e, p)
	writeJSON(w, http.StatusOK, map[string]any{"delivery": delivery})
}

func (s *Server) webhookProject(w http.ResponseWriter, r *http.Request) bool {
	_, err := s.Projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return false
	}
	return true
}

func (s *Server) webhookForProject(w http.ResponseWriter, r *http.Request) (webhooks.Webhook, bool) {
	if !s.webhookProject(w, r) {
		return webhooks.Webhook{}, false
	}
	target, err := s.Webhooks.GetForDelivery(r.Context(), r.PathValue("webhook_id"))
	if errors.Is(err, webhooks.ErrNotFound) || (err == nil && target.ProjectID != r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "not_found", "webhook not found")
		return webhooks.Webhook{}, false
	}
	if err != nil {
		s.fail(w, err)
		return webhooks.Webhook{}, false
	}
	return target, true
}
