// Shared Playwright fixtures: both the browser `page` and the standalone
// `request` context carry the seeded admin session cookie.
//
// `request` has to be overridden too, not just `page`. Playwright's
// default `request` fixture is a fresh APIRequestContext that shares
// nothing with the browser context — so a spec that drives the UI through
// `page` and then checks state through `request` would be making its
// checks UNAUTHENTICATED, and a 401 would be mistaken for whatever the
// spec was actually asserting.
import { request as playwrightRequest, test as base, expect } from "@playwright/test";
import type { Cookie } from "@playwright/test";

import { ADMIN_SESSION_ID } from "./global-setup";

function sessionCookie(baseURL: string): Cookie {
  const url = new URL(baseURL);
  return {
    name: "hangar_session",
    value: ADMIN_SESSION_ID,
    domain: url.hostname,
    path: "/",
    httpOnly: true,
    // The server sets this cookie Secure in production; over a loopback
    // http listener the browser would then never send it back, and every
    // request would arrive unauthenticated.
    secure: false,
    sameSite: "Lax",
    expires: -1,
  };
}

const DEFAULT_BASE_URL = "http://127.0.0.1:8099";

export const test = base.extend({
  page: async ({ page, baseURL }, use) => {
    await page.context().addCookies([sessionCookie(baseURL ?? DEFAULT_BASE_URL)]);
    await use(page);
  },
  request: async ({ baseURL }, use) => {
    const url = baseURL ?? DEFAULT_BASE_URL;
    const context = await playwrightRequest.newContext({
      baseURL: url,
      storageState: { cookies: [sessionCookie(url)], origins: [] },
    });
    await use(context);
    await context.dispose();
  },
});

export { expect };
