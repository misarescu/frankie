import { useQuery } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { CheckIcon, CopyIcon, KeyRoundIcon, LinkIcon, PlusIcon, Trash2Icon, UnlinkIcon } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import {
  useChangePassword, useCreateToken, useRevokeToken, useUnlinkIdentity, useUpdateProfile,
} from "@/api/mutations";
import { meQuery, providersQuery, tokensQuery } from "@/api/queries";
import { roleLabel, ssoErrorMessage, type APIToken } from "@/auth";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { PageHeader } from "@/components/app/PageHeader";
import { LoadingState, WriteError } from "@/components/app/states";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/toast";
import { formatWhen } from "@/format";

const route = getRouteApi("/settings/account");

export function AccountPage() {
  useCrumbs([{ label: "Settings" }, { label: "Account" }]);
  const search = route.useSearch();
  const me = useQuery(meQuery());
  const providers = useQuery(providersQuery());
  const tokens = useQuery(tokensQuery());
  const user = me.data;

  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  useEffect(() => {
    if (user) {
      setDisplayName(user.display_name);
      setEmail(user.email ?? "");
    }
  }, [user]);

  const updateProfile = useUpdateProfile();
  const changePassword = useChangePassword();
  const unlinkIdentity = useUnlinkIdentity();
  const createToken = useCreateToken();
  const revokeToken = useRevokeToken();

  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");

  const [tokenDialog, setTokenDialog] = useState(false);
  const [tokenName, setTokenName] = useState("");
  const [tokenDays, setTokenDays] = useState("90");
  const [createdSecret, setCreatedSecret] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  if (me.isPending || !user) return <LoadingState rows={4} />;

  const linkError = ssoErrorMessage(search.error);
  const identities = user.identities ?? [];
  const authMethodCount = (user.has_password ? 1 : 0) + identities.length;
  const linkedProviders = new Set(identities.map((i) => i.provider));
  const availableSSO = (providers.data?.sso ?? []).filter((p) => !linkedProviders.has(p.id));

  const saveProfile = (e: FormEvent) => {
    e.preventDefault();
    updateProfile.mutate(
      { display_name: displayName.trim(), email: email.trim() },
      { onSuccess: () => toast("Profile updated.") },
    );
  };

  const savePassword = (e: FormEvent) => {
    e.preventDefault();
    changePassword.mutate(
      { current_password: currentPassword, new_password: newPassword },
      {
        onSuccess: () => {
          setCurrentPassword("");
          setNewPassword("");
          toast(user.has_password ? "Password updated. Other sessions were signed out." : "Password set.");
        },
      },
    );
  };

  const submitToken = (e: FormEvent) => {
    e.preventDefault();
    const days = Number(tokenDays);
    createToken.mutate(
      { name: tokenName.trim(), ...(Number.isFinite(days) && days > 0 ? { expires_in_days: Math.round(days) } : {}) },
      {
        onSuccess: (res) => {
          setCreatedSecret(res.secret);
          setTokenName("");
          setCopied(false);
          toast("API token created.");
        },
      },
    );
  };

  const copySecret = async () => {
    if (!createdSecret) return;
    await navigator.clipboard.writeText(createdSecret);
    setCopied(true);
    toast("Copied API token to clipboard.");
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Account & API tokens"
        sub="Manage your profile, sign-in methods and personal API tokens."
      />

      {search.linked && (
        <Alert tone="ok">
          <p>Linked your {search.linked} account.</p>
        </Alert>
      )}
      {linkError && (
        <Alert tone="bad">
          <p>{linkError}</p>
        </Alert>
      )}

      <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Profile</CardTitle>
              <CardDescription>Recorded on decisions, answers, steers and start requests.</CardDescription>
            </div>
            <Badge tone={user.role === "admin" ? "primary" : "neutral"}>{roleLabel(user.role)}</Badge>
          </CardHeader>
          <CardContent>
            <form onSubmit={saveProfile} className="grid gap-3">
              <label className="grid gap-1 text-[13px] font-medium">
                Username
                <Input value={user.username} disabled className="mono" />
              </label>
              <label className="grid gap-1 text-[13px] font-medium">
                Display name
                <Input value={displayName} disabled={updateProfile.isPending} onChange={(e) => setDisplayName(e.target.value)} />
              </label>
              <label className="grid gap-1 text-[13px] font-medium">
                Email
                <Input type="email" value={email} disabled={updateProfile.isPending} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" />
              </label>
              <WriteError error={updateProfile.error} what="update profile" />
              <div>
                <Button type="submit" disabled={updateProfile.isPending}>
                  {updateProfile.isPending ? "Saving…" : "Save profile"}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div>
              <CardTitle>{user.has_password ? "Change password" : "Set a password"}</CardTitle>
              <CardDescription>
                {user.has_password
                  ? "Changing your password signs out your other browser sessions."
                  : "Add a local password alongside your linked SSO account."}
              </CardDescription>
            </div>
          </CardHeader>
          <CardContent>
            <form onSubmit={savePassword} className="grid gap-3">
              {user.has_password && (
                <label className="grid gap-1 text-[13px] font-medium">
                  Current password
                  <Input
                    type="password"
                    autoComplete="current-password"
                    value={currentPassword}
                    disabled={changePassword.isPending}
                    onChange={(e) => setCurrentPassword(e.target.value)}
                  />
                </label>
              )}
              <label className="grid gap-1 text-[13px] font-medium">
                New password (at least 10 characters)
                <Input
                  type="password"
                  autoComplete="new-password"
                  value={newPassword}
                  disabled={changePassword.isPending}
                  onChange={(e) => setNewPassword(e.target.value)}
                />
              </label>
              <WriteError error={changePassword.error} what="update password" />
              <div>
                <Button
                  type="submit"
                  disabled={changePassword.isPending || newPassword.length < 10 || (user.has_password && currentPassword === "")}
                >
                  {changePassword.isPending ? "Updating…" : user.has_password ? "Update password" : "Set password"}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Linked sign-in providers</CardTitle>
            <CardDescription>Link external accounts to sign in with SSO. Keep at least one sign-in method active.</CardDescription>
          </div>
          {availableSSO.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {availableSSO.map((p) => (
                <Button key={p.id} asChild size="sm" variant="outline">
                  <a href={`/api/auth/sso/${encodeURIComponent(p.id)}/link`}>
                    <LinkIcon className="size-3.5" />Link {p.name}
                  </a>
                </Button>
              ))}
            </div>
          )}
        </CardHeader>
        <CardContent className="space-y-3">
          <WriteError error={unlinkIdentity.error} what="unlink provider" />
          {identities.length === 0 ? (
            <p className="text-[13px] text-muted-fg">
              No SSO accounts linked.{(providers.data?.sso.length ?? 0) === 0 ? " No external SSO providers are configured on this deployment." : ""}
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Provider</TableHead>
                  <TableHead>Account</TableHead>
                  <TableHead>Linked</TableHead>
                  <TableHead className="text-right">Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {identities.map((id) => (
                  <TableRow key={id.id}>
                    <TableCell className="font-medium capitalize">{id.provider}</TableCell>
                    <TableCell className="mono text-xs">{id.email || id.login || "linked"}</TableCell>
                    <TableCell className="text-xs text-muted-fg">{formatWhen(id.created_at)}</TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={unlinkIdentity.isPending || authMethodCount <= 1}
                        title={authMethodCount <= 1 ? "Set a password or link another provider before unlinking your last sign-in method" : undefined}
                        onClick={() => unlinkIdentity.mutate(id.id, { onSuccess: () => toast(`Unlinked ${id.provider}.`) })}
                      >
                        <UnlinkIcon className="size-3.5" />Unlink
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Personal API tokens</CardTitle>
            <CardDescription>Bearer tokens (<span className="mono">Authorization: Bearer frk_…</span>) inherit your role and identity for CLI and automation.</CardDescription>
          </div>
          <Button size="sm" onClick={() => { setCreatedSecret(null); setTokenDialog(true); }}>
            <PlusIcon className="size-3.5" />New token
          </Button>
        </CardHeader>
        <CardContent className="space-y-3">
          <WriteError error={revokeToken.error} what="revoke token" />
          {tokens.isPending ? (
            <p className="text-[13px] text-muted-fg">Loading tokens…</p>
          ) : (tokens.data?.length ?? 0) === 0 ? (
            <p className="text-[13px] text-muted-fg">No API tokens created yet.</p>
          ) : (
            <TokenTable
              tokens={tokens.data ?? []}
              busy={revokeToken.isPending}
              onRevoke={(id, name) => revokeToken.mutate(id, { onSuccess: () => toast(`Revoked token "${name}".`) })}
            />
          )}
        </CardContent>
      </Card>

      <Dialog open={tokenDialog} onOpenChange={(o) => { if (!createToken.isPending) setTokenDialog(o); }}>
        <DialogContent>
          {createdSecret ? (
            <div className="grid gap-4">
              <DialogHeader>
                <DialogTitle>Copy your new API token</DialogTitle>
                <DialogDescription>This secret is shown only once. Store it securely before closing this dialog.</DialogDescription>
              </DialogHeader>
              <div className="flex items-center gap-2">
                <Input readOnly value={createdSecret} className="mono text-xs" />
                <Button variant="outline" onClick={() => void copySecret()}>
                  {copied ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
                  {copied ? "Copied" : "Copy"}
                </Button>
              </div>
              <DialogFooter>
                <Button onClick={() => { setTokenDialog(false); setCreatedSecret(null); }}>Done</Button>
              </DialogFooter>
            </div>
          ) : (
            <form onSubmit={submitToken} className="grid gap-4">
              <DialogHeader>
                <DialogTitle>Create personal API token</DialogTitle>
                <DialogDescription>Give the token a descriptive name so you know where it is used.</DialogDescription>
              </DialogHeader>
              <label className="grid gap-1.5 text-[13px] font-medium">
                Token name
                <Input autoFocus value={tokenName} disabled={createToken.isPending} onChange={(e) => setTokenName(e.target.value)} placeholder="e.g. laptop CLI" />
              </label>
              <label className="grid gap-1.5 text-[13px] font-medium">
                Expires in days (optional, 1–365; blank for no expiry)
                <Input type="number" min={1} max={365} value={tokenDays} disabled={createToken.isPending} onChange={(e) => setTokenDays(e.target.value)} />
              </label>
              <WriteError error={createToken.error} what="create token" />
              <DialogFooter>
                <Button variant="outline" type="button" onClick={() => setTokenDialog(false)}>Cancel</Button>
                <Button type="submit" disabled={createToken.isPending || tokenName.trim() === ""}>
                  <KeyRoundIcon className="size-3.5" />Create token
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}

function TokenTable({ tokens, busy, onRevoke }: { tokens: APIToken[]; busy: boolean; onRevoke: (id: string, name: string) => void }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Prefix</TableHead>
          <TableHead>Last used</TableHead>
          <TableHead>Expires</TableHead>
          <TableHead>Status</TableHead>
          <TableHead className="text-right">Action</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {tokens.map((t) => (
          <TableRow key={t.id}>
            <TableCell className="font-medium">{t.name}</TableCell>
            <TableCell className="mono text-xs">{t.token_prefix}…</TableCell>
            <TableCell className="text-xs text-muted-fg">{t.last_used_at ? formatWhen(t.last_used_at) : "Never"}</TableCell>
            <TableCell className="text-xs text-muted-fg">{t.expires_at ? formatWhen(t.expires_at) : "Never"}</TableCell>
            <TableCell>
              {t.revoked_at ? <Badge tone="neutral">Revoked</Badge> : <Badge tone="ok">Active</Badge>}
            </TableCell>
            <TableCell className="text-right">
              {!t.revoked_at && (
                <Button size="sm" variant="outline" disabled={busy} onClick={() => onRevoke(t.id, t.name)}>
                  <Trash2Icon className="size-3.5" />Revoke
                </Button>
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
