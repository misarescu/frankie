import { useRouter } from "@tanstack/react-router";
import type { ComponentProps, MouseEvent } from "react";

// AppLink is a link to a path of the app (paths.ts) that navigates inside it;
// a modified or middle click still opens a new tab.
export function AppLink({ href, onClick, ...props }: Omit<ComponentProps<"a">, "href"> & { href: string }) {
  const router = useRouter();
  const go = (e: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(e);
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    void router.navigate({ href });
  };
  return <a href={href} onClick={go} {...props} />;
}
