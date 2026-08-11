import { describe, expect, it } from "vitest";

import { ageSeconds, formatAge, REVOCATION_SLO_SECONDS } from "./age";

// The arithmetic half of TestExposureBoardShowsExactAges. The rendering
// half (that the board reads `event_at` and nothing else) is asserted in
// ExposureBoard.test.tsx.
describe("ageSeconds", () => {
  const now = Date.parse("2026-08-11T12:00:00Z");

  it("measures from event_at, exactly", () => {
    expect(ageSeconds("2026-08-11T11:59:01Z", now)).toBe(59);
    expect(ageSeconds("2026-08-11T11:00:00Z", now)).toBe(3600);
    expect(ageSeconds("2026-08-11T12:00:00Z", now)).toBe(0);
  });

  it("truncates rather than rounds, so an age never reads older than it is", () => {
    expect(ageSeconds("2026-08-11T11:59:59.900Z", now)).toBe(0);
  });

  it("clamps a future event_at to zero rather than reporting a negative age", () => {
    // Clock skew between the API server and whatever enqueued the audit
    // row. "-3s of exposure" is not a thing.
    expect(ageSeconds("2026-08-11T12:00:03Z", now)).toBe(0);
  });

  it("returns null for a row with no event_at", () => {
    // A mismatched provisioning_state row is a live desired-vs-actual
    // disagreement, not an enqueued action, and has no event_at. It must
    // render as "—" rather than be aged from some other timestamp, which
    // would answer a different question.
    expect(ageSeconds(null, now)).toBeNull();
    expect(ageSeconds(undefined, now)).toBeNull();
    expect(ageSeconds("", now)).toBeNull();
    expect(ageSeconds("not a date", now)).toBeNull();
    expect(ageSeconds(1754913600, now)).toBeNull();
  });
});

describe("formatAge", () => {
  it("is exact to the second, never relative", () => {
    expect(formatAge(0)).toBe("0s");
    expect(formatAge(9)).toBe("9s");
    expect(formatAge(59)).toBe("59s");
    expect(formatAge(60)).toBe("1m 00s");
    expect(formatAge(3849)).toBe("1h 04m 09s");
    expect(formatAge(86_399)).toBe("23h 59m 59s");
  });

  it("keeps the SLO boundary legible", () => {
    expect(formatAge(REVOCATION_SLO_SECONDS)).toBe("1m 00s");
    expect(formatAge(REVOCATION_SLO_SECONDS + 1)).toBe("1m 01s");
  });
});
