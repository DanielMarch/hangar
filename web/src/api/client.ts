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
  if (result.data === undefined) {
    throw new ApiError(result.response.status, "empty response body");
  }
  return result.data;
}
