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
	"github.com/hangar-project/hangar/internal/scopes"
	"github.com/hangar-project/hangar/internal/sso"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

const authTag = "auth"

func registerAuth(hapi huma.API, deps api.Deps) {
	get[EmptyIn, MeOut](hapi, deps, "", "/api/v1/me", "get-me", "The caller's own user record", authTag, meHandler(deps))
	get[EmptyIn, CollectionOut](hapi, deps, "", "/api/v1/me/characters", "list-my-characters", "Characters linked to the caller's account", authTag, myCharactersHandler(deps))
	// PHASE 20.2 (B26/B40). The caller's own materialised permission set,
	// behind a resolved session and no RBAC permission of its own — a user
	// must be able to discover what they can do without already being able
	// to do something. It is also what lets the SPA hide a control instead
	// of rendering it and 403ing on click, which is SRS §5.2's "degrade
	// gracefully" for a user with a narrow role.
	get[EmptyIn, CollectionOut](hapi, deps, "", "/api/v1/me/permissions", "list-my-permissions", "The caller's own effective permissions", authTag, myPermissionsHandler(deps))
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

// myPermissionsHandler is GET /api/v1/me/permissions. It reads the
// MATERIALISED table, not a live grant resolution, for the same reason
// middleware.RequirePermission does: the materialised row is the
// authoritative answer to "will the next request be allowed", and a live
// recomputation here could disagree with what the middleware will actually
// enforce a millisecond later.
func myPermissionsHandler(deps api.Deps) func(context.Context, *EmptyIn) (*CollectionOut, error) {
	return func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		names, err := deps.Store.ListEffectivePermissions(ctx, userID)
		if err != nil {
			return nil, api.Internal("listing effective permissions", err)
		}
		data := make([]map[string]any, len(names))
		for i, name := range names {
			data[i] = map[string]any{"permission": name}
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{
			Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{},
		}}, nil
	}
}

// myCharactersHandler is GET /api/v1/me/characters.
//
// PHASE 20.3. Each row now carries `needs_reauthorization` and the exact
// `missing_scopes` behind it. Without this the SPA can offer a
// "reauthorize" button but cannot say WHICH characters need it or why, so
// the answer to "why is this character's data stale" is a support question
// rather than something the screen already answers. It is also the
// user-facing half of the scope-reduction trigger: a character that lost a
// scope shows up here immediately, rather than only in the operator's log.
//
// A per-character scope query is one small indexed lookup on a list a user
// realistically has single digits of; it is not worth a join that would
// have to reshape ListCharactersForUser's generated row for every other
// caller.
//
// COST, stated rather than discovered later: the required set comes from
// Flow.RequestedScopes, which is one indexed query against app.esi_route on
// any installation whose catalogue has been ingested — and, on one whose
// catalogue is still EMPTY, a parse of the 587 KB embedded snapshot per
// call. That fallback is deliberate in loginScopeResolver (a value cached
// at boot would be whatever the catalogue held before the background ingest
// finished, which is how B37 would come back), and the installation that
// pays it is a fresh one whose users have no characters to list yet. Worth
// revisiting if this endpoint ever becomes hot before first ingest; not
// worth a cache whose staleness would be invisible.
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

		// Resolved once for the whole list, not per character: it is the
		// same answer for every row and it may hit the catalogue.
		//
		// If it cannot be resolved, the two fields are OMITTED rather than
		// defaulted. `needs_reauthorization: false` on a character we could
		// not check is a reassuring answer to a question that was not
		// asked — SRS §6's empty-versus-unavailable rule, and the same
		// reasoning as a null `divergence` on the rate-limit board.
		if required, rerr := requestedScopes(ctx, deps); rerr == nil {
			for i, ch := range rows {
				grantedList, gerr := deps.Store.ListCharacterTokenScopes(ctx, ch.CharacterID)
				if gerr != nil {
					continue
				}
				missing := scopes.NewSet(grantedList).Missing(required)
				data[i]["needs_reauthorization"] = scopes.NeedsReauthorization(scopes.NewSet(grantedList), required)
				data[i]["missing_scopes"] = missing
			}
		}

		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

