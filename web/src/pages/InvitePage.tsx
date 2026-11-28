import { useQuery } from "@tanstack/react-query";
import { getRouteApi, useNavigate } from "@tanstack/react-router";
import { UserPlusIcon } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { useAcceptInvite } from "@/api/mutations";
import { invitePreviewQuery, providersQuery } from "@/api/queries";
import { roleLabel } from "@/auth";
import { AppLink } from "@/components/app/AppLink";
import { WriteError } from "@/components/app/states";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { formatWhen } from "@/format";
import { paths } from "@/paths";

const route = getRouteApi("/invite/$token");

export function InvitePage() {
  const { token } = route.useParams();
  const navigate = useNavigate();
  const preview = useQuery(invitePreviewQuery(token));
  const providers = useQuery(providersQuery());
  const accept = useAcceptInvite();

  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  useEffect(() => {
    if (preview.data?.email) {
      setEmail(preview.data.email);
    }
  }, [preview.data?.email]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    accept.mutate(
      { token, username: username.trim(), display_name: displayName.trim(), email: email.trim(), password },
      { onSuccess: () => void navigate({ href: "/", replace: true }) },
    );
  };

  const ssoList = providers.data?.sso ?? [];
  const lockedEmail = Boolean(preview.data?.email);

  return (
    <div className="flex min-h-dvh items-center justify-center bg-bg px-4 py-10">
      <div className="w-full max-w-sm space-y-5">
        <div className="flex items-center justify-center gap-2.5">
          <span className="grid size-8 place-items-center rounded-md bg-primary text-sm font-bold text-primary-fg">F</span>
          <span className="text-lg font-semibold tracking-tight">Frankie</span>
        </div>

        <Card>
          <CardHeader className="flex-col items-start gap-1">
            <CardTitle>Accept invitation</CardTitle>
            <CardDescription>Create your Frankie account from this single-use invite.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {preview.isPending && <p className="text-[13px] text-muted-fg">Checking invite…</p>}

            {preview.isError && (
              <div className="space-y-3">
                <Alert tone="bad">
                  <p>This invite link is invalid, expired, or has already been used.</p>
                </Alert>
                <Button asChild variant="outline" className="w-full">
                  <AppLink href={paths.login()}>Back to sign in</AppLink>
                </Button>
              </div>
            )}

            {preview.data && (
              <>
                <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-line bg-muted px-3 py-2 text-xs">
                  <span>Role: <Badge tone="primary">{roleLabel(preview.data.role)}</Badge></span>
                  <span className="text-muted-fg">Expires {formatWhen(preview.data.expires_at)}</span>
                </div>

                <form onSubmit={submit} className="grid gap-3">
                  <label className="grid gap-1.5 text-[13px] font-medium">
                    Username
                    <Input
                      autoFocus
                      autoComplete="username"
                      value={username}
                      disabled={accept.isPending}
                      onChange={(e) => setUsername(e.target.value)}
                      placeholder="e.g. ana"
                    />
                  </label>
                  <label className="grid gap-1.5 text-[13px] font-medium">
                    Display name
                    <Input
                      autoComplete="name"
                      value={displayName}
                      disabled={accept.isPending}
                      onChange={(e) => setDisplayName(e.target.value)}
                      placeholder="e.g. Ana"
                    />
                  </label>
                  <label className="grid gap-1.5 text-[13px] font-medium">
                    Email
                    <Input
                      type="email"
                      autoComplete="email"
                      value={email}
                      disabled={accept.isPending || lockedEmail}
                      onChange={(e) => setEmail(e.target.value)}
                      placeholder="ana@example.com"
                    />
                  </label>
                  <label className="grid gap-1.5 text-[13px] font-medium">
                    Password (at least 12 characters)
                    <Input
                      type="password"
                      autoComplete="new-password"
                      value={password}
                      disabled={accept.isPending}
                      onChange={(e) => setPassword(e.target.value)}
                    />
                  </label>
                  <WriteError error={accept.error} what="create account" />
                  <Button type="submit" className="w-full" disabled={accept.isPending || username.trim() === "" || password.length < 12}>
                    <UserPlusIcon className="size-3.5" />
                    {accept.isPending ? "Creating account…" : "Create account"}
                  </Button>
                </form>

                {ssoList.length > 0 && (
                  <>
                    <div className="relative flex items-center py-1">
                      <div className="flex-1 border-t border-line" />
                      <span className="px-2.5 text-xs text-muted-fg">or accept with SSO</span>
                      <div className="flex-1 border-t border-line" />
                    </div>
                    <div className="grid gap-2">
                      {ssoList.map((p) => (
                        <Button key={p.id} asChild variant="outline" className="w-full">
                          <a href={`/api/auth/sso/${encodeURIComponent(p.id)}/start?invite=${encodeURIComponent(token)}`}>
                            Accept with {p.name}
                          </a>
                        </Button>
                      ))}
                    </div>
                  </>
                )}
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
