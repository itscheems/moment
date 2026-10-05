"use client";

import { cn } from "@notion/lib/editor-utils";
import "@content-notion/components/primitives/textarea/textarea.scss";

function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn("textarea", className)}
      {...props}
    />
  );
}

export { Textarea };
