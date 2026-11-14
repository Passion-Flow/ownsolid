// Dropdown menus on Radix primitives. Menus run non-modal: the modal mode injects a
// <style> element for its scroll lock, which the Content-Security-Policy refuses.
import { Check } from 'lucide-react';
import { DropdownMenu } from 'radix-ui';
import type { ComponentProps, ReactNode } from 'react';
import { cn } from '../cn.ts';

/** Props for {@link Menu}. */
export interface MenuProps {
  /** The element that opens the menu; it must accept a ref, such as a button. */
  trigger: ReactNode;
  children: ReactNode;
  align?: 'start' | 'end' | undefined;
  className?: string | undefined;
}

/** A menu opened from a button. */
export function Menu({ trigger, children, align = 'end', className }: MenuProps) {
  return (
    <DropdownMenu.Root modal={false}>
      <DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align={align}
          sideOffset={6}
          className={cn(
            'z-30 min-w-[200px] rounded-[12px] border border-border bg-surface p-1.5 text-text shadow-2',
            className,
          )}
        >
          {children}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

/** One action in a {@link Menu}. */
export function MenuItem({ className, ...props }: ComponentProps<typeof DropdownMenu.Item>) {
  return (
    <DropdownMenu.Item
      className={cn(
        'flex h-[34px] cursor-pointer items-center gap-2.5 rounded-control px-2.5 text-text-2 outline-none',
        'data-[disabled]:pointer-events-none data-[disabled]:opacity-50',
        'data-[highlighted]:bg-accent-soft data-[highlighted]:text-text',
        className,
      )}
      {...props}
    />
  );
}

/** A choice that stays on screen when toggled, so several can change in one visit. */
export function MenuCheckboxItem({
  className,
  children,
  ...props
}: ComponentProps<typeof DropdownMenu.CheckboxItem>) {
  return (
    <DropdownMenu.CheckboxItem
      className={cn(
        'flex h-[34px] cursor-pointer items-center gap-2.5 rounded-control px-2.5 text-text-2 outline-none',
        'data-[disabled]:pointer-events-none data-[disabled]:opacity-50',
        'data-[highlighted]:bg-accent-soft data-[highlighted]:text-text',
        className,
      )}
      onSelect={(event) => {
        event.preventDefault();
      }}
      {...props}
    >
      <span className="flex-1">{children}</span>
      <DropdownMenu.ItemIndicator>
        <Check aria-hidden className="size-4 text-accent" />
      </DropdownMenu.ItemIndicator>
    </DropdownMenu.CheckboxItem>
  );
}

/** A group heading in a {@link Menu}. */
export function MenuLabel({ className, ...props }: ComponentProps<typeof DropdownMenu.Label>) {
  return (
    <DropdownMenu.Label
      className={cn('px-2.5 py-1 text-[11.5px] font-semibold text-text-3', className)}
      {...props}
    />
  );
}

/** A line between groups in a {@link Menu}. */
export function MenuSeparator() {
  return <DropdownMenu.Separator className="my-1 h-px bg-border" />;
}
