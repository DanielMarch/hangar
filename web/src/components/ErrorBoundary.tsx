// SRS §8.3: "Every distinct data module wrapped in an error boundary
// rendering a local retry, never crashing the route." Thin wrapper around
// react-error-boundary so every feature module gets the same fallback UI.
import { useTranslation } from "react-i18next";
import {
  ErrorBoundary as ReactErrorBoundary,
  type FallbackProps,
} from "react-error-boundary";

import { Button } from "@/components/ui/button";

function Fallback({ resetErrorBoundary }: FallbackProps) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-4">
      <p className="text-sm font-medium text-destructive">
        {t("errors.boundaryTitle")}
      </p>
      <p className="text-sm text-muted-foreground">
        {t("errors.boundaryBody")}
      </p>
      <Button variant="outline" size="sm" onClick={resetErrorBoundary}>
        {t("errors.retry")}
      </Button>
    </div>
  );
}

export function ErrorBoundary({ children }: { children: React.ReactNode }) {
  return (
    <ReactErrorBoundary FallbackComponent={Fallback}>
      {children}
    </ReactErrorBoundary>
  );
}
