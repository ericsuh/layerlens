import * as PopoverPrimitive from "@radix-ui/react-popover";
import type { ReactNode } from "react";

/**
 * Radix popover, styled per DESIGN §2.3. Used for the raw-instruction body and
 * the could-be-shared explanation — both of which must escape the layer
 * column's own scroll container, which is why this portals rather than
 * rendering in place.
 */
export function Popover({
  trigger,
  children,
  label,
  side = "right",
  align = "start",
  open,
  onOpenChange,
}: {
  trigger: ReactNode;
  children: ReactNode;
  label: string;
  side?: PopoverPrimitive.PopoverContentProps["side"];
  align?: PopoverPrimitive.PopoverContentProps["align"];
  /** Optional controlled state, for menus that close on their own selection. */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  return (
    <PopoverPrimitive.Root
      {...(open === undefined ? {} : { open })}
      {...(onOpenChange === undefined ? {} : { onOpenChange })}
    >
      <PopoverPrimitive.Trigger asChild>{trigger}</PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          className="ll-overlay"
          side={side}
          align={align}
          sideOffset={8}
          collisionPadding={12}
          aria-label={label}
        >
          {children}
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  );
}
