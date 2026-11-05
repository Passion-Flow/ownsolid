import { cva, type VariantProps } from 'class-variance-authority';
import { Slot } from 'radix-ui';
import type { ComponentProps } from 'react';
import { cn } from '../cn.ts';

/** Button styles, exported for links that look like buttons. */
export const buttonVariants = cva(
  [
    'inline-flex shrink-0 cursor-pointer items-center justify-center gap-[7px] rounded-field border font-[550] whitespace-nowrap',
    'transition-[background-color,border-color,transform] duration-150 active:scale-[0.98]',
    'disabled:pointer-events-none disabled:opacity-55 [&_svg]:size-4 [&_svg]:shrink-0',
  ],
  {
    variants: {
      variant: {
        primary: 'border-accent-strong bg-accent-strong text-white hover:bg-accent-strong-hover',
        secondary: 'border-border-strong bg-surface text-text hover:bg-surface-2',
        ghost: 'border-transparent bg-transparent text-text-2 hover:bg-surface-2 hover:text-text',
        danger: 'border-border-strong bg-surface text-critical-ink hover:bg-surface-2',
      },
      size: {
        md: 'h-9 px-3.5',
        sm: 'h-[30px] rounded-control px-2.5 text-[13px]',
        icon: 'size-9 p-0 [&_svg]:size-[18px]',
      },
    },
    defaultVariants: { variant: 'secondary', size: 'md' },
  },
);

/** Props for {@link Button}. */
export interface ButtonProps extends ComponentProps<'button'>, VariantProps<typeof buttonVariants> {
  /** Renders the single child element with button styles instead of a `<button>`. */
  asChild?: boolean;
}

/** A button. Use `variant="primary"` for the one main action of a view. */
export function Button({ className, variant, size, asChild = false, type, ...props }: ButtonProps) {
  const classes = cn(buttonVariants({ variant, size }), className);
  if (asChild) return <Slot.Root className={classes} {...props} />;
  return <button type={type ?? 'button'} className={classes} {...props} />;
}
