import { Command as CommandPrimitive } from "cmdk";
import { SearchIcon } from "lucide-react";
import type { ComponentProps, ReactNode } from "react";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

export function Command({ className, ...props }: ComponentProps<typeof CommandPrimitive>) {
  return <CommandPrimitive data-slot="command" className={cn("flex size-full flex-col overflow-hidden bg-card text-fg", className)} {...props} />;
}

export function CommandDialog({ title, description, children, open, onOpenChange }: {
  title: string; description: string; children: ReactNode; open: boolean; onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent showClose={false} className="top-[12vh] max-w-lg translate-y-0 gap-0 overflow-hidden p-0">
        <DialogTitle className="sr-only">{title}</DialogTitle>
        <DialogDescription className="sr-only">{description}</DialogDescription>
        {children}
      </DialogContent>
    </Dialog>
  );
}

// CommandInput shows no focus ring: the palette opens focused on it (mockup review).
export function CommandInput({ className, right, ...props }: ComponentProps<typeof CommandPrimitive.Input> & { right?: ReactNode }) {
  return (
    <div data-slot="command-input-wrapper" className="flex items-center gap-2 border-b border-line px-3">
      <SearchIcon className="size-4 shrink-0 text-muted-fg" />
      <CommandPrimitive.Input
        data-slot="command-input"
        className={cn("h-11 min-w-0 flex-1 bg-transparent text-[14px] outline-none placeholder:text-muted-fg focus-visible:outline-none", className)}
        {...props}
      />
      {right}
    </div>
  );
}

export function CommandList({ className, ...props }: ComponentProps<typeof CommandPrimitive.List>) {
  return <CommandPrimitive.List data-slot="command-list" className={cn("scroll-thin max-h-80 overflow-y-auto p-1", className)} {...props} />;
}

export function CommandEmpty({ className, ...props }: ComponentProps<typeof CommandPrimitive.Empty>) {
  return <CommandPrimitive.Empty data-slot="command-empty" className={cn("px-3 py-6 text-center text-[13px] text-muted-fg", className)} {...props} />;
}

export function CommandGroup({ className, ...props }: ComponentProps<typeof CommandPrimitive.Group>) {
  return (
    <CommandPrimitive.Group
      data-slot="command-group"
      className={cn("[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:pt-2 [&_[cmdk-group-heading]]:pb-1 [&_[cmdk-group-heading]]:text-xs2 [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:tracking-wider [&_[cmdk-group-heading]]:text-muted-fg [&_[cmdk-group-heading]]:uppercase", className)}
      {...props}
    />
  );
}

export function CommandItem({ className, ...props }: ComponentProps<typeof CommandPrimitive.Item>) {
  return (
    <CommandPrimitive.Item
      data-slot="command-item"
      className={cn("flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-[13px] outline-none select-none data-[disabled=true]:pointer-events-none data-[disabled=true]:opacity-50 data-[selected=true]:bg-hover [&_svg]:shrink-0", className)}
      {...props}
    />
  );
}

export function CommandSeparator({ className, ...props }: ComponentProps<typeof CommandPrimitive.Separator>) {
  return <CommandPrimitive.Separator className={cn("-mx-1 h-px bg-line", className)} {...props} />;
}
