import { useEffect, useState } from "react";
import { ApprovalsPage } from "./ApprovalsPage";
import { RunPage } from "./RunPage";
import { RunsPage } from "./RunsPage";

type Route = { page: "runs" } | { page: "run"; id: string } | { page: "approvals" };

// Hash routing keeps the server trivial: #/runs/<id>, #/approvals or the run list.
function route(): Route {
  const m = /^#\/runs\/([0-9a-f-]{36})$/.exec(window.location.hash);
  if (m?.[1]) return { page: "run", id: m[1] };
  if (window.location.hash === "#/approvals") return { page: "approvals" };
  return { page: "runs" };
}

export function App() {
  const [r, setRoute] = useState(route());
  useEffect(() => {
    const onHash = () => setRoute(route());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  return (
    <main>
      {r.page === "run" ? <RunPage runID={r.id} /> : r.page === "approvals" ? <ApprovalsPage /> : <RunsPage />}
    </main>
  );
}
