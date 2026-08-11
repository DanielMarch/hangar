// Roadmap edge case: "The mail reader must render bodies safely — CCP mail
// contains user-authored HTML; sanitise it." The header list (this
// component) is an ordinary DataTable; selecting a row fetches that one
// mail's body (a separate ESI-shaped resource, GET .../mail/{sub_id}) and
// renders it through sanitizeMailBody() — never the raw string.
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { useCollection, useItem } from "@/api/queries/collection";
import { CollectionTable } from "@/components/data-table/CollectionTable";
import { dateColumn, textColumn } from "@/components/data-table/columns";
import { ItemPanel } from "@/components/ItemPanel";
import { sanitizeMailBody } from "@/lib/sanitize";
import {
  characterMailBodyPath,
  characterMailPath,
} from "@/features/character/queries";

export function CharacterMail({ characterId }: { characterId: number }) {
  const { t } = useTranslation();
  const mailColumns = useMemo(
    () => [
      textColumn("from_id", t("characters.mail.from")),
      textColumn("subject", t("characters.mail.subject")),
      dateColumn("sent_at", t("characters.mail.sentAt")),
    ],
    [t],
  );
  const mail = useCollection(
    characterMailPath,
    { params: { path: { id: characterId } } },
    ["characters", characterId, "mail"],
  );
  const [selectedMailId, setSelectedMailId] = useState<number | null>(null);

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <CollectionTable
        query={mail}
        columns={mailColumns}
        title={t("characters.mail.inbox")}
        getRowId={(r) => String(r.mail_id)}
        onRowClick={(row) => setSelectedMailId(Number(row.mail_id))}
      />
      <div className="rounded-md border border-border bg-card p-4">
        {selectedMailId === null ? (
          <p className="text-sm text-muted-foreground">
            {t("characters.mail.noSelection")}
          </p>
        ) : (
          <MailBody characterId={characterId} mailId={selectedMailId} />
        )}
      </div>
    </div>
  );
}

function MailBody({
  characterId,
  mailId,
}: {
  characterId: number;
  mailId: number;
}) {
  const query = useItem(
    characterMailBodyPath,
    { params: { path: { id: characterId, sub_id: mailId } } },
    ["characters", characterId, "mail", mailId],
  );

  return (
    <ItemPanel query={query}>
      {(body) => {
        const html = typeof body.body === "string" ? body.body : "";
        return (
          // dompurify-sanitized ESI mail HTML — see lib/sanitize.ts's file
          // banner for why this is the one sanctioned dangerouslySetInnerHTML.
          <div
            className="prose prose-sm prose-invert max-w-none text-sm"
            dangerouslySetInnerHTML={{ __html: sanitizeMailBody(html) }}
          />
        );
      }}
    </ItemPanel>
  );
}
