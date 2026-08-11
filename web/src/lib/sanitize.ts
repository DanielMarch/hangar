// Roadmap edge case (Phase 17): "The mail reader must render bodies
// safely — CCP mail contains user-authored HTML; sanitise it." EVE mail
// bodies come back as raw ESI-supplied HTML (internal/api/v1's
// mailBodyHandler does no server-side sanitisation — that's a frontend
// rendering concern, not a storage concern) and are rendered via
// dangerouslySetInnerHTML, so this is the one and only place in the app
// that's allowed to do that.
import DOMPurify from "dompurify";

export function sanitizeMailBody(html: string): string {
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS: [
      "a",
      "b",
      "i",
      "u",
      "em",
      "strong",
      "br",
      "p",
      "div",
      "span",
      "ul",
      "ol",
      "li",
      "font",
      "loc",
      "img",
    ],
    ALLOWED_ATTR: ["href", "src", "color", "size", "alt"],
    ALLOW_DATA_ATTR: false,
  });
}
