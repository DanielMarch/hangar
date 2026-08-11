package main

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// TestSPAHandlerFallsBackToIndexForClientRoutes is the Phase 17 defect
// closure regression test for serve.go's spaHandler: Phase 16 registered a
// plain http.FileServerFS on "/", which 404s any client-side route on
// direct navigation (a hard refresh, a bookmark) rather than handing the
// request to the SPA's router. See spaHandler's doc comment for the full
// story.
func TestSPAHandlerFallsBackToIndexForClientRoutes(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":       {Data: []byte("<html>spa shell</html>")},
		"assets/app.js":    {Data: []byte("console.log('app')")},
		"assets/style.css": {Data: []byte("body{}")},
	}
	handler := spaHandler(dist)

	t.Run("root serves index.html", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Code != 200 || rec.Body.String() != "<html>spa shell</html>" {
			t.Fatalf("got %d %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("a real asset path is served as itself", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/app.js", nil))
		if rec.Code != 200 || rec.Body.String() != "console.log('app')" {
			t.Fatalf("got %d %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("a client-side route with no matching file falls back to index.html, not a 404", func(t *testing.T) {
		for _, p := range []string{"/login", "/characters", "/characters/123/wallet", "/corporations/456/ledgers", "/squads/abc-def/members"} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
			if rec.Code != 200 || rec.Body.String() != "<html>spa shell</html>" {
				t.Fatalf("path %s: got %d %q, want 200 index.html", p, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("a missing asset path (has a file extension) still 404s honestly", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/does-not-exist.js", nil))
		if rec.Code != 404 {
			t.Fatalf("got %d, want 404", rec.Code)
		}
	})

	t.Run("an unregistered path under a reserved API/auth/health prefix 404s, never falls back to the SPA shell", func(t *testing.T) {
		for _, p := range []string{"/api/v1/nonexistent-route", "/auth/nonexistent", "/healthz"} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
			if rec.Code != 404 {
				t.Fatalf("path %s: got %d, want 404 (must never silently serve the SPA shell for an unknown API/auth/health route)", p, rec.Code)
			}
		}
	})
}
