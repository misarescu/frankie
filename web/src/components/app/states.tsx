import { AlertTriangleIcon, RotateCwIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

// EmptyState says there is nothing to show and what would make something appear.
// level 1 when it is the whole page (a missing issue, an unknown path), so
// the page still has its heading.
export function EmptyState({ icon, title, children, action, className, level = 2 }: {
  icon?: ReactNode; title: string; children?: ReactNode; action?: ReactNode; className?: string; level?: 1 | 2;
}) {
  const Title = level === 1 ? "h1" : "h2";
  return (
    <Card className={cn("px-6 py-12 text-center", className)}>
      {icon && <div className="mx-auto mb-2 w-fit text-muted-fg">{icon}</div>}
      <Title className="font-medium">{title}</Title>
      {children && <div className="mx-auto mt-1 max-w-prose text-[13px] text-muted-fg">{children}</div>}
      {action && <div className="mt-4 flex justify-center">{action}</div>}
    </Card>
  );
}

// ErrorState says a read failed, with the API's reason, and offers to retry.
export function ErrorState({ error, what, onRetry, className, level = 2 }: {
  error: unknown; what: string; onRetry?: () => void; className?: string; level?: 1 | 2;
}) {
  const Title = level === 1 ? "h1" : "h2";
  const message = error instanceof Error ? error.message : String(error);
  return (
    <Card role="alert" className={cn("flex flex-col items-center gap-2 border-bad/40 px-6 py-10 text-center", className)}>
      <AlertTriangleIcon className="size-6 text-bad" />
      <Title className="font-medium">Could not load {what}</Title>
      <p className="max-w-prose text-[13px] text-muted-fg">{message}</p>
      {onRetry && (
        <Button variant="outline" size="sm" className="mt-2" onClick={onRetry}>
          <RotateCwIcon className="size-3.5" />
          Try again
        </Button>
      )}
    </Card>
  );
}

// WriteError says why a write failed, next to the control that sent it,
// in the API's words.
export function WriteError({ error, what, className }: { error: Error | null; what?: string; className?: string }) {
  if (!error) return null;
  return <p role="alert" className={cn("text-xs text-bad [overflow-wrap:anywhere]", className)}>{what && `Could not ${what}: `}{error.message}</p>;
}

// LoadingState holds a page's place while its data loads.
export function LoadingState({ rows = 4, className }: { rows?: number; className?: string }) {
  return (
    <div role="status" aria-label="Loading" className={cn("space-y-3", className)}>
      <Skeleton className="h-7 w-56" />
      <Skeleton className="h-4 w-80 max-w-full" />
      <div className="space-y-2 pt-3">
        {Array.from({ length: rows }, (_, i) => <Skeleton key={i} className="h-10 w-full" />)}
      </div>
    </div>
  );
}
