"use client";

import { Switch as SwitchPrimitive } from "@base-ui/react/switch";

import { cn } from "@notion/lib/editor-utils";

import "@content-notion/components/primitives/switch/switch.scss";

function Switch({
  className,
  size = "default",
  ...props
}: SwitchPrimitive.Root.Props & {
  size?: "sm" | "default";
}) {
  return (
    <SwitchPrimitive.Root
      data-slot="tiptap-switch"
      data-size={size}
      className={(state) =>
        cn(
          "tiptap-switch",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="tiptap-switch-thumb"
        className="tiptap-switch-thumb"
      />
    </SwitchPrimitive.Root>
  );
}

export { Switch };
