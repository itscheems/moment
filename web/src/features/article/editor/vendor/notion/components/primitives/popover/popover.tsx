"use client";

import * as PopoverPrimitive from "@radix-ui/react-popover";
import { cn } from "@notion/lib/editor-utils";
import "@content-notion/components/primitives/popover/popover.scss";

function Popover({
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Root>) {
  return <PopoverPrimitive.Root {...props} />;
}

function PopoverTrigger({
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Trigger>) {
  return <PopoverPrimitive.Trigger {...props} />;
}

function PopoverContent({
  className,
  align = "center",
  sideOffset = 4,
  fade = false,
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Content> & { fade?: boolean }) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        align={align}
        sideOffset={sideOffset}
        className={cn("tiptap-popover", className)}
        data-fade={fade || undefined}
        {...props}
      />
    </PopoverPrimitive.Portal>
  );
}

export { Popover, PopoverTrigger, PopoverContent };
