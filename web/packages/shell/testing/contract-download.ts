import { expect, type Download } from '@playwright/test';

/** Reads a saved contract and checks that every operation belongs to its entrance. */
export async function expectContractAudience(
  download: Download,
  audience: 'admin' | 'portal',
): Promise<void> {
  expect(await download.failure()).toBeNull();
  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const chunk of stream) chunks.push(chunk as Buffer);
  const contract = JSON.parse(Buffer.concat(chunks).toString('utf8')) as {
    paths: Record<string, Record<string, { 'x-audiences'?: string[] }>>;
  };
  const operations = Object.values(contract.paths).flatMap((item) =>
    Object.entries(item).filter(([key]) => key !== 'parameters'),
  );
  expect(operations.length).toBeGreaterThan(0);
  for (const [, operation] of operations) {
    expect(operation['x-audiences']).toContain(audience);
  }
}
