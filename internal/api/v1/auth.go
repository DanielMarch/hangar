// auth.go implements SRS §6.1: authentication, users and third-party
// tokens. `GET /auth/login`, `GET /auth/callback` and `POST /auth/logout`
// are deliberately NOT under /api/v1 (the SRS lists them outside that
// prefix) and are not JSON operations — an OAuth redirect flow doesn't fit
// Huma's request/response contract, so they are plain net/http handlers
// registered directly on the mux by RegisterAuthRedirects, which
// cmd/hangar/serve.go calls alongside RegisterAll. Everything else in this
// file (`/api/v1/me...`, `/api/v1/api-tokens...`) is a normal Huma
// operation.
package v1

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/api"
	apimw "github.com/hangar-project/hangar/internal/api/middleware"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

const authTag = "auth"

func registerAuth(hapi huma.API, deps api.Deps) {
	get[EmptyIn, MeOut](hapi, deps, "", "/api/v1/me", "get-me", "The caller's own user record", authTag, meHandler(deps))
	get[EmptyIn, CollectionOut](hapi, deps, "", "/api/v1/me/characters", "list-my-characters", "Characters linked to the caller's account", authTag, myCharactersHandler(deps))
	mutate[SubIDEmptyIn, ReauthorizeOut](hapi, deps, http.MethodPost, "", "/api/v1/me/characters/{id}/reauthorize", "reauthorize-character", "Re-mint the ESI OAuth redirect for one already-linked character", authTag, reauthorizeHandler(deps))
	mutate[IDIn, EmptyOut](hapi, deps, http.MethodDelete, "", "/api/v1/me/characters/{id}", "unlink-character", "Unlink a character from the caller's account", authTag, unlinkCharacterHandler(deps))

	get[EmptyIn, CollectionOut](hapi, deps, "", "/api/v1/me/share-links", "list-share-links", "The caller's active share links", authTag, listShareLinksHandler(deps))
	mutate[CreateShareLinkIn, ItemOut](hapi, deps, http.MethodPost, "", "/api/v1/me/share-links", "create-share-link", "Create a share link", authTag, createShareLinkHandler(deps))
	mutate[UUIDIn, EmptyOut](hapi, deps, http.MethodDelete, "", "/api/v1/me/share-links/{id}", "revoke-share-link", "Revoke a share link", authTag, revokeShareLinkHandler(deps))

	get[EmptyIn, CollectionOut](hapi, deps, "api_tokens.manage", "/api/v1/api-tokens", "list-api-tokens", "The caller's third-party API tokens", authTag, listAPITokensHandler(deps))
	mutate[CreateAPITokenIn, CreateAPITokenOut](hapi, deps, http.MethodPost, "api_tokens.manage", "/api/v1/api-tokens", "create-api-token", "Mint a new third-party API token", authTag, createAPITokenHandler(deps))
	mutate[UUIDIn, EmptyOut](hapi, deps, http.MethodDelete, "api_tokens.manage", "/api/v1/api-tokens/{id}", "revoke-api-token", "Revoke a third-party API token", authTag, revokeAPITokenHandler(deps))
	get[UUIDPageIn, CollectionOut](hapi, deps, "api_tokens.view_access_log", "/api/v1/api-tokens/access-log", "api-token-access-log", "Access log for one API token", authTag, apiTokenAccessLogHandler(deps))
}

// ---- shapes ----

type EmptyIn struct{}
type EmptyOut struct{}

type SubIDEmptyIn struct {
	ID int64 `path:"id"`
}

type UUIDPageIn struct {
	TokenID string `query:"token_id" format:"uuid" required:"true"`
	After   string `query:"after"`
	Before  string `query:"before"`
	Limit   int32  `query:"limit" default:"50"`
}

type MeOut struct {
	Body struct {
		Data map[string]any `json:"data"`
	}
}

type ReauthorizeOut struct {
	// SetCookie carries the pre-auth session cookie the SSO callback needs
	// to find this pending login again (Phase 15.1 — see
	// reauthorizeHandler).
	SetCookie string `header:"Set-Cookie"`
	Body      struct {
		RedirectURL string `json:"redirect_url"`
	}
}

type CreateShareLinkIn struct {
	Body struct {
		View   string         `json:"view"`
		Params map[string]any `json:"params"`
	}
}

type CreateAPITokenIn struct {
	Body struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
}

type CreateAPITokenOut struct {
	Body struct {
		TokenID string `json:"token_id"`
		Secret  string `json:"secret" doc:"Shown once — HANGAR stores only its hash."`
	}
}

// ---- handlers ----

func meHandler(deps api.Deps) func(context.Context, *EmptyIn) (*MeOut, error) {
	return func(ctx context.Context, _ *EmptyIn) (*MeOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		user, err := deps.Store.GetUser(ctx, userID)
		if err != nil {
			return nil, api.NotFound("user")
		}
		out := &MeOut{}
		out.Body.Data = rowOf(user)
		return out, nil
	}
}

