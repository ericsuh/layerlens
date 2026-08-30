import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import type { ComponentPropsWithoutRef, ReactNode } from "react";

/**
 * Radix tooltip, styled per DESIGN §2.3 (shadow-lg + border, max-width 480px).
 *
 * Radix positions its content with inline `style` attributes; the shipped CSP
 * allows that through `style-src-attr 'unsafe-inline'` (see DECISIONS,
 * "Phase 006"). `style-src` itself stays `'self'`, so no stylesheet or
 * <style> element can be injected.
 */
export const TooltipProvider = TooltipPrimitive.Provider;

export function Tooltip({
  content,
  children,
  side = "top",
  ...rest
}: {
  content: ReactNode;
  children: ReactNode;
  side?: TooltipPrimitive.TooltipContentProps["side"];
} & Omit<ComponentPropsWithoutRef<typeof TooltipPrimitive.Root>, "children">) {
  return (
    <TooltipPrimitive.Root {...rest}>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <TooltipPrimitive.Portal>
        <TooltipPrimitive.Content className="ll-overlay" side={side} sideOffset={6} collisionPadding={12}>
          {content}
        </TooltipPrimitive.Content>
      </TooltipPrimitive.Portal>
    </TooltipPrimitive.Root>
  );
}
