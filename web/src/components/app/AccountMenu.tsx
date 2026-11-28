import { ChevronsUpDownIcon, KeyRoundIcon, LogOutIcon, UserIcon } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast";
import { clearToken, loadToken, saveToken, TOKEN_REJECTED } from "@/auth";
import { loadName, MAX_NAME, saveName } from "@/identity";

// AccountMenu sits at the foot of the sidebar: the name the UI records with
// every write (M5.1) and the API token writes need (D58). Neither is sign-in
// (M10). The token dialog opens by itself when the API rejects a write.
export function AccountMenu() {
  const [name, setName] = useState(() => loadName());
  const [hasToken, setHasToken] = useState(() => loadToken() !== null);
  const [dialog, setDialog] = useState<"name" | "token" | null>(null);
  const [rejected, setRejected] = useState(false);
  // A dialog opened from the menu gives focus back to the menu's button.
  const trigger = useRef<HTMLButtonElement>(null);
  const backToMenu = (e: Event) => { e.preventDefault(); trigger.current?.focus(); };

  useEffect(() => {
    const onRejected = () => { setRejected(true); setDialog("token"); };
    window.addEventListener(TOKEN_REJECTED, onRejected);
    return () => window.removeEventListener(TOKEN_REJECTED, onRejected);
  }, []);

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger ref={trigger} className="flex w-full cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-2 text-left hover:bg-hover" aria-label="Your name and API token">
          <span className="grid size-6 shrink-0 place-items-center rounded-full bg-muted text-muted-fg"><UserIcon className="size-3.5" /></span>
          <span className="min-w-0 flex-1 text-xs">
            <span className="block truncate font-medium">{name ?? "Set your name"}</span>
            <span className={hasToken ? "block truncate text-muted-fg" : "block truncate text-warn"}>{hasToken ? "API token set" : "No API token: read only"}</span>
          </span>
          <ChevronsUpDownIcon className="size-3.5 shrink-0 text-muted-fg" />
        </DropdownMenuTrigger>
        <DropdownMenuContent side="top" align="start" className="w-56">
          <DropdownMenuItem onSelect={() => setDialog("name")}><UserIcon className="size-4" />{name ? "Change your name" : "Set your name"}</DropdownMenuItem>
          <DropdownMenuItem onSelect={() => { setRejected(false); setDialog("token"); }}><KeyRoundIcon className="size-4" />{hasToken ? "Change the API token" : "Set the API token"}</DropdownMenuItem>
          {hasToken && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => { clearToken(); setHasToken(false); toast("Forgot the API token."); }}><LogOutIcon className="size-4" />Forget the API token</DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      <NameDialog onCloseAutoFocus={backToMenu} open={dialog === "name"} name={name} onClose={() => setDialog(null)} onSaved={(n) => { setName(n); setDialog(null); }} />
      <TokenDialog onCloseAutoFocus={backToMenu} open={dialog === "token"} rejected={rejected} onClose={() => { setDialog(null); setRejected(false); }}
        onSaved={() => { setHasToken(loadToken() !== null); setDialog(null); setRejected(false); toast("API token saved."); }} />
    </>
  );
}

function NameDialog({ open, name, onClose, onSaved, onCloseAutoFocus }: {
  open: boolean; name: string | null; onClose: () => void; onSaved: (n: string | null) => void; onCloseAutoFocus: (e: Event) => void;
}) {
  const [value, setValue] = useState(name ?? "");
  useEffect(() => { if (open) setValue(name ?? ""); }, [open, name]);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    saveName(value);
    onSaved(loadName());
  };
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent onCloseAutoFocus={onCloseAutoFocus}>
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Your name</DialogTitle>
            <DialogDescription>Recorded with your decisions, answers, steers and start requests. It is not sign-in: anyone with the API token can type any name.</DialogDescription>
          </DialogHeader>
          <label className="grid gap-1.5 text-[13px] font-medium">
            Your name
            <Input autoFocus autoComplete="name" maxLength={MAX_NAME} value={value} onChange={(e) => setValue(e.target.value)} placeholder="e.g. Mihai" />
          </label>
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={value.trim() === ""}>Save</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function TokenDialog({ open, rejected, onClose, onSaved, onCloseAutoFocus }: {
  open: boolean; rejected: boolean; onClose: () => void; onSaved: () => void; onCloseAutoFocus: (e: Event) => void;
}) {
  const [value, setValue] = useState("");
  useEffect(() => { if (open) setValue(""); }, [open]);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    saveToken(value);
    onSaved();
  };
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent onCloseAutoFocus={onCloseAutoFocus}>
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>API token</DialogTitle>
            <DialogDescription>
              {rejected ? "The API needs a valid token for that. " : ""}
              Writes (starting issues, decisions, answers, steers) send it. It stays in this browser. <span className="mono">mise run api-token</span> prints it.
            </DialogDescription>
          </DialogHeader>
          <label className="grid gap-1.5 text-[13px] font-medium">
            API token
            <Input autoFocus type="password" autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)} placeholder="from the control plane's Secret, key api-token" />
          </label>
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={value.trim() === ""}>Save</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
