import { useEffect, useState } from "react";
import { clearToken, loadToken, saveToken, TOKEN_REJECTED } from "./auth";

// TokenBar sets the API token (D58) the UI sends with every write. It opens by
// itself when the API rejects a request for a missing or wrong token.
export function TokenBar() {
  const [hasToken, setHasToken] = useState(() => loadToken() !== null);
  const [editing, setEditing] = useState(false);
  const [rejected, setRejected] = useState(false);
  const [value, setValue] = useState("");

  useEffect(() => {
    const onRejected = () => { setRejected(true); setEditing(true); };
    window.addEventListener(TOKEN_REJECTED, onRejected);
    return () => window.removeEventListener(TOKEN_REJECTED, onRejected);
  }, []);

  const save = () => {
    saveToken(value);
    setHasToken(loadToken() !== null);
    setValue("");
    setEditing(false);
    setRejected(false);
  };
  const forget = () => { clearToken(); setHasToken(false); };

  if (!editing) {
    return (
      <p className="token">
        {hasToken ? "API token set" : "No API token: you can look around, but not decide, answer or steer"}
        {" · "}<button className="link" onClick={() => setEditing(true)}>{hasToken ? "change" : "set token"}</button>
        {hasToken && <>{" · "}<button className="link" onClick={forget}>forget</button></>}
      </p>
    );
  }
  return (
    <form className="token" onSubmit={(e) => { e.preventDefault(); save(); }}>
      {rejected && <p className="status">The API needs a valid token for that.</p>}
      <label>
        API token{" "}
        <input type="password" autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)}
          placeholder="from the control plane's Secret, key api-token" />
      </label>
      <div className="actions">
        <button type="submit" disabled={value.trim() === ""}>Save</button>
        <button type="button" className="reject" onClick={() => { setEditing(false); setRejected(false); }}>Cancel</button>
      </div>
    </form>
  );
}
