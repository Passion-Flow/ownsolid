import { ApiProblem, type ModelImport } from '@ownsolid/api';
import { describe, expect, it, vi } from 'vitest';
import { sendBundle, type TransferCalls } from './model-upload.ts';

const chunk = 4;

function target(uploaded: number, total: number): ModelImport {
  return {
    id: 'import',
    workspace_id: 'workspace',
    name: 'model',
    catalog_digest: 'digest',
    expected_bytes: total,
    uploaded_bytes: uploaded,
    state: 'uploading',
    error_code: null,
    model_id: null,
    attempts: 0,
    created_at: '',
    updated_at: '',
  };
}

/** A server holding `held` bytes, which refuses or loses what `script` says, call by call. */
function server(held: number, total: number, script: (ApiProblem | 'lost' | null)[] = []) {
  const state = { held };
  const sent: number[] = [];
  const calls: TransferCalls = {
    read: vi.fn(() => Promise.resolve(target(state.held, total))),
    upload: vi.fn(async (_: string, offset: number, body: Blob) => {
      sent.push(offset);
      await Promise.resolve();
      const step = script.shift() ?? null;
      if (step instanceof ApiProblem) throw step;
      if (offset === state.held) state.held += body.size;
      if (step === 'lost') throw new ApiProblem('NETWORK_UNAVAILABLE');
      return target(state.held, total);
    }),
    verify: vi.fn(() => Promise.resolve(target(state.held, total))),
    wait: vi.fn(() => Promise.resolve()),
  };
  return { calls, sent, state };
}

const send = (total: number, uploaded: number, calls: TransferCalls, progress = vi.fn()) =>
  sendBundle(
    new Blob([new Uint8Array(total)]),
    target(uploaded, total),
    chunk,
    'reason',
    new AbortController().signal,
    progress,
    calls,
  );

describe('sendBundle', () => {
  it('sends every chunk in order, the last one short, and asks for verification', async () => {
    const { calls, sent } = server(0, 10);
    const progress = vi.fn();
    await send(10, 0, calls, progress);
    expect(sent).toEqual([0, 4, 8]);
    expect(progress.mock.calls.map(([value]) => value as number)).toEqual([4, 8, 10]);
    expect(calls.verify).toHaveBeenCalledExactlyOnceWith('import', 'reason');
  });

  it('continues from the server after a lost answer, without sending a chunk twice', async () => {
    const { calls, sent } = server(0, 12, [null, 'lost']);
    await send(12, 0, calls);
    expect(sent).toEqual([0, 4, 8]);
    expect(calls.read).toHaveBeenCalledOnce();
  });

  it('proves a resumed file with the last stored chunk before sending more', async () => {
    const { calls, sent } = server(8, 12);
    await send(12, 8, calls);
    expect(sent).toEqual([4, 8]);
  });

  it('refuses a different file before any of it is stored', async () => {
    const { calls, sent, state } = server(8, 12, [
      new ApiProblem('MODEL_CHUNK_CONFLICT', { status: 409 }),
    ]);
    await expect(send(12, 8, calls)).rejects.toMatchObject({ code: 'MODEL_CHUNK_CONFLICT' });
    expect(sent).toEqual([4]);
    expect(state.held).toBe(8);
    expect(calls.verify).not.toHaveBeenCalled();
  });

  it('keeps proving after a passing failure rather than skipping the check', async () => {
    const { calls, sent } = server(8, 12, [new ApiProblem('NETWORK_UNAVAILABLE')]);
    await send(12, 8, calls);
    expect(sent).toEqual([4, 4, 8]);
  });

  it('proves a fully uploaded file, then asks for verification', async () => {
    const { calls, sent } = server(12, 12);
    await send(12, 12, calls);
    expect(sent).toEqual([8]);
    expect(calls.verify).toHaveBeenCalledOnce();
  });

  it('stops after a bounded number of tries and says why', async () => {
    const down = new ApiProblem('NETWORK_UNAVAILABLE');
    const { calls, sent } = server(0, 8, [down, down, down, down, down]);
    await expect(send(8, 0, calls)).rejects.toBe(down);
    expect(sent).toEqual([0, 0, 0, 0]);
    expect(vi.mocked(calls.wait).mock.calls.map(([delay]) => delay)).toEqual([1_000, 2_000, 4_000]);
  });

  it('does not retry a refusal that will not pass', async () => {
    const refused = new ApiProblem('MODEL_IMPORT_STATE_CONFLICT', { status: 409 });
    const { calls, sent } = server(0, 8, [refused]);
    await expect(send(8, 0, calls)).rejects.toBe(refused);
    expect(sent).toEqual([0]);
  });

  it('retries the request for verification too', async () => {
    const { calls } = server(0, 4);
    vi.mocked(calls.verify).mockRejectedValueOnce(new ApiProblem('NETWORK_UNAVAILABLE'));
    await send(4, 0, calls);
    expect(calls.verify).toHaveBeenCalledTimes(2);
  });

  it('stops at once when the person stops it', async () => {
    const { calls } = server(0, 8, [new ApiProblem('NETWORK_UNAVAILABLE')]);
    const stop = new AbortController();
    stop.abort();
    await expect(
      sendBundle(
        new Blob([new Uint8Array(8)]),
        target(0, 8),
        chunk,
        'r',
        stop.signal,
        vi.fn(),
        calls,
      ),
    ).rejects.toBeInstanceOf(ApiProblem);
    expect(calls.wait).not.toHaveBeenCalled();
  });
  it.each([
    { state: 'cancelled' as const, error_code: 'MODEL_IMPORT_EXPIRED' },
    { state: 'uploading' as const, error_code: 'MODEL_IMPORT_EXPIRY_PENDING' },
    { state: 'uploading' as const, error_code: 'MODEL_CANCELLATION_PENDING' },
  ])('does not send or verify a closed upload: $error_code', async (closed) => {
    const { calls } = server(4, 8);
    await expect(
      sendBundle(
        new Blob([new Uint8Array(8)]),
        { ...target(4, 8), ...closed },
        chunk,
        'reason',
        new AbortController().signal,
        vi.fn(),
        calls,
      ),
    ).rejects.toMatchObject({ code: 'MODEL_IMPORT_STATE_CONFLICT' });
    expect(calls.upload).not.toHaveBeenCalled();
    expect(calls.verify).not.toHaveBeenCalled();
    expect(calls.wait).not.toHaveBeenCalled();
  });

  it('does not resume an expired offset after a lost chunk response', async () => {
    const { calls, sent } = server(0, 12, ['lost']);
    vi.mocked(calls.read).mockResolvedValue({
      ...target(4, 12),
      state: 'cancelled',
      error_code: 'MODEL_IMPORT_EXPIRED',
    });
    await expect(send(12, 0, calls)).rejects.toMatchObject({ code: 'MODEL_IMPORT_STATE_CONFLICT' });
    expect(sent).toEqual([0]);
    expect(calls.read).toHaveBeenCalledOnce();
    expect(calls.verify).not.toHaveBeenCalled();
  });
});
