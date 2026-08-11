// Phase 17 exit criterion `TestMailReaderRendersAndSanitisesBodies`:
// script content in a mail body is neutralised — this half covers the
// actual reader component (lib/sanitize.test.ts covers the pure function).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import "@/i18n";
import { CharacterMail } from "./CharacterMail";

const CHARACTER_ID = 777;
const emptySync = { last_modified_at: null, next_due_at: null, stale: false };

describe("CharacterMail reader", () => {
  it("renders a selected mail's sanitized body and never executes/keeps embedded <script> content", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { staleTime: Infinity, retry: false } },
    });
    const mailId = 9001;

    queryClient.setQueryData(["characters", CHARACTER_ID, "mail"], {
      rows: [
        {
          mail_id: mailId,
          from_id: 123,
          subject: "hello",
          sent_at: "2026-01-01T00:00:00Z",
        },
      ],
      sync: emptySync,
    });
    queryClient.setQueryData(["characters", CHARACTER_ID, "mail", mailId], {
      data: {
        mail_id: mailId,
        body: '<p>Hi there</p><script>document.title = "pwned"</script>',
      },
      sync: emptySync,
    });

    render(
      <QueryClientProvider client={queryClient}>
        <CharacterMail characterId={CHARACTER_ID} />
      </QueryClientProvider>,
    );

    const row = await screen.findByText("hello");
    row.click();

    expect(await screen.findByText("Hi there")).not.toBeNull();
    expect(document.querySelector("script")).toBeNull();
    expect(document.title).not.toBe("pwned");
  });
});
