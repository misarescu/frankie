// Frankie's read-only shell for pi (bash:readonly, docs/spec/04). The runner
// loads it with -e and lists the allowed commands in FRANKIE_READONLY_COMMANDS
// (runner.ReadOnlyCommands). A bash call runs only if every segment of a
// pipeline is one of them, alone or with arguments; redirections, command
// lists, substitutions and multi-line scripts are refused.
export default function (pi: any) {
  const allowed: string[] = JSON.parse(process.env.FRANKIE_READONLY_COMMANDS ?? "[]");
  const permitted = (segment: string): boolean => {
    const s = segment.trim();
    return allowed.some((c) => s === c || s.startsWith(c + " "));
  };
  pi.on("tool_call", (event: any) => {
    if (event.toolName !== "bash") {
      return;
    }
    const command = String(event.input?.command ?? "");
    if (/[;&<>`\n\r]|\$\(/.test(command) || !command.split("|").every(permitted)) {
      return { block: true, reason: "Only read-only commands are allowed here: " + allowed.join(", ") };
    }
  });
}
