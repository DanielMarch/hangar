// Vendored shadcn/ui primitive (SRS §8.1 — Radix + cva, not an npm
// component library). Owned by this repo; edit freely.
//
// PHASE 23. The folder had button, badge, dialog and five others and no
// text input: every screen that needed one wrote a raw <input> with the
// class string copied by hand (Platforms.tsx's lockdown reason field is the
// original). Three new admin screens landing at once is where "copied by
// hand" stops being a mechanism — the focus ring in particular is the
// accessibility affordance most easily lost in a copy, and it is the one
// nobody notices is missing.
//
// The class list is Button's, minus the variants an input has no use for
// and plus the file/placeholder rules a text field needs.
import * as React from "react";

import { cn } from "@/lib/utils";

function Input({ className, type, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        "flex h-9 w-full rounded-md border border-border bg-background px-3 py-1 text-sm shadow-xs transition-colors",
        "placeholder:text-muted-foreground",
        "file:inline-flex file:border-0 file:bg-transparent file:text-sm file:font-medium",
        "outline-none focus-visible:ring-2 focus-visible:ring-cyan-500",
        "disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}

export { Input };
