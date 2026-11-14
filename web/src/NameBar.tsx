import { useState } from "react";
import { loadName, MAX_NAME, saveName } from "./identity";

// NameBar asks once for the name the UI sends as `by` with every write (M5.1),
// so decisions, answers, steers and start requests are recorded under it. It is
// not authentication (open question 7): anyone with the API token can type any
// name.
export function NameBar() {
  const [name, setName] = useState(() => loadName());
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState("");

  if (name && !editing) {
    return (
      <p className="token">
        Acting as <strong>{name}</strong>
        {" · "}<button className="link" onClick={() => { setValue(name); setEditing(true); }}>change</button>
      </p>
    );
  }
  const save = () => {
    saveName(value);
    setName(loadName());
    setEditing(false);
  };
  return (
    <form className="token" onSubmit={(e) => { e.preventDefault(); save(); }}>
      <label>
        Your name{" "}
        <input autoComplete="name" maxLength={MAX_NAME} value={value} onChange={(e) => setValue(e.target.value)}
          placeholder="recorded with your decisions, answers and starts" />
      </label>
      <div className="actions">
        <button type="submit" disabled={value.trim() === ""}>Save</button>
        {editing && <button type="button" className="reject" onClick={() => setEditing(false)}>Cancel</button>}
      </div>
    </form>
  );
}
