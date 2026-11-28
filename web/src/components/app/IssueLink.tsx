import { AppLink } from "@/components/app/AppLink";
import { cn } from "@/lib/utils";
import { paths, type IssueTab } from "@/paths";

// IssueLink links an issue by key, with its title after it when given.
export function IssueLink({ issueKey, title, tab, className }: { issueKey: string; title?: string; tab?: IssueTab; className?: string }) {
  return (
    <AppLink href={paths.issue(issueKey, tab)} className={cn("min-w-0 text-primary hover:underline", className)}>
      <span className="mono text-[12.5px]">{issueKey}</span>
      {title && <span className="text-fg"> {title}</span>}
    </AppLink>
  );
}
