# Gate 2 — Revocation SLO — NOT RE-RUN at v1.0.0-rc3

rc2's measurement stands: **p99 0.132s against a 60-second budget**, over 15,000
revocations with the bulk queue saturated. See `../../v1.0.0-rc2/gate2/`.

## Why not, derived rather than asserted

```
$ git diff --stat cdbd15d..HEAD -- internal/provisioning/
(no output — the package is untouched)

$ git diff cdbd15d..HEAD -- internal/api/v1/admin_provisioning.go
62 insertions, 0 deletions
```

Gate 2 measures `provisioning_revocation_seconds` from the originating
entitlement-reducing event to the completed revocation. Its subject is
`internal/provisioning` — the urgent queue, the drivers, the audit rows §2.2
measures between — and **this phase changed none of it**.

The one provisioning-adjacent change is PURELY ADDITIVE: a new
`SetEntitlementRuleEnabled` that makes disabling a rule enqueue the same urgent
revocation deleting one always has. It calls `Urgent.HandleUserChange` with
`eventAt` stamped once, exactly as `DeleteEntitlementRule` does, and it modifies
no line of the path Gate 2 exercises. Zero deletions is the evidence for that.

## The honest counter-argument

This is a REASONING argument, not a measurement, and it is listed in §4's
"check this to disbelieve me" section for that reason. A reader who thinks
either the alerting-role assembly or the entitlement-disable path could perturb
the SLO is owed an hour of machine time that nobody has spent.

`04_RELEASE_GATES.md` §8 blocks the release on all seven gates. Gate 2 is
recorded as PASSING at rc2 and unaffected at rc3 — not as passing at rc3.
