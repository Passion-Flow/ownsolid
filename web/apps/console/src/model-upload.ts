// Sending a bundle to the server, one chunk at a time. It lives outside any page, so moving around
// the console does not stop it (docs/research/model-library.md).
import {
  ApiProblem,
  getModelImport,
  isProblem,
  uploadModelChunk,
  verifyModelImport,
  type ModelImport,
} from '@ownsolid/api';
import { useSyncExternalStore } from 'react';
import { canUploadImport } from './model-state.ts';

// A short outage passes within these; a longer one is the person's to see and retry.
const attemptsPerStep = 4;
const firstRetryMilliseconds = 1_000;

// Failures that say nothing about the bundle: another request held the import, or the server's
// offset moved on because an answer was lost.
const passing = new Set(['MODEL_IMPORT_BUSY', 'MODEL_CHUNK_OFFSET_MISMATCH']);

/** The calls a transfer makes, so a test can stand in for the server. */
export interface TransferCalls {
  read: typeof getModelImport;
  upload: typeof uploadModelChunk;
  verify: typeof verifyModelImport;
  wait: (milliseconds: number, signal: AbortSignal) => Promise<void>;
}

function pause(milliseconds: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(resolve, milliseconds);
    signal.addEventListener(
      'abort',
      () => {
        clearTimeout(timer);
        reject(signal.reason as Error);
      },
      { once: true },
    );
  });
}

const serverCalls: TransferCalls = {
  read: getModelImport,
  upload: uploadModelChunk,
  verify: verifyModelImport,
  wait: pause,
};

function recoverable(error: unknown): boolean {
  return isProblem(error) && (error.retryable || passing.has(error.code));
}

/** The chunk holding the last byte the server stored. */
function lastStoredChunk(uploaded: number, chunkBytes: number): number {
  return Math.floor((uploaded - 1) / chunkBytes) * chunkBytes;
}

/**
 * Sends what the server lacks of `file`, then asks it to verify the bundle. The server's offset
 * decides where to continue. A resumed upload first sends the last chunk the server holds again:
 * it accepts only the same bytes, so a different file is refused before any of it is stored.
 */
export async function sendBundle(
  file: Blob,
  target: ModelImport,
  chunkBytes: number,
  reason: string,
  signal: AbortSignal,
  onProgress: (sent: number) => void,
  calls: TransferCalls = serverCalls,
): Promise<void> {
  const requireOpen = (item: ModelImport) => {
    if (!canUploadImport(item))
      throw new ApiProblem('MODEL_IMPORT_STATE_CONFLICT', { status: 409 });
  };
  requireOpen(target);
  const retrying = async (step: () => Promise<void>) => {
    for (let attempt = 1; ; attempt += 1) {
      try {
        await step();
        return;
      } catch (error) {
        if (signal.aborted || !recoverable(error) || attempt === attemptsPerStep) throw error;
        await calls.wait(firstRetryMilliseconds * 2 ** (attempt - 1), signal);
      }
    }
  };
  const total = target.expected_bytes;
  let proven = target.uploaded_bytes === 0;
  let cursor = proven ? 0 : lastStoredChunk(target.uploaded_bytes, chunkBytes);
  let answered = true;
  while (cursor < total || !proven) {
    await retrying(async () => {
      // A failure may have stored the chunk with its answer lost, so ask rather than assume.
      if (!answered && proven) {
        const current = await calls.read(target.id, { signal });
        requireOpen(current);
        cursor = current.uploaded_bytes;
        if (cursor >= total) return;
      }
      answered = false;
      const end = Math.min(cursor + chunkBytes, total);
      const stored = await calls.upload(target.id, cursor, file.slice(cursor, end), { signal });
      cursor = stored.uploaded_bytes;
      proven = true;
      answered = true;
      onProgress(cursor);
    });
  }
  await retrying(async () => {
    await calls.verify(target.id, reason);
  });
}

/** How one upload stands. */
export interface Transfer {
  sent: number;
  total: number;
  state: 'sending' | 'finished' | 'failed';
  /** Why it stopped, when it failed. */
  problem: unknown;
  /** Sends the rest of the same file. */
  retry: () => void;
}

const transfers = new Map<string, Transfer>();
const stops = new Map<string, AbortController>();
const listeners = new Set<() => void>();

function publish(id: string, change: Partial<Transfer>) {
  const current = transfers.get(id);
  if (!current) return;
  transfers.set(id, { ...current, ...change });
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// Closing the tab ends the upload, so it asks first. It can be continued, but not unnoticed.
function warnBeforeLeaving(event: BeforeUnloadEvent) {
  if (stops.size > 0) event.preventDefault();
}

/** Starts sending `file`, replacing any earlier attempt for this import. */
export function startTransfer(
  file: Blob,
  target: ModelImport,
  chunkBytes: number,
  reason: string,
  onSettled: () => void,
): void {
  stopTransfer(target.id);
  if (!canUploadImport(target)) {
    onSettled();
    return;
  }
  const stop = new AbortController();
  stops.set(target.id, stop);
  window.addEventListener('beforeunload', warnBeforeLeaving);
  const retry = () => {
    void getModelImport(target.id)
      .then((latest) => {
        startTransfer(file, latest, chunkBytes, reason, onSettled);
      })
      .catch((problem: unknown) => {
        publish(target.id, { problem });
      });
  };
  transfers.set(target.id, {
    sent: target.uploaded_bytes,
    total: target.expected_bytes,
    state: 'sending',
    problem: null,
    retry,
  });
  for (const listener of listeners) listener();
  const settle = (change: Partial<Transfer>) => {
    if (stops.get(target.id) !== stop) return;
    stops.delete(target.id);
    if (stops.size === 0) window.removeEventListener('beforeunload', warnBeforeLeaving);
    publish(target.id, change);
    onSettled();
  };
  sendBundle(file, target, chunkBytes, reason, stop.signal, (sent) => {
    publish(target.id, { sent });
  }).then(
    () => {
      settle({ state: 'finished', sent: target.expected_bytes });
    },
    (problem: unknown) => {
      settle({ state: 'failed', problem });
    },
  );
}

/** Stops sending, and forgets the attempt. */
export function stopTransfer(id: string): void {
  const stop = stops.get(id);
  stops.delete(id);
  if (stops.size === 0) window.removeEventListener('beforeunload', warnBeforeLeaving);
  stop?.abort();
  if (transfers.delete(id)) for (const listener of listeners) listener();
}

/** The upload of this import in this tab, if there is one. */
export function useTransfer(id: string): Transfer | undefined {
  return useSyncExternalStore(subscribe, () => transfers.get(id));
}
