package domain

import "fmt"

// Owner identifies one side of an owner-polymorphic Tier-2 row
// (02_DATABASE_SCHEMA.md §5.1): assets, wallets, contracts, industry jobs,
// blueprints, killmails, market orders, contacts, standings and mining
// ledgers all lead with (owner_kind, owner_id) rather than duplicating a
// table per owner.
type Owner struct {
	Kind OwnerKind
	ID   int64
}

// Validate reports whether Kind is one of the three recognised owner kinds
// and ID is a positive ESI identifier. It does not reject a kind unknown to
// this build outright — CCP could in principle extend ownership to a fourth
// concept — but every *table* that uses owner polymorphism today only
// accepts these three, so callers should treat a fourth kind as a defect to
// investigate, not silently store.
func (o Owner) Validate() error {
	switch o.Kind {
	case OwnerCharacter, OwnerCorporation, OwnerAlliance:
	default:
		return fmt.Errorf("domain: unrecognised owner kind %q", o.Kind)
	}
	if o.ID <= 0 {
		return fmt.Errorf("domain: owner id must be positive, got %d", o.ID)
	}
	return nil
}
