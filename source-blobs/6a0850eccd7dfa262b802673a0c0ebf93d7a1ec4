import type { ReactNode } from 'react';

/** Name and value pairs in a drawer or a page. */
export function DetailList({ children }: { children: ReactNode }) {
  return <dl className="m-0 grid grid-cols-[130px_1fr] gap-y-2.5 text-[13.5px]">{children}</dl>;
}

/** One pair in a {@link DetailList}. */
export function Detail({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-text-3">{label}</dt>
      <dd className="m-0 min-w-0 break-words">{children}</dd>
    </>
  );
}
