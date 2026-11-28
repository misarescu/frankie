import { Link } from "@tanstack/react-router";
import { MapPinOffIcon } from "lucide-react";
import { EmptyState } from "@/components/app/states";
import { Button } from "@/components/ui/button";

// NotFoundPage answers a path the UI has no page for, inside the shell.
export function NotFoundPage() {
  return (
    <EmptyState
      icon={<MapPinOffIcon className="size-6" />}
      title="No page here"
      action={<Button asChild variant="outline"><Link to="/">Go to the board</Link></Button>}
    >
      The address does not name a page of Frankie. It may be mistyped, or from a link to something that no longer exists.
    </EmptyState>
  );
}
