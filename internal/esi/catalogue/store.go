package catalogue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store is the subset of gen.Querier this package needs. Declared narrowly
// here — rather than depending on the full ~150-method generated interface
// — so a test fake only has to implement what this package actually calls;
// *gen.Queries and *store.Store both satisfy it as-is, with no adapter.
type Store interface {
	GetSetting(ctx context.Context, key string) (gen.AppSetting, error)
	UpsertSetting(ctx context.Context, key string, value json.RawMessage, updatedBy uuid.NullUUID) error
	RecordEsiPinAdvance(ctx context.Context, arg gen.RecordEsiPinAdvanceParams) (gen.AppEsiPinHistory, error)

	UpsertEsiRoute(ctx context.Context, arg gen.UpsertEsiRouteParams) (gen.AppEsiRoute, error)
	GetEsiRouteByOperationID(ctx context.Context, operationID string) (gen.AppEsiRoute, error)
	ListEsiRoutes(ctx context.Context) ([]gen.AppEsiRoute, error)
	ListSchedulableEsiRoutes(ctx context.Context) ([]gen.AppEsiRoute, error)
	ListBlockedEsiRoutes(ctx context.Context) ([]gen.AppEsiRoute, error)
	RetireEsiRoute(ctx context.Context, routeID uuid.UUID) error
	AddEsiRouteScope(ctx context.Context, routeID uuid.UUID, scope string) error
	AddEsiRouteRole(ctx context.Context, routeID uuid.UUID, role string) error

	UpsertEsiScope(ctx context.Context, scope string) error
	RecordOpenVocabularyValue(ctx context.Context, vocabulary string, value string) error
}

// pgDate converts a UTC-midnight time.Time into the pgtype.Date the
// generated store expects. pgtype.Date (not overridden in sqlc.yaml — see
// its Principle 9/13 override list) carries its own Valid flag, so this is
// also how a NULL date is expressed: pgDate on the zero time with Valid
// left false by the caller where that matters (AdvancePin's OldPin is
// always valid in practice, since GetPin always seeds a value).
func pgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}
