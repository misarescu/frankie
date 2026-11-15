// WaitNote follows a step's status with what it waits for (steps.ts waitNote):
// a link where there is somewhere to go, plain text otherwise.
export function WaitNote({ note }: { note?: { text: string; href?: string } }) {
  if (!note) return null;
  return <> · {note.href ? <a href={note.href}>{note.text}</a> : note.text}</>;
}
