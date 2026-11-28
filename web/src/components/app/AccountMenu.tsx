import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ChevronsUpDownIcon, KeyRoundIcon, LogOutIcon, UserIcon, UsersIcon } from "lucide-react";
import { useLogout } from "@/api/mutations";
import { meQuery } from "@/api/queries";
import { displayUserName, isAdmin, roleLabel } from "@/auth";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { toast } from "@/components/ui/toast";
import { paths } from "@/paths";

// AccountMenu sits at the foot of the sidebar (D147): shows the signed-in
// person and role, links to Account & API tokens and (for admins) Users, and
// signs out.
export function AccountMenu({ onNavigate }: { onNavigate?: () => void }) {
  const me = useQuery(meQuery());
  const logout = useLogout();
  const navigate = useNavigate();
  const user = me.data;
  if (!user) return null;

  const go = (href: string) => {
    onNavigate?.();
    void navigate({ href });
  };

  const signOut = () => {
    logout.mutate(undefined, {
      onSuccess: () => {
        onNavigate?.();
        toast("Signed out.");
        void navigate({ href: paths.login(), replace: true });
      },
    });
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className="flex w-full cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-2 text-left hover:bg-hover"
        aria-label="Account menu"
      >
        <span className="grid size-6 shrink-0 place-items-center rounded-full bg-muted text-muted-fg">
          <UserIcon className="size-3.5" />
        </span>
        <span className="min-w-0 flex-1 text-xs">
          <span className="block truncate font-medium">{displayUserName(user)}</span>
          <span className="block truncate text-muted-fg">{roleLabel(user.role)}</span>
        </span>
        <ChevronsUpDownIcon className="size-3.5 shrink-0 text-muted-fg" />
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="w-56">
        <DropdownMenuItem onSelect={() => go(paths.account())}>
          <KeyRoundIcon className="size-4" />Account & API tokens
        </DropdownMenuItem>
        {isAdmin(user.role) && (
          <DropdownMenuItem onSelect={() => go(paths.users())}>
            <UsersIcon className="size-4" />Users & invites
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem disabled={logout.isPending} onSelect={signOut}>
          <LogOutIcon className="size-4" />Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
