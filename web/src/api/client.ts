// Typed fetch client for /api/v1, generated types from
// web/src/api/schema.d.ts (pnpm run api:types, consumed from docs/openapi.json
// — do not hand-edit either file). `credentials: "include"` sends the
// HttpOnly `hangar_session` cookie the backend sets from /auth/login and
// /auth/callback (internal/api/v1/auth.go); there is no bearer token on the
// browser side.
import createClient from "openapi-fetch";

import type { paths } from "./schema.d.ts";

export const apiClient = createClient<paths>({
  baseUrl: "/",
  credentials: "include",
});

// Raised when the API answers with the ProblemDetails ("application/problem
// +json") error shape. TanStack Query hooks throw this from their
// queryFn/mutationFn so components can branch on `status` (401 -> send to
// /login, 403 -> degrade gracefully per SRS §5.2, anything else -> the
// generic ErrorBoundary).
export class ApiError extends Error {
  status: number;
  detail?: string;

  constructor(status: number, message: string, detail?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.detail = detail;
  }
}

/**
 * Throws ApiError for any openapi-fetch `error` result. Pass straight
 * through the `{ data, error }` result from an apiClient.GET/POST/... call.
 */
export function unwrap<T>(result: {
  data?: T;
  error?: unknown;
  response: Response;
}): T {
  if (result.error !== undefined) {
    const problem = result.error as
      { title?: string; detail?: string } | undefined;
    throw new ApiError(
      result.response.status,
      problem?.title ?? result.response.statusText,
      problem?.detail,
    );
  }
  // PHASE 18 DEFECT CLOSURE. A successful response that carries NO BODY is
  // not an error. Every `EmptyOut` mutation on the Go side answers
  // 204 No Content — the acknowledge actions, the dead-letter requeue, the
  // squad member add/remove, unlink-character, revoke-share-link,
  // revoke-api-token — and openapi-fetch reports those as
  // `{ data: undefined, error: undefined }`. Throwing here sent every one
  // of them down the mutation's error path: the write had ALREADY
  // succeeded server-side, but `onSuccess` never ran, so no query was
  // invalidated and the screen kept showing the row the operator had just
  // acted on. Found by clicking Acknowledge in a real browser and watching
  // the row stay put while the database said otherwise.
  //
  // 205 Reset Content is included for completeness; nothing emits it today.
  if (result.data === undefined) {
    if (result.response.ok) {
      // 204/205, or any other-bodied success. `undefined` is the honest
      // value; callers of empty mutations ignore it.
      return undefined as T;
    }
    throw new ApiError(result.response.status, "empty response body");
  }
  return result.data;
}
