import { XIcon } from "lucide-react";
import { Dialog as DialogPrimitive } from "radix-ui";
import { useRef, type ComponentProps } from "react";
import { cn } from "@/lib/utils";

export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;
export const DialogClose = DialogPrimitive.Close;
export const DialogPortal = DialogPrimitive.Portal;

type AutoFocus = { onOpenAutoFocus?: (e: Event) => void; onCloseAutoFocus?: (e: Event) => void };

// useReturnFocus gives focus back to what had it when a dialog opened, as
// Radix does only for a dialog opened by its own Trigger; ours open from
// state (a button's onClick, a 401), and focus would otherwise fall to the
// page. When that is gone (a menu item), the dialog's own onCloseAutoFocus
// decides.
export function useReturnFocus({ onOpenAutoFocus, onCloseAutoFocus }: AutoFocus): Required<AutoFocus> {
  const opener = useRef<Element | null>(null);
  return {
    onOpenAutoFocus: (e) => { opener.current = document.activeElement; onOpenAutoFocus?.(e); },
    onCloseAutoFocus: (e) => {
      const el = opener.current;
      if (el instanceof HTMLElement && el.isConnected && el !== document.body) {
        e.preventDefault();
        el.focus();
        return;
      }
      onCloseAutoFocus?.(e);
    },
  };
}

export function DialogOverlay({ className, ...props }: ComponentProps<typeof DialogPrimitive.Overlay>) {
  return <DialogPrimitive.Overlay data-slot="dialog-overlay" className={cn("fixed inset-0 z-50 bg-black/40", className)} {...props} />;
}

export function DialogContent({ className, children, showClose = true, onOpenAutoFocus, onCloseAutoFocus, ...props }: ComponentProps<typeof DialogPrimitive.Content> & { showClose?: boolean }) {
  const focus = useReturnFocus({ onOpenAutoFocus, onCloseAutoFocus });
  return (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        className={cn(
          "fixed top-1/2 left-1/2 z-50 grid max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 gap-4 overflow-y-auto rounded-xl border border-line bg-card p-5 text-fg shadow-2xl outline-none",
          className,
        )}
        {...props}
        {...focus}
      >
        {children}
        {showClose && (
          <DialogPrimitive.Close className="absolute top-3 right-3 grid size-7 cursor-pointer place-items-center rounded-md text-muted-fg hover:bg-hover hover:text-fg" aria-label="Close">
            <XIcon className="size-4" />
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Content>
    </DialogPortal>
  );
}

export function DialogHeader({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="dialog-header" className={cn("flex flex-col gap-1 pr-6", className)} {...props} />;
}

export function DialogFooter({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="dialog-footer" className={cn("flex flex-col-reverse gap-2 sm:flex-row sm:justify-end", className)} {...props} />;
}

export function DialogTitle({ className, ...props }: ComponentProps<typeof DialogPrimitive.Title>) {
  return <DialogPrimitive.Title data-slot="dialog-title" className={cn("text-[15px] font-semibold", className)} {...props} />;
}

export function DialogDescription({ className, ...props }: ComponentProps<typeof DialogPrimitive.Description>) {
  return <DialogPrimitive.Description data-slot="dialog-description" className={cn("text-[13px] text-muted-fg", className)} {...props} />;
}
