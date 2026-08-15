// SRS §4.9's SUBSCRIBER side: registering an outbound webhook endpoint, its
// HMAC signing secret, and its event filter.
//
// ── DEFECT B24, CLOSED IN PHASE 20.5 ─────────────────────────────────────
// The dispatcher has run since Phase 19 (cmd/hangar/webhooks.go), the outbox
// has been written by internal/rbac's mutations since Phase 20.2, and
// internal/events.PendingCount has been counting an outbox nobody could
// subscribe to. crypto.SealWebhookSecret and crypto.NewWebhookSecret had no
// production caller at all: an app.webhook_endpoint row could be created
// only by hand-written SQL against columns holding an envelope-encrypted
// secret whose AAD is bound to the row's own uuid — which is to say, not by
// any operator, only by somebody willing to write Go to do it.
//
// ── THE SECRET IS RETURNED EXACTLY ONCE, AND NEVER AGAIN ─────────────────
// Not by the list endpoint, not by a detail endpoint, not by an admin
// screen. It is shown in the response to the call that MINTED it — create,
// or rotate — and after that it exists only sealed. There is deliberately no
// "show me the secret" endpoint to add later: HANGAR cannot show it without
// holding a decryption path whose only purpose is disclosure, and an owner
// who has lost theirs rotates, which is one call and is the safer answer
// anyway.
package v1

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hangar-project/hangar/internal/api"
	apimw "github.com/hangar-project/hangar/internal/api/middleware"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/events"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

const webhookTag = "Webhooks"

// permWebhooks is the closed-vocabulary permission this whole surface sits
// behind. It already existed — nothing had ever required it.
const permWebhooks = "webhooks.manage"

// RotationGracePeriod is how long a superseded HMAC secret keeps verifying
// after a rotation.
//
// ── WHY 24 HOURS ─────────────────────────────────────────────────────────
// The window has to cover the worst case it exists for: an event that was
// already in the outbox when the rotation happened, dispatched under the
// retry schedule, at a receiver whose operator is asleep. internal/events'
// retry policy exhausts its attempts well inside a day, and a subscriber
// that cannot update a configuration value within one working day has a
// problem a longer window would only postpone.
//
// It is deliberately NOT configurable. A knob here would be a knob whose
// only settings are "shorter than the retry schedule" (which breaks the
// guarantee) and "long enough that the old secret is effectively permanent"
// (which defeats rotating). One correct value is better than an invitation
// to pick a wrong one.
const RotationGracePeriod = 24 * time.Hour

func registerWebhooks(hapi huma.API, deps api.Deps) {
	get[EmptyIn, CollectionOut](hapi, deps, permWebhooks, "/api/v1/me/webhooks", "list-webhook-endpoints",
		"The caller's outbound webhook endpoints (never their signing secrets)", webhookTag, listWebhooksHandler(deps))

	get[EmptyIn, CollectionOut](hapi, deps, permWebhooks, "/api/v1/me/webhooks/event-types", "list-webhook-event-types",
		"The closed vocabulary an endpoint's event_filter may draw from", webhookTag, webhookEventTypesHandler())

	mutate[CreateWebhookIn, WebhookSecretOut](hapi, deps, http.MethodPost, permWebhooks, "/api/v1/me/webhooks", "create-webhook-endpoint",
		"Register an endpoint. The signing secret is returned ONCE, here, and never again", webhookTag, createWebhookHandler(deps))

	mutate[UUIDIn, WebhookSecretOut](hapi, deps, http.MethodPost, permWebhooks, "/api/v1/me/webhooks/{id}/rotate", "rotate-webhook-secret",
		"Replace the signing secret. The previous one keeps verifying for 24h, so nothing already queued is dropped", webhookTag, rotateWebhookHandler(deps))

	mutate[UUIDIn, EmptyOut](hapi, deps, http.MethodDelete, permWebhooks, "/api/v1/me/webhooks/{id}", "revoke-webhook-endpoint",
		"Disable an endpoint and dead-letter everything still owed to it", webhookTag, revokeWebhookHandler(deps))
}

type CreateWebhookIn struct {
	Body struct {
		URL         string   `json:"url" doc:"Absolute https:// URL deliveries are POSTed to."`
		EventFilter []string `json:"event_filter,omitempty" doc:"Event types to receive. EMPTY MEANS EVERYTHING — an omitted filter is not a filter that matches nothing."`
	}
}

