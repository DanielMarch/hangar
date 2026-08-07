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
)

// IdentifierType is the Postgres column type an ESI identifier column must
// use, per its OpenAPI schema (Principle 13, 02_DATABASE_SCHEMA.md §3.2).
// Coercion between the two is prohibited in both directions.
type IdentifierType string

const (
	IdentifierBigInt IdentifierType = "bigint" // OpenAPI: type: integer, format: int64
	IdentifierUUID   IdentifierType = "uuid"   // OpenAPI: type: string, format: uuid
)

// KnownUUIDIdentifiers is the mapping-registry seed for
// `hangar admin verify-identifier-types` (Phase 2): identifier column names
// that are `uuid` rather than the `bigint` default. It is deliberately data,
// not a switch statement — CCP has stated more UUID-keyed routes are coming
// (02_… §3.2), and Phase 2 ingest overlays whatever `identifier_types` each
// app.esi_route row actually declares on top of this seed rather than
// trusting it alone.
var KnownUUIDIdentifiers = map[string]IdentifierType{
	"project_id":            IdentifierUUID, // corporation projects (v1.0)
	"job_id":                IdentifierUUID, // freelance jobs (post-v1.0)
	"campaign_id":           IdentifierUUID, // military campaigns (post-v1.0)
	"objective_id":          IdentifierUUID, // military campaign objectives (post-v1.0)
	"tactical_operation_id": IdentifierUUID, // mercenary tactical operations (post-v1.0)
}

// IdentifierTypeFor returns the expected Postgres type for a column name
// matching `%_id`, defaulting to bigint (the overwhelming majority of ESI
// identifiers) unless the registry above says otherwise.
func IdentifierTypeFor(columnName string) IdentifierType {
	if t, ok := KnownUUIDIdentifiers[columnName]; ok {
		return t
	}
	return IdentifierBigInt
}
