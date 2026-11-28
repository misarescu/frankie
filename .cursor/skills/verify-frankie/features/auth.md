# Sign-in, roles, tokens and users

Every route and API endpoint requires a signed-in person (D144-D147): unauthenticated visits redirect to `/login?next=...`, users manage their profile, password, linked SSO identities and personal API tokens on `/settings/account`, and administrators manage users and single-use invites on `/settings/users`. Viewers can read everything but cannot perform operational writes or access `/settings/users`.

## Sub-features

- Unauthenticated gate: visiting any app path redirects to `/login?next=...`; `/api/*` reads and writes return 401.
- Password sign-in and sign-out: `/login` authenticates with username or email + password, sets an `HttpOnly` session cookie, and returns to `next`; `Sign out` in the sidebar footer menu invalidates the session.
- Account & API tokens (`/settings/account`): update display name and email, change password, view/link/unlink SSO providers, create personal API tokens (`frk_...`, shown once) and revoke them.
- Users & invites (`/settings/users`, admin only): list users, create user, change role (last-admin demotion/disable returns 409), revoke sessions, disable/enable user, create and revoke single-use invite links.
- Invite acceptance (`/invite/<token>`): create a new account from an invite link and land signed in with the invited role.
- Viewer role enforcement: signed-in viewer sees read-only board/issue/approvals/run pages (no Start/Cancel/Approve/Steer buttons) and gets 403 on write API endpoints and `/api/users`.

## How to get to it (user POV)

- Unauthenticated: open `/` or `/projects/vfy/board` -> redirected to `/login`.
- Signed in: sidebar `Settings` section (`Account`, and `Users` for admins) or the sidebar footer `Account menu`.
- Invite link: open `/invite/<token>` copied from `/settings/users`.

## Driving it with browser.mjs

```bash
export FRANKIE_VERIFY_FEATURE=auth
eval "$($S/frankie-verify env)"
B="node $S/browser.mjs"

# 1. Unauthenticated redirect + password sign-in
$B goto /projects/vfy/board
$B expect --text "Sign in"
$B fill --label "Username or email" --value "$ADMIN_USER"
$B fill --label "Password" --value "$ADMIN_PASSWORD"
$B click --role button --name "Sign in"
$B expect --text "Board"
$B expect --text "Admin"

# 2. Personal API token creation on /settings/account
$B goto /settings/account
$B expect --text "Account & API tokens"
$B click --role button --name "New token"
$B fill --label "Token name" --value "verify-cli"
$B click --role button --name "Create token"
$B expect --text "Copy your new API token"
$B click --role button --name "Done"
$B expect --text "verify-cli"

# 3. Admin user creation + viewer role check on /settings/users
$B goto /settings/users
$B expect --text "Users & invites"
$B click --role button --name "Add user"
$B fill --label "Username" --value "alice-viewer"
$B fill --label "Display name" --value "Alice Viewer"
$B fill --label "Initial password" --value "viewer secret password"
$B click --role button --name "Create user"
$B expect --text "alice-viewer"

# 4. Sign out
$B click --role button --name "Account menu"
$B click --role menuitem --name "Sign out"
$B expect --text "Sign in"
```

## Gotchas

- `frankie-verify launch` starts the control plane without SSO credentials by default, so SSO provider buttons are hidden on `/login` unless configured; OIDC/GitHub OAuth flows are covered by `internal/auth/sso_test.go` and `internal/api/auth_test.go`.
- Password inputs in `browser.mjs` should be filled via `--label` so credentials stay clean in logs; never paste passwords into PR descriptions or reports.
