// Migrates and seeds the throwaway database the e2e suite runs against.
//
// Authentication is seeded, not performed: /auth/login redirects to EVE
// SSO, which an e2e run must never depend on. app.session is a plain table
// keyed by a uuid, and internal/api/middleware.ResolveSession reads that
// uuid straight out of the `hangar_session` cookie — so inserting a
// session row and handing the browser the matching cookie IS a real login
// as far as every layer under test is concerned. Nothing about the auth
// path is stubbed or bypassed; only the SSO round trip, which is not what
// this suite verifies.
//
// The fixed uuids below are what the specs reference. They are seeded
// values, never generated, so a failing run can be inspected in psql
// afterwards by the same ids the spec names.
import { execFileSync } from "node:child_process";
import { Client } from "pg";

export const ADMIN_USER_ID = "e2e0a11d-0000-4000-8000-000000000001";
export const ADMIN_SESSION_ID = "e2e0a11d-0000-4000-8000-0000000000a1";
export const ADMIN_ROLE_ID = "e2e0a11d-0000-4000-8000-0000000000b1";
export const PLATFORM_ID = "e2e0a11d-0000-4000-8000-0000000000c1";
export const GROUP_A_ID = "e2e0a11d-0000-4000-8000-0000000000d1";
export const GROUP_B_ID = "e2e0a11d-0000-4000-8000-0000000000d2";

/** The seeded starting pin. The pin-advance spec moves it from here. */
export const SEEDED_PIN = "2026-08-04";
/** The seeded D_max — the ceiling the server refuses to advance past. */
export const SEEDED_D_MAX = "2026-08-11";
/** A date beyond D_max, for the refusal case. */
export const BEYOND_D_MAX = "2026-12-01";

export default async function globalSetup(): Promise<void> {
  const url = process.env.HANGAR_DB_URL;
  if (!url) {
    throw new Error(
      "HANGAR_DB_URL is not set. The e2e suite runs against a real, throwaway Postgres — " +
        "point it at one (see `make e2e`), never at a database with data in it.",
    );
  }

  // Migrations through the real command, so the suite exercises the same
  // schema path a deployment does rather than a hand-maintained DDL copy.
  execFileSync("go", ["run", "./cmd/hangar", "migrate", "up"], {
    cwd: "..",
    stdio: "inherit",
  });

  const db = new Client({ connectionString: url });
  await db.connect();
  try {
    await db.query("BEGIN");

    // Idempotent: the suite may be re-run against the same database while
    // iterating. Every insert below is ON CONFLICT-guarded or preceded by
    // a scoped delete.
    await db.query(
      `INSERT INTO app.user (user_id, display_name, is_active, is_admin)
       VALUES ($1, 'E2E Administrator', true, true)
       ON CONFLICT (user_id) DO NOTHING`,
      [ADMIN_USER_ID],
    );

    // A role carrying every permission in the closed vocabulary. Read from
    // app.permission (seeded by the migration from
    // internal/domain/vocabulary.go) rather than listed here, so a
    // permission added in a later phase is granted automatically and this
    // file never becomes a second, drifting copy of the vocabulary.
    await db.query(
      `INSERT INTO app.role (role_id, name, description, is_system)
       VALUES ($1, 'e2e-administrator', 'Every permission, for the e2e suite', false)
       ON CONFLICT (role_id) DO NOTHING`,
      [ADMIN_ROLE_ID],
    );
    await db.query(`DELETE FROM app.role_grant WHERE role_id = $1`, [ADMIN_ROLE_ID]);
    await db.query(
      `INSERT INTO app.role_grant (role_id, permission, effect)
       SELECT $1, permission, 'allow' FROM app.permission`,
      [ADMIN_ROLE_ID],
    );
    await db.query(
      `INSERT INTO app.user_role (user_id, role_id) VALUES ($1, $2)
       ON CONFLICT DO NOTHING`,
      [ADMIN_USER_ID, ADMIN_ROLE_ID],
    );
    // app.effective_permission is the materialised projection every
    // RequirePermission check reads. internal/rbac rematerialises it on
    // every grant change; seeding grants directly means seeding this too.
    await db.query(`DELETE FROM app.effective_permission WHERE user_id = $1`, [
      ADMIN_USER_ID,
    ]);
    await db.query(
      `INSERT INTO app.effective_permission (user_id, permission, permitted)
       SELECT $1, permission, true FROM app.permission`,
      [ADMIN_USER_ID],
    );

    await db.query(
      `INSERT INTO app.session (session_id, user_id, expires_at)
       VALUES ($1, $2, now() + interval '1 day')
       ON CONFLICT (session_id) DO UPDATE SET expires_at = now() + interval '1 day'`,
      [ADMIN_SESSION_ID, ADMIN_USER_ID],
    );

    // The compatibility pin and the D_max ceiling.
    await db.query(
      `INSERT INTO app.setting (key, value) VALUES ('esi.compatibility_pin', $1::jsonb)
       ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
      [JSON.stringify(SEEDED_PIN)],
    );
    await db.query(
      `INSERT INTO app.setting (key, value) VALUES ('esi.d_max', $1::jsonb)
       ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
      [JSON.stringify(SEEDED_D_MAX)],
    );

    // A tiny route catalogue whose compatibility dates straddle the pin in
    // both directions, so the preview has a real, predictable diff to show
    // — including newly BLOCKED routes on a rollback.
    await db.query(`DELETE FROM app.esi_route WHERE operation_id LIKE 'e2e_%'`);
    for (const [opId, compat] of [
      ["e2e_get_old", "2026-08-01"],
      ["e2e_get_on_pin", "2026-08-04"],
      ["e2e_get_mid", "2026-08-07"],
      ["e2e_get_new", "2026-08-11"],
      ["e2e_get_future", "2026-12-01"],
    ] as const) {
      await db.query(
        `INSERT INTO app.esi_route
           (operation_id, method, upstream_path, compatibility_date, blocked_by_pin, spec_fragment, identifier_types)
         VALUES ($1, 'GET', $2, $3::date, $3::date > $4::date, '{}'::jsonb, '{}'::jsonb)`,
        [opId, `/${opId}/`, compat, SEEDED_PIN],
      );
    }

    // One platform with two groups, for the rule editor.
    await db.query(
      `INSERT INTO app.platform (platform_id, kind, name, config, enabled)
       VALUES ($1, 'discord', 'E2E Discord', '{}'::jsonb, true)
       ON CONFLICT (platform_id) DO UPDATE SET name = EXCLUDED.name`,
      [PLATFORM_ID],
    );
    for (const [id, ref, name] of [
      [GROUP_A_ID, "e2e-role-a", "E2E Group A"],
      [GROUP_B_ID, "e2e-role-b", "E2E Group B"],
    ] as const) {
      await db.query(
        `INSERT INTO app.platform_group (group_id, platform_id, remote_ref, name)
         VALUES ($1, $2, $3, $4)
         ON CONFLICT (group_id) DO UPDATE SET name = EXCLUDED.name`,
        [id, PLATFORM_ID, ref, name],
      );
    }
    await db.query(
      `DELETE FROM app.entitlement_rule WHERE group_id IN ($1, $2)`,
      [GROUP_A_ID, GROUP_B_ID],
    );

    await db.query("COMMIT");
  } catch (err) {
    await db.query("ROLLBACK");
    throw err;
  } finally {
    await db.end();
  }
}