// WebhookSecretOut is the only response shape in HANGAR that carries a
// credential in plaintext, and it is returned by exactly two operations.
type WebhookSecretOut struct {
	Body struct {
		EndpointID  string   `json:"endpoint_id"`
		URL         string   `json:"url"`
		EventFilter []string `json:"event_filter"`
		// Secret is lower-case hex, the encoding
		// deploy/verify-webhook-signature.sh expects and the one the header
		// itself uses. THIS IS THE ONLY TIME IT IS EVER RETURNED.
		Secret string `json:"secret"`
		Notice string `json:"notice"`
		// RotationGraceSeconds is present on a rotation response and zero on
		// a create, so an integrator can see how long they have without
		// reading the documentation first.
		RotationGraceSeconds int64 `json:"rotation_grace_seconds,omitempty"`
	}
}

func listWebhooksHandler(deps api.Deps) func(context.Context, *EmptyIn) (*CollectionOut, error) {
	return func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
		userID, ok := apimw.UserIDFromContext(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		rows, err := deps.Store.ListWebhookEndpointsForUser(ctx, userID)
		if err != nil {
			return nil, api.Internal("listing webhook endpoints", err)
		}
		data := make([]map[string]any, len(rows))
		for i, r := range rows {
			// Hand-built rather than dto.Row'd: this table has FOUR columns
			// of key material on it plus four more for a rotation's previous
			// secret, and a generic row projection would ship every one of
			// them the day somebody adds a column. The allowlist is here, in
			// full, and it is short.
			data[i] = map[string]any{
				"endpoint_id":     r.EndpointID,
				"url":             r.Url,
				"event_filter":    r.EventFilter,
				"enabled":         r.Enabled,
				"created_at":      r.CreatedAt,
				"rotated_at":      r.RotatedAt,
				"disabled_at":     r.DisabledAt,
				"disabled_reason": r.DisabledReason,
				// The COUNT, never the secret: an owner debugging failed
				// deliveries needs to know the breaker is climbing.
				"consecutive_failures": r.ConsecutiveFailures,
				// Whether a rotation overlap is still open, as a boolean.
				// The expiry instant would be more informative and is
				// deliberately included; the key material is not.
				"previous_secret_valid_until": r.PrevHmacExpiresAt,
			}
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{
			Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{},
		}}, nil
	}
}

func webhookEventTypesHandler() func(context.Context, *EmptyIn) (*CollectionOut, error) {
	return func(_ context.Context, _ *EmptyIn) (*CollectionOut, error) {
		types := events.KnownTypes()
		data := make([]map[string]any, len(types))
		for i, t := range types {
			data[i] = map[string]any{"event_type": t}
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{
			Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{},
		}}, nil
	}
}

