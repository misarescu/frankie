# M9 UI mockup

A clickable mockup of the M9 UI overhaul (`docs/spec/10-roadmap.md`, D141, D142), reviewed and approved by the user on 2026-11-28. Open `index.html` in a browser; it loads React 18 and Tailwind's play CDN from public CDNs, so it needs network access. Every number and issue in it is example data.

`mockup.jsx` is the source. `index.html` embeds it compiled:

```sh
mise exec -- bun build docs/notes/m9-ui-mockup/mockup.jsx --jsx-runtime=classic --outfile /tmp/mockup.js
```

then replace the last `<script>` of `index.html` with the output.

## What it shows

- **Shell:** a sidebar with the project switcher, Board, Runs, Approvals (with a count of what waits) and Releases; a header with breadcrumbs (project > epic > story > task > run > step), search (Cmd-K or Ctrl-K) and a theme toggle. At phone width the sidebar is a drawer.
- **Board:** a summary strip, then the issue tree with status, progress over the leaves of a parent, what each issue waits for, cost (a parent shows its total with its own cost beneath), change request and latest run. List and Dependencies views, show finished, start selected.
- **Issue page** (`gc-83` has the most data, `gc-80` shows an epic): a banner for what waits for a person on the issue, a summary strip, tabs Overview, Runs, Issues (parents) and Activity.
- **Run page:** the steps on the left (a failed `checks` looping back to `implement`, a skipped step with its condition, steps not reached yet), the selected step's transcript on the right, steer and abort for a running step.
- **Approvals:** human gates, protected changes, an agent's question, a refined spec and a release stage gate, decided inline.
- **Releases:** stages, verification, notes, the production gate.

## Feedback from the review, to keep in the real UI

- The browser's back and forward buttons walk every page, tab and selected step.
- The search field shows no focus ring when the palette opens; the first result stays highlighted because Enter opens it.
- Pages in the search results show the icon they have in the sidebar, not a chevron, which reads as "expand".
- Every icon has an explicit size.

## Where the real UI differs

- Path URLs (`/issues/gc-83?tab=runs`) instead of the mockup's hash names, which only exist because the mockup ran inside a sandboxed frame.
- No Settings entry or sign-in placeholder until M10 and M11 add them.
- Activity is derived from the issue's own fields (sync, start requests, spec decisions, runs, closing); the mockup's lines are invented.
