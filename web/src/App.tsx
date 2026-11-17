import { useEffect, useState } from "react";
import { ApprovalsPage } from "./ApprovalsPage";
import { IssuePage } from "./IssuePage";
import { IssuesPage } from "./IssuesPage";
import { NameBar } from "./NameBar";
import { ReleasePage } from "./ReleasePage";
import { ReleasesPage } from "./ReleasesPage";
import { route } from "./routes";
import { RunPage } from "./RunPage";
import { RunsPage } from "./RunsPage";
import { TokenBar } from "./TokenBar";

export function App() {
  const [r, setRoute] = useState(route(window.location.hash));
  useEffect(() => {
    const onHash = () => setRoute(route(window.location.hash));
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  return (
    <main>
      <TokenBar />
      <NameBar />
      {r.page === "run" ? <RunPage runID={r.id} />
        : r.page === "approvals" ? <ApprovalsPage />
        : r.page === "issues" ? <IssuesPage />
        : r.page === "issue" ? <IssuePage issueKey={r.key} />
        : r.page === "releases" ? <ReleasesPage />
        : r.page === "release" ? <ReleasePage releaseID={r.id} />
        : <RunsPage />}
    </main>
  );
}