func createWebhookHandler(deps api.Deps) func(context.Context, *CreateWebhookIn) (*WebhookSecretOut, error) {
	return func(ctx context.Context, in *CreateWebhookIn) (*WebhookSecretOut, error) {
		userID, ok := apimw.UserIDFromContext(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		if deps.Keyring == nil {
			return nil, api.Internal("webhook endpoints need envelope-encryption key material", errors.New("no keyring configured"))
		}
		if err := validateEndpointURL(in.Body.URL); err != nil {
			return nil, err
		}
		filter, err := validateEventFilter(in.Body.EventFilter)
		if err != nil {
			return nil, err
		}

		secret, err := crypto.NewWebhookSecret()
		if err != nil {
			return nil, api.Internal("generating a webhook signing secret", err)
		}

		// ── THE ENDPOINT ID CHICKEN-AND-EGG, AND WHY IT IS TWO WRITES ────
		// The AAD binds the sealed secret to the endpoint's own uuid
		// (02_DATABASE_SCHEMA.md §4.6), and the uuid is assigned by the
		// INSERT's uuidv7() default — so the row must exist before its
		// secret can be sealed. Rather than move id generation into Go and
		// lose the column default that every other table relies on, the row
		// is created with a PLACEHOLDER sealing and immediately rotated into
		// its real one, inside one transaction.
		//
		// The placeholder is a freshly generated secret that is discarded
		// and never returned, not a constant: if the transaction were ever
		// to commit half-way despite the guarantee, the row would hold a
		// secret nobody knows rather than one everybody does.
		placeholder, err := crypto.NewWebhookSecret()
		if err != nil {
			return nil, api.Internal("generating a webhook signing secret", err)
		}

		var created gen.AppWebhookEndpoint
		err = store.WithTx(ctx, deps.Pool, func(ctx context.Context, tx *store.Store) error {
			// Sealed against a nil uuid purely to satisfy NOT NULL; it is
			// overwritten below and is never used to sign anything.
			seed, sealErr := crypto.SealWebhookSecret(deps.Keyring, uuid.Nil, placeholder)
			if sealErr != nil {
				return sealErr
			}
			row, insErr := tx.CreateWebhookEndpoint(ctx, gen.CreateWebhookEndpointParams{
				OwnerUserID: userID, Url: in.Body.URL,
				HmacKeyVersion: int32(seed.KeyVersion), HmacWrappedDek: seed.WrappedDEK,
				HmacNonce: seed.Nonce, HmacCiphertext: seed.Ciphertext,
				EventFilter: filter,
			})
			if insErr != nil {
				return insErr
			}
			sealed, sealErr := crypto.SealWebhookSecret(deps.Keyring, row.EndpointID, secret)
			if sealErr != nil {
				return sealErr
			}
			// Reuses the rotation statement, which sets prev_* — harmless
			// and deliberate: the placeholder becomes a "previous secret"
			// nobody holds, it verifies nothing, and it expires on the
			// ordinary schedule. Adding a second write path to avoid one
			// unused row of ciphertext would be the more fragile choice.
			created, insErr = tx.RotateWebhookSecret(ctx, gen.RotateWebhookSecretParams{
				EndpointID: row.EndpointID, OwnerUserID: userID, Grace: RotationGracePeriod,
				HmacKeyVersion: int32(sealed.KeyVersion), HmacWrappedDek: sealed.WrappedDEK,
				HmacNonce: sealed.Nonce, HmacCiphertext: sealed.Ciphertext,
			})
			return insErr
		})
		if err != nil {
			return nil, api.Internal("creating the webhook endpoint", err)
		}

		_ = apimw.Audit(ctx, deps.Store, userID, "webhook.endpoint.created", nil, "",
			map[string]any{"endpoint_id": created.EndpointID.String(), "url": created.Url, "event_filter": created.EventFilter})

		out := &WebhookSecretOut{}
		out.Body.EndpointID = created.EndpointID.String()
		out.Body.URL = created.Url
		out.Body.EventFilter = created.EventFilter
		out.Body.Secret = hex.EncodeToString(secret)
		out.Body.Notice = "This is the only time this secret is shown. Store it now; no endpoint returns it again. " +
			"Verify deliveries with deploy/verify-webhook-signature.sh, or the construction it documents."
		return out, nil
	}
}

func rotateWebhookHandler(deps api.Deps) func(context.Context, *UUIDIn) (*WebhookSecretOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*WebhookSecretOut, error) {
		userID, ok := apimw.UserIDFromContext(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		if deps.Keyring == nil {
			return nil, api.Internal("rotating a webhook secret needs envelope-encryption key material", errors.New("no keyring configured"))
		}
		endpointID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("endpoint id is not a uuid")
		}
		if _, err := deps.Store.GetWebhookEndpointForOwner(ctx, endpointID, userID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, api.NotFound("webhook endpoint")
			}
			return nil, api.Internal("reading the webhook endpoint", err)
		}

		secret, err := crypto.NewWebhookSecret()
		if err != nil {
			return nil, api.Internal("generating a webhook signing secret", err)
		}
		sealed, err := crypto.SealWebhookSecret(deps.Keyring, endpointID, secret)
		if err != nil {
			return nil, api.Internal("sealing the new webhook secret", err)
		}
		rotated, err := deps.Store.RotateWebhookSecret(ctx, gen.RotateWebhookSecretParams{
			EndpointID: endpointID, OwnerUserID: userID, Grace: RotationGracePeriod,
			HmacKeyVersion: int32(sealed.KeyVersion), HmacWrappedDek: sealed.WrappedDEK,
			HmacNonce: sealed.Nonce, HmacCiphertext: sealed.Ciphertext,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, api.NotFound("webhook endpoint")
			}
			return nil, api.Internal("rotating the webhook secret", err)
		}

		_ = apimw.Audit(ctx, deps.Store, userID, "webhook.secret.rotated", nil, "",
			map[string]any{"endpoint_id": endpointID.String(), "grace_seconds": int64(RotationGracePeriod.Seconds())})

		out := &WebhookSecretOut{}
		out.Body.EndpointID = rotated.EndpointID.String()
		out.Body.URL = rotated.Url
		out.Body.EventFilter = rotated.EventFilter
		out.Body.Secret = hex.EncodeToString(secret)
		out.Body.RotationGraceSeconds = int64(RotationGracePeriod.Seconds())
		out.Body.Notice = "This is the only time this secret is shown. The PREVIOUS secret keeps verifying for the grace " +
			"period below: until then every delivery carries two v1= signatures and either matches, so nothing already " +
			"queued is dropped. Update your receiver before the window closes."
		return out, nil
	}
}

