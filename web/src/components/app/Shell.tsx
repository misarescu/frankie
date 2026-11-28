import { Outlet } from "@tanstack/react-router";

// Shell is the app's frame. (Sidebar, header and palette follow.)
export function Shell() {
  return (
    <main className="mx-auto max-w-6xl px-4 py-6">
      <Outlet />
    </main>
  );
}
