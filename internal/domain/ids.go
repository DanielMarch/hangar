package domain

// OwnerKind discriminates the owner-polymorphic Tier-2 tables (asset,
// wallet, contract, industry, blueprint, killmail, market_order, contact,
// standing, medal, mining — 02_DATABASE_SCHEMA.md §5.1). Declared here in
// Phase 1a, ahead of the Tier-2 tables that use it in Phase 1b, because
// several Tier-1 concepts (squad_member.character_id, provisioning source
// references) already need to talk about "a character or a corporation"
// without duplicating a table per owner.
type OwnerKind string

const (
	OwnerCharacter   OwnerKind = "character"
	OwnerCorporation OwnerKind = "corporation"
	// OwnerAlliance is added in Phase 1b: app.contact, app.contact_label and
	// app.standing are owner-polymorphic across all three owner kinds
	// (SRS v3.1 §6.2-§6.4 all expose contacts/standings endpoints), unlike
	// the eleven Phase 1a-scoped concepts that only span character/corporation.
	OwnerAlliance OwnerKind = "alliance"
)

// IdentifierType is the Postgres column type an ESI identifier column must
// use, per its OpenAPI schema (Principle 13, 02_DATABASE_SCHEMA.md §3.2).
// Coercion between the two is prohibited in both directions.
type IdentifierType string

const (
	IdentifierBigInt IdentifierType = "bigint" // OpenAPI: type: integer, format: int64
	IdentifierUUID   IdentifierType = "uuid"   // OpenAPI: type: string, format: uuid
)

// IdentifierKey names one column of one table — the registry's actual key.
//
// Phase 1a shipped this registry keyed by column name alone. Phase 1b's
// verify-identifier-types found that false the moment a second, unrelated
// ESI concept reused a generic identifier name with a different type: ESI's
// own /corporations/{id}/industry/jobs `job_id` is `int64` (bigint here),
// while the *post-v1.0* freelance-job concept's own `job_id` is a `uuid`.
// Same column name, two tables, two spec-declared types. A column-name-only
// registry cannot represent that; (table, column) can, and does not assume
// CCP will keep choosing distinct identifier names as the API grows —
// entirely plausible given ESI's history of reusing "id"-suffixed names.
type IdentifierKey struct {
	Table  string
	Column string
}

// KnownUUIDIdentifiers is the mapping-registry seed for
// `hangar admin verify-identifier-types` (Phase 2): identifier columns that
// are `uuid` rather than the `bigint` default. It is deliberately data, not
// a switch statement — CCP has stated more UUID-keyed routes are coming
// (02_… §3.2), and Phase 2 ingest overlays whatever `identifier_types` each
// app.esi_route row actually declares on top of this seed rather than
// trusting it alone.
var KnownUUIDIdentifiers = map[IdentifierKey]IdentifierType{
	{"corporation_project", "project_id"}:                     IdentifierUUID, // corporation projects (v1.0)
	{"corporation_project_contributor", "project_id"}:         IdentifierUUID,
	{"corporation_project_contribution", "project_id"}:        IdentifierUUID,
	{"freelance_job", "job_id"}:                               IdentifierUUID, // post-v1.0
	{"military_campaign", "campaign_id"}:                      IdentifierUUID, // post-v1.0
	{"military_campaign_objective", "objective_id"}:           IdentifierUUID, // post-v1.0
	{"mercenary_tactical_operation", "tactical_operation_id"}: IdentifierUUID, // post-v1.0
}

// InternalUUIDIdentifiers names "%_id"-suffixed columns that are `uuid` by
// HANGAR's own design but neither carry a self-generating DEFAULT
// (uuidv7()/gen_random_uuid()) nor a FOREIGN KEY — the two signals
// verify-identifier-types otherwise uses to recognise a HANGAR-internal
// identity automatically. app.esi_replica.replica_id is the seed case: it
// is minted by Go (uuid.New(), internal/telemetry/replica.go) once per
// process at heartbeat-registration time, not by a database default, so
// nothing about its column definition alone distinguishes it from an
// ESI-sourced identifier that was simply never registered. This registry
// exists so that distinction stays explicit and reviewable rather than
// silently assumed.
var InternalUUIDIdentifiers = map[IdentifierKey]bool{
	{"esi_replica", "replica_id"}: true,
}

// IdentifierTypeFor returns the expected Postgres type for `column` on
// `table`, defaulting to bigint (the overwhelming majority of ESI
// identifiers) unless the registry above says otherwise.
func IdentifierTypeFor(table, column string) IdentifierType {
	if t, ok := KnownUUIDIdentifiers[IdentifierKey{table, column}]; ok {
		return t
	}
	return IdentifierBigInt
}
