// Saves theme and density changes to the account, one request at a time.
import { isProblem, type Preference, type PreferenceChange } from '@ownsolid/api';

/** What {@link createPreferenceSync} needs. */
export interface PreferenceSyncOptions {
  /** The last preference read from the server, if any. */
  known: () => Preference | undefined;
  /** Reads the preference from the server. */
  load: () => Promise<Preference>;
  /** Saves a change on top of a revision. */
  save: (change: PreferenceChange, revision: number) => Promise<Preference>;
  onSaved: (saved: Preference) => void;
  /** A save failed. `retry` tries again; the change also goes out with the next one. */
  onFailed: (error: unknown, retry: () => void) => void;
}

/** The queue {@link createPreferenceSync} returns. */
export interface PreferenceSync {
  /** Saves a change, merged with any change still waiting. */
  save: (change: PreferenceChange) => void;
  /** True while a change is unsaved, so a read from the server must not overwrite it. */
  busy: () => boolean;
}

/**
 * Creates the save queue. Every save carries the revision it was based on, so saves run
 * one at a time and changes made meanwhile are merged into the next request. When another
 * browser saved first, the change is applied on top of the newer revision: it names only
 * what the user just chose, and that is the latest intent.
 */
export function createPreferenceSync(options: PreferenceSyncOptions): PreferenceSync {
  let waiting: PreferenceChange = {};
  let running = false;
  const hasWaiting = () => Object.keys(waiting).length > 0;

  async function send(change: PreferenceChange): Promise<Preference> {
    const base = options.known() ?? (await options.load());
    try {
      return await options.save(change, base.revision);
    } catch (error) {
      if (!isProblem(error, 'PREFERENCE_REVISION_CONFLICT')) throw error;
      return options.save(change, (await options.load()).revision);
    }
  }

  async function drain(): Promise<void> {
    if (running) return;
    running = true;
    while (hasWaiting()) {
      const change = waiting;
      waiting = {};
      try {
        options.onSaved(await send(change));
      } catch (error) {
        waiting = { ...change, ...waiting };
        running = false;
        options.onFailed(error, () => void drain());
        return;
      }
    }
    running = false;
  }

  return {
    save: (change) => {
      waiting = { ...waiting, ...change };
      void drain();
    },
    busy: () => running || hasWaiting(),
  };
}
