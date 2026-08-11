// SRS §8.3: "No blocking full-screen spinners. React <Suspense> with
// shape-matched Skeleton components." These compose the vendored
// ui/skeleton.tsx primitive into the shapes this app actually renders while
// data loads, so a loading dashboard card looks like the loaded card, not a
// generic spinner.
import { Skeleton as SkeletonBase } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

export function TextSkeleton({ className }: { className?: string }) {
  return <SkeletonBase className={cn("h-4 w-32", className)} />;
}

export function CardSkeleton({ className }: { className?: string }) {
  return (
    <div
      className={cn(
        "space-y-3 rounded-lg border border-border bg-card p-4",
        className,
      )}
    >
      <SkeletonBase className="h-4 w-24" />
      <SkeletonBase className="h-7 w-40" />
      <SkeletonBase className="h-3 w-28" />
    </div>
  );
}

export function TableSkeleton({ rows = 6 }: { rows?: number }) {
  return (
    <div className="space-y-2">
      {Array.from({ length: rows }, (_, i) => (
        <SkeletonBase key={i} className="h-8 w-full" />
      ))}
    </div>
  );
}
