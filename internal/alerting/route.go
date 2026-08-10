package alerting

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// Target is a routing destination's audience: app.alert_routing_rule's
// (target_kind, target_ref) pair. Kind is one of the CHECK-constrained
// values 'user' | 'squad' | 'corporation' | 'alliance' | 'installation';
// Ref is the entity's id, empty for 'installation' (which has no id — it
// means "this HANGAR installation's operators").
//
// It is one half of §4.4's coalescing key, which is why it is a value type
// with comparable fields: it is used as a map key when grouping rules.
type Target struct {
	Kind string
	Ref  string
}

// String renders a target for logs and for the coalescing key.
func (t Target) String() string {
	if t.Ref == "" {
		return t.Kind
	}
	return t.Kind + ":" + t.Ref
}

// Destination is one concrete place a routed alert must be delivered: a
// channel, plus the mention to lead with. Several destinations can share
// one Target (an operator routing squad alerts to both email and Discord).
type Destination struct {
	Target    Target
	ChannelID uuid.UUID
	Mention   string
}

// Routing is the result of resolving one alert type's rules: destinations
// grouped by target, since §4.4's coalescing key is per (target, alert
// type) and the outbox writes one event per target.
type Routing struct {
	// Targets is every distinct target with at least one enabled rule, in
	// a deterministic order (kind then ref) so a burst of events produces
	// the same event ordering on every replica.
	Targets []Target
	// Destinations maps each target to its channels.
	Destinations map[Target][]Destination
}

// IsEmpty reports whether nothing is routed. A known alert type with no
// routing rules is a completely normal state — nobody has subscribed to it
// — and produces no outbox rows at all rather than a dangling event with
// no deliveries.
func (r Routing) IsEmpty() bool { return len(r.Targets) == 0 }

// Resolve reads the enabled routing rules for alertType and groups them.
func Resolve(ctx context.Context, s *store.Store, alertType string) (Routing, error) {
	rules, err := s.ListAlertRoutingRulesForType(ctx, alertType)
	if err != nil {
		return Routing{}, fmt.Errorf("alerting: reading routing rules for %q: %w", alertType, err)
	}
	return GroupRules(rules), nil
}

// GroupRules turns a flat rule list into a Routing. Split out of Resolve so
// the grouping — the part with the ordering guarantee worth testing — is
// testable without a database.
//
// Two rules with the same (target, channel) pair collapse into one
// destination: an operator who has configured the same channel twice for
// one target wants one message, not two identical ones. The first rule's
// mention wins, by rule order, so the behaviour is deterministic rather
// than dependent on which duplicate the database happened to return first.
func GroupRules(rules []gen.AppAlertRoutingRule) Routing {
	byTarget := make(map[Target][]Destination)
	seen := make(map[struct {
		Target
		uuid.UUID
	}]bool)

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		target := Target{Kind: rule.TargetKind}
		if rule.TargetRef != nil {
			target.Ref = *rule.TargetRef
		}
		key := struct {
			Target
			uuid.UUID
		}{target, rule.ChannelID}
		if seen[key] {
			continue
		}
		seen[key] = true

		dest := Destination{Target: target, ChannelID: rule.ChannelID}
		if rule.Mention != nil {
			dest.Mention = *rule.Mention
		}
		byTarget[target] = append(byTarget[target], dest)
	}

	targets := make([]Target, 0, len(byTarget))
	for t := range byTarget {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Kind != targets[j].Kind {
			return targets[i].Kind < targets[j].Kind
		}
		return targets[i].Ref < targets[j].Ref
	})
	// Channel order within a target is likewise pinned, so a re-run
	// enqueues deliveries in the same order.
	for _, dests := range byTarget {
		sort.Slice(dests, func(i, j int) bool {
			return dests[i].ChannelID.String() < dests[j].ChannelID.String()
		})
	}

	return Routing{Targets: targets, Destinations: byTarget}
}
