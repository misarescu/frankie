import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

// cn joins class names, later Tailwind classes winning over earlier ones.
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
