import { useQuery } from "@tanstack/react-query";
import { getRouteApi, useNavigate } from "@tanstack/react-router";
import { LogInIcon } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { useLogin } from "@/api/mutations";
import { meQuery, providersQuery } from "@/api/queries";
import { safeNextPath, ssoErrorMessage } from "@/auth";
import { WriteError } from "@/components/app/states";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

const route = getRouteApi("/login");

export function LoginPage() {
  const search = route.useSearch();
  const navigate = useNavigate();
  const next = safeNextPath(search.next);
  const ssoError = ssoErrorMessage(search.error);
  const me = useQuery(meQuery());
  const providers = useQuery(providersQuery());
  const login = useLogin();

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  useEffect(() => {
    if (me.data) {
      void navigate({ href: next, replace: true });
    }
  }, [me.data, navigate, next]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    login.mutate({ username: username.trim(), password }, {
      onSuccess: () => void navigate({ href: next, replace: true }),
    });
  };

  const ssoList = providers.data?.sso ?? [];

  return (
    <div className="flex min-h-dvh items-center justify-center bg-bg px-4 py-10">
      <div className="w-full max-w-sm space-y-5">
        <div className="flex items-center justify-center gap-2.5">
          <span className="grid size-8 place-items-center rounded-md bg-primary text-sm font-bold text-primary-fg">F</span>
          <span className="text-lg font-semibold tracking-tight">Frankie</span>
        </div>

        <Card>
          <CardHeader className="flex-col items-start gap-1">
            <CardTitle>Sign in</CardTitle>
            <CardDescription>Use your Frankie account to view runs and approve work.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {ssoError && <Alert tone="bad"><p>{ssoError}</p></Alert>}

            <form onSubmit={submit} className="grid gap-3">
              <label className="grid gap-1.5 text-[13px] font-medium">
                Username or email
                <Input
                  autoFocus
                  autoComplete="username"
                  value={username}
                  disabled={login.isPending}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder="admin"
                />
              </label>
              <label className="grid gap-1.5 text-[13px] font-medium">
                Password
                <Input
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  disabled={login.isPending}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </label>
              <WriteError error={login.error} what="sign in" />
              <Button type="submit" className="w-full" disabled={login.isPending || username.trim() === "" || password === ""}>
                <LogInIcon className="size-3.5" />
                {login.isPending ? "Signing in…" : "Sign in"}
              </Button>
            </form>

            {ssoList.length > 0 && (
              <>
                <div className="relative flex items-center py-1">
                  <div className="flex-1 border-t border-line" />
                  <span className="px-2.5 text-xs text-muted-fg">or continue with</span>
                  <div className="flex-1 border-t border-line" />
                </div>
                <div className="grid gap-2">
                  {ssoList.map((p) => (
                    <Button key={p.id} asChild variant="outline" className="w-full">
                      <a href={`/api/auth/sso/${encodeURIComponent(p.id)}/start?next=${encodeURIComponent(next)}`}>
                        Continue with {p.name}
                      </a>
                    </Button>
                  ))}
                </div>
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
