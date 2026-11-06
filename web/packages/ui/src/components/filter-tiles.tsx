import { cn } from '../cn.ts';

/** One tile of {@link FilterTiles}. */
export interface FilterTile<T extends string> {
  value: T;
  label: string;
  /** How many are in it; absent until it is known, or when it cannot be read. */
  count?: number | undefined;
  /** A count above zero is something to look at. */
  attention?: boolean;
}

/** Props for {@link FilterTiles}. */
export interface FilterTilesProps<T extends string> {
  /** Accessible name of the group. */
  label: string;
  value: T;
  tiles: readonly FilterTile<T>[];
  onChange: (value: T) => void;
}

/**
 * The summary above a list, where each figure is also the filter that shows those rows. The chosen tile
 * is marked by a thicker border and heavier text as well as by colour.
 */
export function FilterTiles<T extends string>({
  label,
  value,
  tiles,
  onChange,
}: FilterTilesProps<T>) {
  return (
    <div role="group" aria-label={label} className="mb-3.5 grid grid-cols-2 gap-3 lg:grid-cols-4">
      {tiles.map((tile) => {
        const chosen = tile.value === value;
        return (
          <button
            key={tile.value}
            type="button"
            aria-pressed={chosen}
            onClick={() => {
              onChange(tile.value);
            }}
            className={cn(
              'block cursor-pointer rounded-card border bg-surface px-[18px] py-3 text-left shadow-1 transition-colors duration-150 hover:border-accent',
              chosen
                ? 'border-accent bg-accent-soft shadow-[inset_0_0_0_1px_var(--accent)]'
                : 'border-border',
            )}
          >
            <span
              className={cn(
                'block text-[13px] text-text-2',
                chosen ? 'font-bold text-text' : 'font-medium',
              )}
            >
              {tile.label}
            </span>
            <span
              className={cn(
                'mt-1 block min-h-[31px] text-[24px] leading-[1.25] font-bold tracking-[-0.03em] tabular-nums',
                tile.attention && tile.count ? 'text-critical' : 'text-text',
              )}
            >
              {tile.count}
            </span>
          </button>
        );
      })}
    </div>
  );
}