// requestedScopes is the scope set HANGAR needs today, or an error when no
// SSO flow is assembled to answer.
func requestedScopes(ctx context.Context, deps api.Deps) ([]string, error) {
	if deps.SSO == nil {
		return nil, errNoSSOFlow
	}
	return deps.SSO.RequestedScopes(ctx)
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

		// Defect B37: this passed []string{}, so the reauthorize URL asked
		// EVE for no scopes and came back with no refresh token — making
		// "reauthorize" a no-op dressed as a fix for the very problem it
		// is offered to solve.
		required, err := deps.SSO.RequestedScopes(ctx)
		if err != nil {
			return nil, api.Internal("resolving login scopes", err)
		}

		// PHASE 20.3. Reauthorize must never NARROW what a character
		// already granted.
		//
		// RequestedScopes answers "what does HANGAR need today", derived
		// from the route catalogue. A character may legitimately hold MORE
		// than that — an operator set HANGAR_SSO_SCOPES wider, or the sync
		// set shrank since they authorised. Sending only the derived set
		// would re-authorise them with fewer scopes than they had, and EVE
		// SSO scopes are replaced per authorization, not merged. That is
		// Gate 2's "scope set reduced" trigger firing as a side effect of
		// the button an operator presses to FIX a token — the login would
		// succeed, the callback would detect the reduction, and HANGAR
		// would dutifully revoke entitlements nobody asked it to.
		//
		// A read failure here is not fatal: falling back to the required
		// set is exactly the old behaviour, and refusing to reauthorize
		// because a scope lookup failed helps nobody. It is logged by
		// being visible in the response's own scope list rather than
		// silently swallowed, since the caller gets the URL it produced.
		granted, scopeErr := deps.Store.ListCharacterTokenScopes(ctx, in.ID)
		scopeList := required
		if scopeErr == nil {
			scopeList = scopes.MergeScopes(granted, required)
		}

		pending, err := deps.SSO.BeginLogin(ctx, scopeList, nil, nil)
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
// /auth/logout directly on mux.
//
// PHASE 15.1: a nil flow is now a PROGRAMMING ERROR, not a supported
// deployment state. cmd/hangar/serve.go always builds a real *sso.Flow and
// aborts startup if it cannot, so a served installation can never reach
// these guards; they answer 500 rather than 501 for the same reason
// reauthorizeHandler does. 501 would advertise "this endpoint is not
// implemented", which is exactly the claim Phase 15.1 exists to stop the
// API making — the login flow IS implemented, and a nil flow here would
// mean the process was assembled wrongly.
//
// The guards are kept rather than removed because RegisterAuthRedirects is
// exported and a future caller (a test, a spec-only build) could still pass
// nil; failing loudly beats a nil-pointer panic in a request handler.
func RegisterAuthRedirects(mux *http.ServeMux, s *store.Store, flow *sso.Flow) {
	mux.HandleFunc("GET /auth/login", func(w http.ResponseWriter, r *http.Request) {
		if flow == nil {
			http.Error(w, "sso flow not assembled — this is a server misconfiguration, not an unimplemented endpoint", http.StatusInternalServerError)
			return
		}
		ip := r.RemoteAddr
		ua := r.UserAgent()
		// Defect B37 — see reauthorizeHandler. An empty scope set here is
		// what made every stored token unusable for ESI.
		scopeList, scopeErr := flow.RequestedScopes(r.Context())
		if scopeErr != nil {
			http.Error(w, "login unavailable: "+scopeErr.Error(), http.StatusInternalServerError)
			return
		}
		pending, err := flow.BeginLogin(r.Context(), scopeList, &ip, &ua)
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
			http.Error(w, "sso flow not assembled — this is a server misconfiguration, not an unimplemented endpoint", http.StatusInternalServerError)
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
