import { Outlet, useRouterState } from "@tanstack/react-router";
import { MenuIcon, SearchIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { Breadcrumbs, CrumbsProvider } from "@/components/app/Breadcrumbs";
import { CommandPalette } from "@/components/app/CommandPalette";
import { Sidebar } from "@/components/app/Sidebar";
import { ThemeToggle } from "@/components/app/ThemeToggle";
import { Button } from "@/components/ui/button";
import { Kbd } from "@/components/ui/kbd";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet";
import { Toaster } from "@/components/ui/toast";
import { TooltipProvider } from "@/components/ui/tooltip";

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

// Shell is the app's frame (the mockup's): the sidebar, a drawer at phone
// width, the header with breadcrumbs, search and the theme, and the page.
export function Shell() {
  const [drawer, setDrawer] = useState(false);
  const [palette, setPalette] = useState(false);
  const href = useRouterState({ select: (s) => s.location.href });
  useEffect(() => setDrawer(false), [href]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette((p) => !p);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const pad = { paddingLeft: "max(16px, env(safe-area-inset-left, 0px))", paddingRight: "max(16px, env(safe-area-inset-right, 0px))" };
  return (
    <TooltipProvider delayDuration={300}>
      <CrumbsProvider>
        <div className="flex min-h-dvh">
          <aside className="sticky top-0 hidden h-dvh w-60 shrink-0 border-r border-line md:block">
            <Sidebar />
          </aside>
          <Sheet open={drawer} onOpenChange={setDrawer}>
            <SheetContent side="left" className="bg-sidebar p-0">
              <SheetTitle className="sr-only">Menu</SheetTitle>
              <SheetDescription className="sr-only">Projects and pages</SheetDescription>
              <Sidebar onNavigate={() => setDrawer(false)} />
            </SheetContent>
          </Sheet>
          <div className="flex min-w-0 flex-1 flex-col">
            <header className="sticky top-0 z-30 flex h-12 shrink-0 items-center gap-2 border-b border-line bg-bg/95 backdrop-blur" style={pad}>
              <Button variant="ghost" size="icon" className="md:hidden" onClick={() => setDrawer(true)} aria-label="Open menu">
                <MenuIcon className="size-4" />
              </Button>
              <Breadcrumbs />
              <button
                onClick={() => setPalette(true)}
                className="hidden h-8 cursor-pointer items-center gap-2 rounded-md border border-line bg-muted px-2.5 text-[13px] text-muted-fg hover:text-fg sm:flex"
              >
                <SearchIcon className="size-3.5" />
                Search
                <Kbd>{isMac ? "⌘K" : "Ctrl K"}</Kbd>
              </button>
              <Button variant="ghost" size="icon" className="sm:hidden" onClick={() => setPalette(true)} aria-label="Search">
                <SearchIcon className="size-4" />
              </Button>
              <ThemeToggle />
            </header>
            <main className="mx-auto w-full max-w-6xl min-w-0 flex-1 py-6" style={{ ...pad, paddingBottom: "calc(24px + env(safe-area-inset-bottom, 0px))" }}>
              <Outlet />
            </main>
          </div>
          <CommandPalette open={palette} onOpenChange={setPalette} />
          <Toaster />
        </div>
      </CrumbsProvider>
    </TooltipProvider>
  );
}
