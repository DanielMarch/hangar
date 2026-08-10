package main

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"

	v1 "github.com/hangar-project/hangar/internal/api/v1"
	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/store"
)

// registerMumbleAuthRoute mounts POST /api/v1/public/mumble/auth — SRS
// §6.7's one deliberately unauthenticated write route. It reads the raw
// request body (v1.HandleMumbleAuth's HMAC verification needs the exact
// bytes the signature was computed over, before any JSON decoding) and
// delegates everything else to v1.HandleMumbleAuth, the same logic
// mumble.go's gRPC authenticator path uses via v1.NewMumbleDecider — the
// two deployment modes can never disagree about who's allowed to connect.
//
// A no-op (route not mounted) when Mumble isn't enabled or no
// app.platform row of kind "mumble" exists yet — the same
// "warn and skip" precedent mumble.go's registerMumbleDriver already
// sets, not an error.
func registerMumbleAuthRoute(ctx context.Context, mux *http.ServeMux, s *store.Store, cfg *config.Config) error {
	if !cfg.Mumble.Enabled {
		return nil
	}
	platforms, err := s.ListPlatforms(ctx)
	if err != nil {
		return fmt.Errorf("mumble http: listing platforms: %w", err)
	}
	var platformID uuid.UUID
	found := false
	for _, p := range platforms {
		if p.Kind == "mumble" {
			platformID = p.PlatformID
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	secret := cfg.Mumble.AuthSharedSecret.Reveal()

	mux.HandleFunc("POST /api/v1/public/mumble/auth", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			http.Error(w, "reading body", http.StatusBadRequest)
			return
		}
		signature := r.Header.Get("X-Signature")
		sourceIP := r.RemoteAddr
		result, err := v1.HandleMumbleAuth(r.Context(), s, secret, platformID, body, signature, sourceIP)
		if err != nil {
			switch err {
			case v1.ErrMumbleAuthRateLimited:
				http.Error(w, "rate limited", http.StatusTooManyRequests)
			case v1.ErrMumbleAuthBadSignature:
				http.Error(w, "bad signature", http.StatusUnauthorized)
			default:
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"allowed":%t,"user_id":%d,"name":%q}`, result.Allowed, result.UserID, result.Name)
	})
	return nil
}
