import { cn } from "@/shared/ui/cn";
import type { InputHTMLAttributes } from "react";

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={cn(
        "w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-text outline-none focus:border-accent",
        className,
      )}
      {...props}
    />
  );
}
