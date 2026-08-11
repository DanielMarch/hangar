// Phase 17 exit criterion `TestMailReaderRendersAndSanitisesBodies`:
// script content in a mail body is neutralised.
import { describe, expect, it } from "vitest";

import { sanitizeMailBody } from "./sanitize";

describe("sanitizeMailBody", () => {
  it("strips <script> tags entirely", () => {
    const out = sanitizeMailBody('<p>Hi</p><script>alert("pwned")</script>');
    expect(out).not.toContain("<script");
    expect(out).not.toContain("alert(");
    expect(out).toContain("Hi");
  });

  it("strips inline event-handler attributes (onerror, onclick, ...)", () => {
    const out = sanitizeMailBody(
      '<img src="x" onerror="alert(1)"><a href="#" onclick="steal()">link</a>',
    );
    expect(out).not.toContain("onerror");
    expect(out).not.toContain("onclick");
  });

  it("strips javascript: URLs", () => {
    const out = sanitizeMailBody('<a href="javascript:alert(1)">click</a>');
    expect(out.toLowerCase()).not.toContain("javascript:");
  });

  it("preserves ordinary EVE-mail-shaped markup (links, formatting, the CCP <loc> tag)", () => {
    const out = sanitizeMailBody(
      '<p>See <a href="https://example.com">this</a> and <b>bold</b> text.</p><loc>Jita</loc>',
    );
    expect(out).toContain("<a");
    expect(out).toContain("href=");
    expect(out).toContain("<b>");
    expect(out).toContain("Jita");
  });
});
