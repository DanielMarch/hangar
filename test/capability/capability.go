// Package capability holds the verification tests Appendix A's traceability
// matrix cites, one test per capability row, named exactly as the row names
// it.
//
// ── DEFECT B51 (PHASE 20.7), CLOSED IN 20.8 ──────────────────────────────
//
// traceability.csv's `verification_test` column is what makes a row's
// "verified" status mean anything: it names the test that proves the
// capability works. Every value was hand-written and nothing ever confirmed
// the named test existed. Measured in 20.7: of 45 rows naming a test, THREE
// named one that exists and FORTY-TWO named one that did not.
// tools/gate4-traceability/verification_tests.go now measures it and fails
// the gate on the count. This package is the answer.
//
// ── WHAT ONE OF THESE TESTS ASSERTS, AND WHY THAT IS THE RIGHT SET ───────
//
// A stub named TestSyncAssets that asserted nothing would satisfy the
// checker and be a worse lie than the missing test, because it would look
// checked. So each test here asserts the FOUR links of a capability's
// delivery chain, and each link is one that has actually broken in this
// codebase's recorded history:
//
//  1. THE ROUTE IS REACHABLE. Every upstream ESI route the capability names
//     is in worker.SubscribableRoutes() under the entity kind whose worker
//     has a handler for it. A route absent here can never be scheduled, so
//     the capability produces nothing on every installation, silently —
//     defects B30 (thirteen fan-out handlers no subscription could name),
//     B42 (no subscription ever created at all), B47 (corporation assets)
//     and B48 (nine capabilities with tables, endpoints and no writer).
//
//  2. THE DTO MATCHES THE SPEC. Every property the LIVE SPEC marks required
//     on the route's 200 response is a field the handler's DTO actually
//     carries. This is defect B50, found in 20.7: the corporation-projects
//     DTO matched no response ESI has ever sent, and it looked fine because
//     it was only ever compared against its own field names. Comparing
//     against the spec is a different question with a different answer.
//
//  3. THE RECORDED RESPONSE PARSES TO THE RIGHT VALUES. Where a real
//     captured response exists under testdata/esi, it is parsed and
//     CONCRETE VALUES are asserted — not merely that parsing returned no
//     error. golden_test.go already proves no field is LOST; these prove the
//     fields mean what the handler thinks they mean.
//
//  4. THE DATA IS SERVED. Every /api/v1 endpoint the capability names is
//     registered in docs/openapi.json. Capability #8 shipped a working
//     fittings sync in 20.7 whose data no screen could reach; the endpoint
//     half of that chain is the half nothing had ever checked, and checking
//     it here is what turned up defect B52 (seven of twenty-two endpoint
//     citations naming no registered path).
//
// ── WHAT THEY DELIBERATELY DO NOT ASSERT ─────────────────────────────────
//
// The database write. A Sync* function needs Postgres, and faking a store to
// assert "Go called a method" is the kind of test that let B42 exist. Where
// the write is the interesting risk, the capability's test has an
// integration-tagged companion in capability_integration_test.go that runs
// against real Postgres; where an existing integration test already covers
// it (handlers' golden_corporation_test.go, sync_idempotency_integration_test.go,
// phase9_exit_integration_test.go), the test here says so by name rather
// than duplicating it.
package capability
