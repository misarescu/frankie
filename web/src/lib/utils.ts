import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

// The theme's own font size (index.css, --text-xs2) is a size, not a color,
// so text-xs2 and text-muted-fg both stay.
const twMerge = extendTailwindMerge({ extend: { theme: { text: ["xs2"] } } });

// cn joins class names, later Tailwind classes winning over earlier ones.
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
