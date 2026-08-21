# B-4 — the two operator actions, resolved

**Both halves are closed, and only one of them was closed by the operator.** B-4 has been
"blocking, external" since Phase 20.8. It is neither any more.

## Re-authorisation — DONE

The operator revoked HANGAR at EVE account management → Third Party Applications and logged in
again during this phase. Measured immediately before and after:

| | Derived GET scopes | Granted (`app.character_token_scope`) | The diff |
| :-- | --: | --: | :-- |
| Before | 52 | **50** | `esi-universe.read_structures.v1`, `esi-alliances.read_contacts.v1` |
| After | 52 | **52** | none |

Both previously-missing scopes are present:

```
esi-alliances.read_contacts.v1
esi-universe.read_structures.v1
```

`granted-scopes.txt` is the full 52 from the database; `derived-scopes.txt` is what
`go run ./tools/scopedump` derives from the ingested catalogue. **Standing trap 10 did not
fire** — EVE silently drops scopes the SSO registration lacks, and it dropped neither, so the
registration carries all 52.

## Capability #41's structure half — RUNS NOW, and resolves nothing

This is the half nobody could measure before, and the measurement is exactly what Phase 20.9
predicted from reading the code:

```
/universe/structures/{structure_id}
  enabled = true   last_status = 200   last_success = 2026-08-21 02:50:36+00
  sync_run: outcome 200, rows_affected 0, error none
```

**It reached real ESI, succeeded, and affected zero rows.** 20.9's note was:

> when the grant lands the structure fan-out will resolve **nothing and not even 403** —
> `ListCharacterStructureIDs` unions four sources and all four hold zero rows on this
> installation.

That is now observed rather than predicted. The scope gate no longer excludes the subscription,
the reconciler created it, the planner claimed it and the worker ran it — the whole path is live
and there is nothing for it to fan out over. A 403 would have meant the grant was wrong; a 200
with zero rows means the grant is right and the installation is empty.

## Capability #37, alliance contacts — NOT an operator action, and never was

`app.alliance` holds 0 rows, so `ReconcileAllianceSubscriptions` produces nothing and
`AllianceWorker` has still never been dispatched against real ESI. Phase 20.8 recorded that as a
second operator action. **It is not one**, and this phase has the measurement that says so:

```
characters:                  2
corporations:                1
corps with an alliance_id:   0
character 2124613505  corp = 98840805  alliance = null
```

The operator's corporation is **not in an alliance**. There is no action available to them that
would populate `app.alliance` short of joining one, so this is a property of the installation
rather than a task on somebody's list. `TestSyncAllianceSubscriptionsAreOrdered`'s seeded-
integration claim remains the only claim that can be made for `AllianceWorker`, and it will
remain so on any installation whose corporations are unallied.

**Recorded as a limitation of the evidence, not as an outstanding action.** The distinction
matters for the release: an unfinished task blocks, and a fact about one developer's EVE
character does not.