func myCharactersHandler(deps api.Deps) func(context.Context, *EmptyIn) (*CollectionOut, error) {
	return func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		rows, err := deps.Store.ListCharactersForUser(ctx, uuid.NullUUID{UUID: userID, Valid: true})
		if err != nil {
			return nil, api.Internal("listing characters", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

// reauthorizeHandler is POST /api/v1/me/characters/{id}/reauthorize.
//
// PHASE 15.1. Two things were wrong here. It answered 501 when deps.SSO
// was nil (cmd/hangar/serve.go now always builds a real Flow, and fails
// startup rather than serving a half-configured API, so the nil case is
// now a programming error — a 500, not a documented "not implemented"),
// and more importantly it discarded the pending session entirely: it
// returned a redirect URL whose `state` and PKCE verifier live on a
// session row the browser was never given a cookie for, so the subsequent
// /auth/callback could not find the session and every reauthorization
// failed. SetCookie on the output struct fixes that — the same cookie
// /auth/login issues, for the same reason.
func reauthorizeHandler(deps api.Deps) func(context.Context, *SubIDEmptyIn) (*ReauthorizeOut, error) {
	return func(ctx context.Context, in *SubIDEmptyIn) (*ReauthorizeOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		if deps.SSO == nil {
			return nil, api.Internal("reauthorize", errNoSSOFlow)
		}
		// Only a character already linked to the caller may be
		// reauthorized — otherwise this is an open redirect that mints a
		// login session for an arbitrary character id.
		ch, err := deps.Store.GetCharacter(ctx, in.ID)
		if err != nil {
			return nil, api.NotFound("character")
		}
		if !ch.UserID.Valid || ch.UserID.UUID != userID {
			return nil, api.Forbidden("character is not linked to the caller's account")
		}

		pending, err := deps.SSO.BeginLogin(ctx, []string{}, nil, nil)
		if err != nil {
			return nil, api.Internal("beginning reauthorization", err)
		}
		out := &ReauthorizeOut{}
		out.Body.RedirectURL = pending.RedirectURL
		out.SetCookie = (&http.Cookie{
			Name: apimw.SessionCookieName, Value: pending.SessionID.String(),
			Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: pending.ExpiresAt,
		}).String()
		return out, nil
	}
}

var errNoSSOFlow = errors.New("v1: no SSO flow configured on this API instance")

func unlinkCharacterHandler(deps api.Deps) func(context.Context, *IDIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *IDIn) (*EmptyOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		ch, err := deps.Store.GetCharacter(ctx, in.ID)
		if err != nil {
			return nil, api.NotFound("character")
		}
		if !ch.UserID.Valid || ch.UserID.UUID != userID {
			return nil, api.Forbidden("character is not linked to the caller's account")
		}
		if err := deps.Store.SoftDeleteCharacter(ctx, in.ID); err != nil {
			return nil, api.Internal("unlinking character", err)
		}
		return &EmptyOut{}, nil
	}
}

func listShareLinksHandler(deps api.Deps) func(context.Context, *EmptyIn) (*CollectionOut, error) {
	return func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		rows, err := deps.Store.ListShareLinksForUser(ctx, userID)
		if err != nil {
			return nil, api.Internal("listing share links", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func createShareLinkHandler(deps api.Deps) func(context.Context, *CreateShareLinkIn) (*ItemOut, error) {
	return func(ctx context.Context, in *CreateShareLinkIn) (*ItemOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		params, _ := marshalJSON(in.Body.Params)
		link, err := deps.Store.CreateShareLink(ctx, gen.CreateShareLinkParams{
			UserID: userID, View: in.Body.View, Params: params,
		})
		if err != nil {
			return nil, api.Internal("creating share link", err)
		}
		data := rowOf(link)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

func revokeShareLinkHandler(deps api.Deps) func(context.Context, *UUIDIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*EmptyOut, error) {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		if err := deps.Store.RevokeShareLink(ctx, id); err != nil {
			return nil, api.Internal("revoking share link", err)
		}
		return &EmptyOut{}, nil
	}
}

func listAPITokensHandler(deps api.Deps) func(context.Context, *EmptyIn) (*CollectionOut, error) {
	return func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		rows, err := deps.Store.ListApiTokensForUser(ctx, userID)
		if err != nil {
			return nil, api.Internal("listing api tokens", err)
		}
		// HashedSecret must never leave HANGAR — strip it before it ever
		// reaches dto.Row.
		for i := range rows {
			rows[i].HashedSecret = nil
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func createAPITokenHandler(deps api.Deps) func(context.Context, *CreateAPITokenIn) (*CreateAPITokenOut, error) {
	return func(ctx context.Context, in *CreateAPITokenIn) (*CreateAPITokenOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, api.Internal("generating token secret", err)
		}
		secretHex := hex.EncodeToString(secret)
		sum := sha256.Sum256(secret)
		token, err := deps.Store.CreateApiToken(ctx, gen.CreateApiTokenParams{
			UserID: userID, Name: in.Body.Name, HashedSecret: sum[:], Permissions: in.Body.Permissions,
		})
		if err != nil {
			return nil, api.Internal("creating api token", err)
		}
		out := &CreateAPITokenOut{}
		out.Body.TokenID = token.TokenID.String()
		out.Body.Secret = secretHex
		return out, nil
	}
}

func revokeAPITokenHandler(deps api.Deps) func(context.Context, *UUIDIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*EmptyOut, error) {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		if err := deps.Store.RevokeApiToken(ctx, id); err != nil {
			return nil, api.Internal("revoking api token", err)
		}
		return &EmptyOut{}, nil
	}
}

func apiTokenAccessLogHandler(deps api.Deps) func(context.Context, *UUIDPageIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *UUIDPageIn) (*CollectionOut, error) {
		page, err := api.ParsePageRequest(in.After, in.Before, &in.Limit)
		if err != nil {
			return nil, api.PageError(err)
		}
		tokenID, err := uuid.Parse(in.TokenID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed token_id")
		}
		rows, err := deps.Store.ListApiTokenAccessLog(ctx, tokenID, page.Limit)
		if err != nil {
			return nil, api.Internal("listing api token access log", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{
			Data: data,
			Page: api.PageInfo{NextCursor: api.ZeroSentinel, PrevCursor: api.ZeroSentinel, Limit: page.Limit},
			Sync: api.Sync{},
		}}, nil
	}
}

// ---- redirect flow (outside /api/v1, outside huma) ----

// RegisterAuthRedirects mounts /auth/login, /auth/callback and
// /auth/logout directly on mux. flow may be nil in an environment with no
// SSO configured (e.g. `hangar openapi`'s spec-only build, or a
// development server without EVE SSO credentials) — the handlers then
// answer 501 rather than panicking.
func RegisterAuthRedirects(mux *http.ServeMux, s *store.Store, flow *sso.Flow) {
	mux.HandleFunc("GET /auth/login", func(w http.ResponseWriter, r *http.Request) {
		if flow == nil {
			http.Error(w, "sso not configured", http.StatusNotImplemented)
			return
		}
		ip := r.RemoteAddr
		ua := r.UserAgent()
		pending, err := flow.BeginLogin(r.Context(), []string{}, &ip, &ua)
		if err != nil {
			http.Error(w, "failed to begin login", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: apimw.SessionCookieName, Value: pending.SessionID.String(),
			Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: pending.ExpiresAt,
		})
		http.Redirect(w, r, pending.RedirectURL, http.StatusFound)
	})

	mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if flow == nil {
			http.Error(w, "sso not configured", http.StatusNotImplemented)
			return
		}
		cookie, err := r.Cookie(apimw.SessionCookieName)
		if err != nil {
			http.Error(w, "missing session cookie", http.StatusBadRequest)
			return
		}
		sessionID, err := uuid.Parse(cookie.Value)
		if err != nil {
			http.Error(w, "malformed session cookie", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		result, err := flow.HandleCallback(r.Context(), sessionID, code, state)
		if err != nil {
			// PHASE 15.1 — back-button replay. `state` is single-use:
			// CompleteSessionLogin nulls pkce_verifier/state, so replaying
			// the callback URL (browser back button, or a refresh of the
			// post-login redirect) fails HandleCallback's "session has no
			// pending login" check even though the user is, right now,
			// perfectly well logged in. Erroring them out of a working
			// session is the wrong answer; if the cookie still resolves to
			// a completed session, treat the replay as a no-op and send
			// them where a successful login would have. Only a genuinely
			// unauthenticated caller sees 401.
			if session, gerr := s.GetSession(r.Context(), sessionID); gerr == nil && session.UserID.Valid {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			http.Error(w, "login failed", http.StatusUnauthorized)
			return
		}
		// Re-issue the cookie with the AUTHENTICATED session's expiry. The
		// cookie written by /auth/login carries the 10-minute pre-auth
		// sso.StateTTL; leaving it in place would have the browser drop a
		// still-valid 30-day session ten minutes after login.
		http.SetCookie(w, &http.Cookie{
			Name: apimw.SessionCookieName, Value: sessionID.String(),
			Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: result.SessionExpiresAt,
		})
		http.Redirect(w, r, "/", http.StatusFound)
	})

	mux.HandleFunc("POST /auth/logout", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(apimw.SessionCookieName)
		if err == nil {
			if sessionID, err := uuid.Parse(cookie.Value); err == nil && s != nil {
				_ = s.DeleteSession(r.Context(), sessionID)
			}
		}
		http.SetCookie(w, &http.Cookie{Name: apimw.SessionCookieName, Value: "", Path: "/", MaxAge: -1})
		w.WriteHeader(http.StatusNoContent)
	})
}

// small local helpers kept private to this file's callers via router.go's
// exported dto wrappers, given directly here to avoid an import cycle
// concern between v1 and dto for the common case.
func userIDFromCtx(ctx context.Context) (uuid.UUID, bool) {
	return apimw.UserIDFromContext(ctx)
}

func marshalJSON(v map[string]any) ([]byte, error) {
	return jsonMarshal(v)
}

var _ = time.Now
