import { XIcon } from "lucide-react";
import { Dialog as SheetPrimitive } from "radix-ui";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export const Sheet = SheetPrimitive.Root;
export const SheetTrigger = SheetPrimitive.Trigger;
export const SheetClose = SheetPrimitive.Close;

const sides = {
  left: "inset-y-0 left-0 h-full w-64 border-r",
  right: "inset-y-0 right-0 h-full w-80 border-l",
  top: "inset-x-0 top-0 border-b",
  bottom: "inset-x-0 bottom-0 border-t",
};

export function SheetContent({ className, children, side = "left", showClose = true, ...props }:
  ComponentProps<typeof SheetPrimitive.Content> & { side?: keyof typeof sides; showClose?: boolean }) {
  return (
    <SheetPrimitive.Portal>
      <SheetPrimitive.Overlay className="fixed inset-0 z-50 bg-black/40" />
      <SheetPrimitive.Content
        data-slot="sheet-content"
        className={cn("fixed z-50 flex flex-col border-line bg-card text-fg shadow-xl outline-none", sides[side], className)}
        {...props}
      >
        {children}
        {showClose && (
          <SheetPrimitive.Close className="absolute top-3 right-3 grid size-8 cursor-pointer place-items-center rounded-md text-muted-fg hover:bg-hover hover:text-fg" aria-label="Close menu">
            <XIcon className="size-4" />
          </SheetPrimitive.Close>
        )}
      </SheetPrimitive.Content>
    </SheetPrimitive.Portal>
  );
}

export function SheetTitle({ className, ...props }: ComponentProps<typeof SheetPrimitive.Title>) {
  return <SheetPrimitive.Title className={cn("text-[15px] font-semibold", className)} {...props} />;
}

export function SheetDescription({ className, ...props }: ComponentProps<typeof SheetPrimitive.Description>) {
  return <SheetPrimitive.Description className={cn("text-[13px] text-muted-fg", className)} {...props} />;
}
