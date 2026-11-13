import { useEffect, useRef, useState } from 'react';

/** Returns focus to a command menu after its native dialog has closed. */
export function useCommandDialog<T extends string>() {
  const [dialog, setDialog] = useState<T | null>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const restore = useRef(false);
  const close = () => {
    restore.current = true;
    setDialog(null);
  };
  useEffect(() => {
    if (dialog === null && restore.current) {
      restore.current = false;
      trigger.current?.focus();
    }
  }, [dialog]);
  return { dialog, setDialog, trigger, close };
}
