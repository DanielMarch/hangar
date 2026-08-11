// Row virtualization for DataTable.tsx (SRS §8.3: "@tanstack/react-table
// with @tanstack/react-virtual for up to 100k rows"). Fixed row height is
// mandatory — the roadmap's own edge case: "100k-row wallet virtualisation
// at 60fps requires fixed row heights; a variable-height row destroys
// scroll performance." DataTable's cells are constrained to single-line,
// truncating content (never wrapping) specifically so this holds.
import { useVirtualizer } from "@tanstack/react-virtual";
import type { RefObject } from "react";

/** Every DataTable row is exactly this tall, in pixels. Do not make it
 * content-dependent — see file banner. */
export const ROW_HEIGHT_PX = 36;

export function useRowVirtualizer(
  rowCount: number,
  scrollRef: RefObject<HTMLDivElement | null>,
) {
  return useVirtualizer({
    count: rowCount,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_HEIGHT_PX,
    overscan: 12,
  });
}
