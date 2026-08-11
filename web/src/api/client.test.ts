import { describe, expect, it } from "vitest";

import { ApiError, unwrap } from "./client";

// PHASE 18 DEFECT CLOSURE. `unwrap` treated a 204 No Content success as a
// failure, which sent every empty mutation in the app down its error path
// AFTER the write had already succeeded server-side — so nothing was
// invalidated and the screen kept showing stale rows. Every `EmptyOut`
// handler on the Go side answers 204: the two acknowledge actions, the
// dead-letter requeue, squad member add/remove, unlink-character,
// revoke-share-link and revoke-api-token.
//
// Found by clicking Acknowledge in a real browser and watching the row
// stay put while psql said it was gone. jsdom never caught it because no
// component test exercised a 204 response.
describe("unwrap", () => {
  const ok = (status: number) => new Response(null, { status });

  it("returns the body of a normal success", () => {
    const body = { data: { id: 1 } };
    expect(unwrap({ data: body, response: ok(200) })).toBe(body);
  });

  it("accepts a 204 No Content success instead of throwing", () => {
    expect(() =>
      unwrap({ data: undefined, response: ok(204) }),
    ).not.toThrow();
    expect(unwrap({ data: undefined, response: ok(204) })).toBeUndefined();
  });

  it("accepts 205 Reset Content too", () => {
    expect(unwrap({ data: undefined, response: ok(205) })).toBeUndefined();
  });

  it("still throws when a FAILURE arrives with no body", () => {
    // The distinction that matters: an empty 500 is not a success.
    expect(() => unwrap({ data: undefined, response: ok(500) })).toThrow(
      ApiError,
    );
  });

  it("throws ApiError with the problem details on an error result", () => {
    let thrown: unknown;
    try {
      unwrap({
        error: { title: "Unprocessable Entity", detail: "preview_token is required" },
        response: ok(422),
      });
    } catch (e) {
      thrown = e;
    }
    expect(thrown).toBeInstanceOf(ApiError);
    const err = thrown as ApiError;
    expect(err.status).toBe(422);
    expect(err.message).toBe("Unprocessable Entity");
    expect(err.detail).toBe("preview_token is required");
  });

  it("treats a 403 as ApiError so PermissionGate can branch on it", () => {
    let thrown: unknown;
    try {
      unwrap({ error: { title: "Forbidden" }, response: ok(403) });
    } catch (e) {
      thrown = e;
    }
    expect((thrown as ApiError).status).toBe(403);
  });
});
