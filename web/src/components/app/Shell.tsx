import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { MenuIcon, SearchIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { keys, meQuery } from "@/api/queries";
import { AUTH_UNAUTHORIZED, safeNextPath } from "@/auth";
import { Breadcrumbs, CrumbsProvider } from "@/components/app/Breadcrumbs";
import { CommandPalette } from "@/components/app/CommandPalette";
import { Sidebar } from "@/components/app/Sidebar";
import { ErrorState, LoadingState } from "@/components/app/states";
import { ThemeToggle } from "@/components/app/ThemeToggle";
import { Button } from "@/components/ui/button";
import { Kbd } from "@/components/ui/kbd";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet";
import { Toaster } from "@/components/ui/toast";
import { TooltipProvider } from "@/components/ui/tooltip";
import { paths } from "@/paths";

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

function isPublicPath(pathname: string): boolean {
  return pathname === "/login" || pathname.startsWith("/invite/");
}

// Shell is the app's frame: public auth pages render without the chrome, while
// every other page requires a signed-in session and redirects to /login?next=...
export function Shell() {
  const [drawer, setDrawer] = useState(false);
  const [palette, setPalette] = useState(false);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const location = useRouterState({ select: (s) => s.location });
  const isPublic = isPublicPath(location.pathname);

  const me = useQuery({ ...meQuery(), enabled: !isPublic });

  useEffect(() => setDrawer(false), [location.href]);

  useEffect(() => {
    const onUnauthorized = () => {
      qc.setQueryData(keys.me, null);
      if (!isPublicPath(window.location.pathname)) {
        const next = safeNextPath(window.location.pathname + window.location.search);
        void navigate({ href: paths.login(next === "/" ? {} : { next }), replace: true });
      }
    };
    window.addEventListener(AUTH_UNAUTHORIZED, onUnauthorized);
    return () => window.removeEventListener(AUTH_UNAUTHORIZED, onUnauthorized);
  }, [navigate, qc]);

  useEffect(() => {
    if (!isPublic && !me.isPending && !me.isError && me.data === null) {
      const next = safeNextPath(location.pathname + location.searchStr);
      void navigate({ href: paths.login(next === "/" ? {} : { next }), replace: true });
    }
  }, [isPublic, location.pathname, location.searchStr, me.data, me.isError, me.isPending, navigate]);

  useEffect(() => {
    if (isPublic) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette((p) => !p);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [isPublic]);

  if (isPublic) {
    return (
      <TooltipProvider delayDuration={300}>
        <Outlet />
        <Toaster />
      </TooltipProvider>
    );
  }

  if (me.isPending) {
    return (
      <div className="flex min-h-dvh items-center justify-center bg-bg">
        <LoadingState rows={3} className="w-64" />
      </div>
    );
  }

  if (me.isError) {
    return (
      <div className="flex min-h-dvh items-center justify-center bg-bg p-6">
        <div className="w-full max-w-md">
          <ErrorState error={me.error} what="load your session" onRetry={() => void me.refetch()} />
        </div>
      </div>
    );
  }

  if (!me.data) {
    return null;
  }

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
