package api

import (
	"html/template"
	"net/http"
)

const pageStyle = `<style>
:root{color-scheme:light dark;--bg:#fff;--fg:#1d1d1f;--muted:#6e6e73;--line:#e5e5ea;--accent:#5b4bdb}
@media (prefers-color-scheme:dark){:root{--bg:#141416;--fg:#ececf1;--muted:#9a9aa3;--line:#2c2c31;--accent:#a99cff}}
body{margin:0;padding:24px 16px;background:var(--bg);color:var(--fg);font:14px/1.5 -apple-system,system-ui,sans-serif}
main{max-width:960px;margin:0 auto}h1{font-size:20px;margin:0 0 16px}a{color:var(--accent)}
table{width:100%;border-collapse:collapse}td,th{text-align:left;padding:6px 8px;border-bottom:1px solid var(--line)}
th{color:var(--muted);font-weight:500}.ev{border-bottom:1px solid var(--line);padding:8px 0}
.t{display:inline-block;min-width:130px;color:var(--muted);font:12px ui-monospace,monospace}
pre{white-space:pre-wrap;word-break:break-word;margin:4px 0 0;font:12px/1.45 ui-monospace,monospace;max-height:320px;overflow:auto}
#status{color:var(--muted);margin-bottom:12px}
</style>`

var runsTmpl = template.Must(template.New("runs").Parse(`<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Frankie runs</title>` + pageStyle + `</head>
<body><main><h1>Runs</h1><table><tr><th>Issue</th><th>Title</th><th>Status</th><th>Started</th></tr>
{{range .}}<tr><td><a href="/runs/{{.ID}}">{{.IssueKey}}</a></td><td>{{.IssueTitle}}</td><td>{{.Status}}</td>
<td>{{.CreatedAt.Format "2006-01-02 15:04:05"}}</td></tr>{{else}}<tr><td colspan="4">No runs yet.</td></tr>{{end}}
</table></main></body></html>`))

var runTmpl = template.Must(template.New("run").Parse(`<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Run transcript</title>` + pageStyle + `</head>
<body><main><h1><a href="/">Runs</a> / {{.}}</h1><div id="status">connecting…</div><div id="log"></div></main>
<script>
const log = document.getElementById("log"), status = document.getElementById("status");
function summary(e) {
  const p = e.payload || {};
  switch (e.type) {
    case "assistant_message": return p.text;
    case "tool_call": return p.name + " " + JSON.stringify(p.input);
    case "command_output": case "tool_result": return p.output ?? p.content;
    case "file_diff": return p.path + "\n" + p.diff;
    case "token_usage": return "$" + (p.cost_usd ?? 0).toFixed(4) + " · " + p.turns + " turns · " + p.output_tokens + " output tokens";
    case "state_change": return p.entity + ": " + p.from + " → " + p.to;
    case "done": return p.status + (p.reason ? ": " + p.reason : "") + (p.commit_sha ? " · " + p.commit_sha : "");
    case "error": return p.message + (p.detail ? ": " + p.detail : "");
    default: return JSON.stringify(p);
  }
}
const es = new EventSource("/api/runs/{{.}}/stream");
es.onopen = () => status.textContent = "live";
es.onerror = () => status.textContent = "reconnecting…";
es.addEventListener("end", m => { status.textContent = "finished: " + JSON.parse(m.data).status; es.close(); });
const types = ["assistant_message","tool_call","tool_result","file_diff","command_output","token_usage","error","done","gate_result","state_change"];
for (const t of types) es.addEventListener(t, m => {
  const e = JSON.parse(m.data), div = document.createElement("div"), pre = document.createElement("pre");
  div.className = "ev";
  div.innerHTML = '<span class="t"></span>';
  div.firstChild.textContent = "#" + e.seq + " " + e.type;
  pre.textContent = summary(e);
  div.appendChild(pre); log.appendChild(div);
  window.scrollTo(0, document.body.scrollHeight);
});
</script></body></html>`))

func (s *Server) runsPage(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Store.ListRecentRuns(r.Context(), 50)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = runsTmpl.Execute(w, runs)
}

func (s *Server) runPage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.runID(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = runTmpl.Execute(w, id.String())
}