func revokeWebhookHandler(deps api.Deps) func(context.Context, *UUIDIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*EmptyOut, error) {
		userID, ok := apimw.UserIDFromContext(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		endpointID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("endpoint id is not a uuid")
		}
		if _, err := deps.Store.GetWebhookEndpointForOwner(ctx, endpointID, userID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, api.NotFound("webhook endpoint")
			}
			return nil, api.Internal("reading the webhook endpoint", err)
		}
		if err := deps.Store.RevokeWebhookEndpointForOwner(ctx, endpointID, userID); err != nil {
			return nil, api.Internal("revoking the webhook endpoint", err)
		}
		// Everything still owed is dead-lettered rather than left pending,
		// for the reason FailOutstandingDeliveriesForEndpoint's own comment
		// gives: LeasePendingWebhookDeliveries joins on e.enabled, so a
		// disabled endpoint's queue is unclaimable and the rows would sit
		// forever in the one state the design exists to rule out — neither
		// delivered nor dead-lettered.
		reason := "endpoint revoked by its owner"
		if err := deps.Store.FailOutstandingDeliveriesForEndpoint(ctx, endpointID, &reason); err != nil {
			return nil, api.Internal("dead-lettering outstanding deliveries", err)
		}
		_ = apimw.Audit(ctx, deps.Store, userID, "webhook.endpoint.revoked", nil, "",
			map[string]any{"endpoint_id": endpointID.String()})
		return &EmptyOut{}, nil
	}
}

// validateEndpointURL refuses what HANGAR cannot deliver to, and says which.
//
// http:// is refused rather than merely discouraged: the delivery body
// carries whatever a §4.9 event carries, the signature proves origin and not
// confidentiality, and an integration configured over plaintext once stays
// that way. A localhost exception is deliberately NOT carved out — an
// operator testing against a local receiver can terminate TLS or use a
// tunnel, and a carve-out is an SSRF surface with a friendly name.
func validateEndpointURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return huma.Error422UnprocessableEntity("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return huma.Error422UnprocessableEntity("url is not a valid URL")
	}
	if u.Scheme != "https" {
		return huma.Error422UnprocessableEntity("url must be https:// — a webhook signature proves who sent a delivery, not that nobody else read it")
	}
	if u.Host == "" {
		return huma.Error422UnprocessableEntity("url must be absolute, with a host")
	}
	return nil
}

// validateEventFilter checks the filter against the closed event vocabulary
// at REGISTRATION time.
//
// An unknown type here is silent forever otherwise: ListEndpointsForEvent
// matches on containment, so a typo'd event type simply never matches and
// the endpoint receives nothing while looking perfectly configured. That is
// the exact failure this whole phase is about, and it is a 422 rather than a
// shrug.
//
// A nil/empty filter is returned as an empty (non-nil) slice, because the
// column is NOT NULL and because ListEndpointsForEvent spells out that an
// empty filter means EVERYTHING.
func validateEventFilter(filter []string) ([]string, error) {
	if len(filter) == 0 {
		return []string{}, nil
	}
	out := make([]string, 0, len(filter))
	for _, t := range filter {
		if !events.Known(events.Type(t)) {
			return nil, huma.Error422UnprocessableEntity(
				"event_filter contains " + t + ", which is not in the closed event vocabulary — see GET /api/v1/me/webhooks/event-types")
		}
		out = append(out, t)
	}
	return out, nil
}
