import { useQuery } from "@tanstack/react-query";
import { CheckIcon, CopyIcon, LogOutIcon, MailPlusIcon, Trash2Icon, UserPlusIcon } from "lucide-react";
import { useState, type FormEvent } from "react";
import {
  useCreateInvite, useCreateUser, useDeleteInvite, useRevokeUserSessions, useUpdateUser,
} from "@/api/mutations";
import { invitesQuery, meQuery, usersQuery } from "@/api/queries";
import { isAdmin, roleLabel, type Role } from "@/auth";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { PageHeader } from "@/components/app/PageHeader";
import { ErrorState, LoadingState, WriteError } from "@/components/app/states";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/toast";
import { formatWhen } from "@/format";

const ROLES: Role[] = ["viewer", "member", "admin"];

export function UsersPage() {
  useCrumbs([{ label: "Settings" }, { label: "Users" }]);
  const me = useQuery(meQuery());
  const admin = isAdmin(me.data?.role);
  const users = useQuery({ ...usersQuery(), enabled: admin });
  const invites = useQuery({ ...invitesQuery(), enabled: admin });

  const createUser = useCreateUser();
  const updateUser = useUpdateUser();
  const revokeSessions = useRevokeUserSessions();
  const createInvite = useCreateInvite();
  const deleteInvite = useDeleteInvite();

  const [userDialog, setUserDialog] = useState(false);
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("member");
  const [password, setPassword] = useState("");

  const [inviteDialog, setInviteDialog] = useState(false);
  const [inviteEmail, setInviteEmail] = useState("");
  const [inviteRole, setInviteRole] = useState<Role>("member");
  const [inviteHours, setInviteHours] = useState("72");
  const [createdInviteURL, setCreatedInviteURL] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  if (me.isPending) return <LoadingState rows={4} />;
  if (!admin) {
    return (
      <div className="space-y-4">
        <PageHeader title="Users & invites" />
        <Alert tone="bad"><p>Administrator role is required to manage users and invites.</p></Alert>
      </div>
    );
  }

  const submitUser = (e: FormEvent) => {
    e.preventDefault();
    createUser.mutate(
      { username: username.trim(), display_name: displayName.trim(), email: email.trim(), role, password },
      {
        onSuccess: (u) => {
          setUserDialog(false);
          setUsername("");
          setDisplayName("");
          setEmail("");
          setRole("member");
          setPassword("");
          toast(`Created user ${u.username}.`);
        },
      },
    );
  };

  const submitInvite = (e: FormEvent) => {
    e.preventDefault();
    const hours = Number(inviteHours);
    createInvite.mutate(
      {
        email: inviteEmail.trim() || undefined,
        role: inviteRole,
        expires_in_hours: Number.isFinite(hours) && hours > 0 ? Math.round(hours) : 72,
      },
      {
        onSuccess: (res) => {
          setCreatedInviteURL(`${window.location.origin}${res.path}`);
          setInviteEmail("");
          setCopied(false);
          toast("Invite link created.");
        },
      },
    );
  };

  const copyInvite = async () => {
    if (!createdInviteURL) return;
    await navigator.clipboard.writeText(createdInviteURL);
    setCopied(true);
    toast("Copied invite link to clipboard.");
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Users & invites"
        sub="Manage accounts, roles, active sessions and single-use invitations."
        actions={
          <>
            <Button variant="outline" onClick={() => { setCreatedInviteURL(null); setInviteDialog(true); }}>
              <MailPlusIcon className="size-3.5" />Create invite
            </Button>
            <Button onClick={() => setUserDialog(true)}>
              <UserPlusIcon className="size-3.5" />Add user
            </Button>
          </>
        }
      />

      <WriteError error={updateUser.error ?? revokeSessions.error} />

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Users</CardTitle>
            <CardDescription>At least one active administrator is always preserved.</CardDescription>
          </div>
        </CardHeader>
        <CardContent>
          {users.isPending ? (
            <LoadingState rows={4} />
          ) : users.isError ? (
            <ErrorState error={users.error} what="load users" onRetry={() => void users.refetch()} />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>User</TableHead>
                  <TableHead>Sign-in</TableHead>
                  <TableHead>Role</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {users.data.map((u) => {
                  const disabled = Boolean(u.disabled_at);
                  return (
                    <TableRow key={u.id}>
                      <TableCell>
                        <div className="font-medium">{u.display_name || u.username}</div>
                        <div className="mono text-xs text-muted-fg">{u.username}{u.email ? ` · ${u.email}` : ""}</div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap gap-1">
                          {u.has_password && <Badge tone="outline">Password</Badge>}
                          {(u.identities ?? []).map((id) => (
                            <Badge key={id.id} tone="outline" className="capitalize">{id.provider}</Badge>
                          ))}
                        </div>
                      </TableCell>
                      <TableCell>
                        <select
                          aria-label={`Role for ${u.username}`}
                          value={u.role}
                          disabled={updateUser.isPending}
                          onChange={(e) => updateUser.mutate(
                            { id: u.id, role: e.target.value as Role },
                            { onSuccess: () => toast(`Updated ${u.username} to ${roleLabel(e.target.value as Role)}.`) },
                          )}
                          className="h-8 rounded-md border border-line bg-bg px-2 text-xs font-medium"
                        >
                          {ROLES.map((r) => <option key={r} value={r}>{roleLabel(r)}</option>)}
                        </select>
                      </TableCell>
                      <TableCell>
                        {disabled ? <Badge tone="bad">Disabled</Badge> : <Badge tone="ok">Active</Badge>}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1.5">
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={revokeSessions.isPending}
                            onClick={() => revokeSessions.mutate(u.id, { onSuccess: () => toast(`Signed out all sessions for ${u.username}.`) })}
                          >
                            <LogOutIcon className="size-3.5" />Revoke sessions
                          </Button>
                          <Button
                            size="sm"
                            variant={disabled ? "outline" : "destructive"}
                            disabled={updateUser.isPending}
                            onClick={() => updateUser.mutate(
                              { id: u.id, disabled: !disabled },
                              { onSuccess: () => toast(disabled ? `Re-enabled ${u.username}.` : `Disabled ${u.username}.`) },
                            )}
                          >
                            {disabled ? "Enable" : "Disable"}
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Invitations</CardTitle>
            <CardDescription>Single-use links that let a person create a password or SSO account with a chosen role.</CardDescription>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          <WriteError error={deleteInvite.error} what="delete invite" />
          {invites.isPending ? (
            <p className="text-[13px] text-muted-fg">Loading invites…</p>
          ) : (invites.data?.length ?? 0) === 0 ? (
            <p className="text-[13px] text-muted-fg">No invitations yet.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Email</TableHead>
                  <TableHead>Role</TableHead>
                  <TableHead>Invited by</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(invites.data ?? []).map((inv) => (
                  <TableRow key={inv.id}>
                    <TableCell className="mono text-xs">{inv.email || "Any email"}</TableCell>
                    <TableCell><Badge tone="neutral">{roleLabel(inv.role)}</Badge></TableCell>
                    <TableCell className="text-xs text-muted-fg">{inv.invited_by || "—"}</TableCell>
                    <TableCell className="text-xs text-muted-fg">{formatWhen(inv.expires_at)}</TableCell>
                    <TableCell>
                      {inv.accepted_at ? <Badge tone="ok">Accepted</Badge> : <Badge tone="warn">Pending</Badge>}
                    </TableCell>
                    <TableCell className="text-right">
                      {!inv.accepted_at && (
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={deleteInvite.isPending}
                          onClick={() => deleteInvite.mutate(inv.id, { onSuccess: () => toast("Invite revoked.") })}
                        >
                          <Trash2Icon className="size-3.5" />Revoke
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Dialog open={userDialog} onOpenChange={(o) => { if (!createUser.isPending) setUserDialog(o); }}>
        <DialogContent>
          <form onSubmit={submitUser} className="grid gap-3">
            <DialogHeader>
              <DialogTitle>Add user</DialogTitle>
              <DialogDescription>Create an account with a local password.</DialogDescription>
            </DialogHeader>
            <label className="grid gap-1 text-[13px] font-medium">
              Username
              <Input autoFocus value={username} disabled={createUser.isPending} onChange={(e) => setUsername(e.target.value)} placeholder="e.g. ana" />
            </label>
            <label className="grid gap-1 text-[13px] font-medium">
              Display name
              <Input value={displayName} disabled={createUser.isPending} onChange={(e) => setDisplayName(e.target.value)} placeholder="e.g. Ana" />
            </label>
            <label className="grid gap-1 text-[13px] font-medium">
              Email (optional)
              <Input type="email" value={email} disabled={createUser.isPending} onChange={(e) => setEmail(e.target.value)} placeholder="ana@example.com" />
            </label>
            <label className="grid gap-1 text-[13px] font-medium">
              Role
              <select
                value={role}
                disabled={createUser.isPending}
                onChange={(e) => setRole(e.target.value as Role)}
                className="h-9 rounded-md border border-line bg-bg px-2.5 text-[13px]"
              >
                {ROLES.map((r) => <option key={r} value={r}>{roleLabel(r)}</option>)}
              </select>
            </label>
            <label className="grid gap-1 text-[13px] font-medium">
              Initial password (at least 12 characters)
              <Input type="password" autoComplete="new-password" value={password} disabled={createUser.isPending} onChange={(e) => setPassword(e.target.value)} />
            </label>
            <WriteError error={createUser.error} what="create user" />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setUserDialog(false)}>Cancel</Button>
              <Button type="submit" disabled={createUser.isPending || username.trim() === "" || password.length < 12}>Create user</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={inviteDialog} onOpenChange={(o) => { if (!createInvite.isPending) setInviteDialog(o); }}>
        <DialogContent>
          {createdInviteURL ? (
            <div className="grid gap-4">
              <DialogHeader>
                <DialogTitle>Share this invite link</DialogTitle>
                <DialogDescription>This link is shown only once and can be used to create one account.</DialogDescription>
              </DialogHeader>
              <div className="flex items-center gap-2">
                <Input readOnly value={createdInviteURL} className="mono text-xs" />
                <Button variant="outline" onClick={() => void copyInvite()}>
                  {copied ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
                  {copied ? "Copied" : "Copy"}
                </Button>
              </div>
              <DialogFooter>
                <Button onClick={() => { setInviteDialog(false); setCreatedInviteURL(null); }}>Done</Button>
              </DialogFooter>
            </div>
          ) : (
            <form onSubmit={submitInvite} className="grid gap-3">
              <DialogHeader>
                <DialogTitle>Create invite link</DialogTitle>
                <DialogDescription>Optionally restrict the invite to a specific email address.</DialogDescription>
              </DialogHeader>
              <label className="grid gap-1 text-[13px] font-medium">
                Email (optional)
                <Input autoFocus type="email" value={inviteEmail} disabled={createInvite.isPending} onChange={(e) => setInviteEmail(e.target.value)} placeholder="ana@example.com" />
              </label>
              <label className="grid gap-1 text-[13px] font-medium">
                Role
                <select
                  value={inviteRole}
                  disabled={createInvite.isPending}
                  onChange={(e) => setInviteRole(e.target.value as Role)}
                  className="h-9 rounded-md border border-line bg-bg px-2.5 text-[13px]"
                >
                  {ROLES.map((r) => <option key={r} value={r}>{roleLabel(r)}</option>)}
                </select>
              </label>
              <label className="grid gap-1 text-[13px] font-medium">
                Expires in hours (1–720)
                <Input type="number" min={1} max={720} value={inviteHours} disabled={createInvite.isPending} onChange={(e) => setInviteHours(e.target.value)} />
              </label>
              <WriteError error={createInvite.error} what="create invite" />
              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setInviteDialog(false)}>Cancel</Button>
                <Button type="submit" disabled={createInvite.isPending}>Create invite</Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
