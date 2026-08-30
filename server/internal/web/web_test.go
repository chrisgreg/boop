package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSPAFallback(t *testing.T) {
	h := Handler()
	for _, p := range []string{"/", "/events/evt_123", "/settings"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct == "" || ct[:9] != "text/html" {
			t.Errorf("%s: content-type %q", p, ct)
		}
	}
}

func TestPWAAssets(t *testing.T) {
	h := Handler()
	for _, p := range []string{"/manifest.webmanifest", "/sw.js", "/icons/icon-192.png", "/icons/icon-512.png"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", p, rec.Code)
		}
		if cache := rec.Header().Get("Cache-Control"); cache != "no-cache" {
			t.Errorf("%s: cache-control %q", p, cache)
		}
		if p == "/manifest.webmanifest" && rec.Header().Get("Content-Type") != "application/manifest+json" {
			t.Errorf("%s: content-type %q", p, rec.Header().Get("Content-Type"))
		}
	}
}
