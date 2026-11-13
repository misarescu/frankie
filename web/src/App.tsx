import { useEffect, useState } from "react";
import { RunPage } from "./RunPage";
import { RunsPage } from "./RunsPage";

// Hash routing keeps the server trivial: #/runs/<id> or the run list.
function route(): string | null {
  const m = /^#\/runs\/([0-9a-f-]{36})$/.exec(window.location.hash);
  return m?.[1] ?? null;
}

export function App() {
  const [runID, setRunID] = useState(route());
  useEffect(() => {
    const onHash = () => setRunID(route());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  return <main>{runID ? <RunPage runID={runID} /> : <RunsPage />}</main>;
}
