// Questions agents asked with the ask-human tool, as the control plane API
// returns them (internal/api/questions.go, D55).
export type Question = {
  id: string;
  step_run_id: string;
  run_id: string;
  step: string;
  issue_key: string;
  issue_title: string;
  question: string;
  created_at: string;
};

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

// answer sends a person's answer to a question. The control plane answers 202
// once the run's workflow has the signal; the agent then gets the answer as
// its tool's result and the question leaves the inbox.
export async function answer(fetchFn: Fetch, questionID: string, text: string): Promise<void> {
  const r = await fetchFn(`/api/questions/${encodeURIComponent(questionID)}/answer`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text: text.trim() }),
  });
  if (r.status === 409) throw new Error((await r.text()).trim() || "the question no longer waits for an answer");
  if (r.status !== 202) throw new Error((await r.text()).trim() || `status ${r.status}`);
}
