// Toolbar for DataTable.tsx: a single global text filter. SRS §8.1 forbids
// hardcoded English JSX text, so the placeholder routes through i18next
// like every other screen.
import { Search } from "lucide-react";
import { useTranslation } from "react-i18next";

export function DataTableToolbar({
  globalFilter,
  onGlobalFilterChange,
  extra,
}: {
  globalFilter: string;
  onGlobalFilterChange: (value: string) => void;
  extra?: React.ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-2 pb-2">
      <div className="relative max-w-xs flex-1">
        <Search
          className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <input
          value={globalFilter}
          onChange={(e) => onGlobalFilterChange(e.target.value)}
          placeholder={t("dataTable.filterPlaceholder")}
          className="h-8 w-full rounded-md border border-border bg-background pl-7 text-sm outline-none focus-visible:ring-2 focus-visible:ring-cyan-500"
        />
      </div>
      {extra}
    </div>
  );
}
