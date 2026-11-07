// What the console calls a recorded action and its target. An event stores machine identifiers,
// which stay available in the details; the list reads as words in the person's language. An
// action of a module with no page yet shows its family and its identifier (UX-108).
import { useI18n } from '@ownsolid/i18n';

/** Reads the words for an event's action and target, or nothing when none are known. */
export function useAuditNames() {
  const { t, knows } = useI18n();
  const family = (action: string): string | undefined => {
    const key = `audit.category.${action.split('.')[0] ?? ''}`;
    return knows(key) ? t(key) : undefined;
  };
  return {
    action(action: string): string | undefined {
      const key = `audit.action.${action}`;
      return knows(key) ? t(key) : undefined;
    },
    family,
    target(type: string): string | undefined {
      const key = `audit.target.${type}`;
      return knows(key) ? t(key) : undefined;
    },
    /** One line for an action, for places that take text: a title, an accessible name. */
    actionText(action: string): string {
      const key = `audit.action.${action}`;
      if (knows(key)) return t(key);
      const name = family(action);
      return name ? `${name} ${action}` : action;
    },
  };
}

const identifier = 'font-mono text-[12.5px]';

/** An event's action: its name, or its family and identifier when it has no name yet. */
export function ActionName({ action }: { action: string }) {
  const names = useAuditNames();
  const name = names.action(action);
  if (name) return <>{name}</>;
  const family = names.family(action);
  return (
    <>
      {family ? <span className="mr-1.5">{family}</span> : null}
      <code className={identifier}>{action}</code>
    </>
  );
}

/** An event's target type: its name, or its identifier when it has none. */
export function TargetName({ type }: { type: string }) {
  const name = useAuditNames().target(type);
  return name ? <>{name}</> : <code className={identifier}>{type}</code>;
}
