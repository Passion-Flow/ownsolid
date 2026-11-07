// The SHA-256 of some bytes, as lowercase hex. Web Crypto exists only in secure contexts, and the
// control plane also has an explicit plain-HTTP transport, so a library covers that one case; it
// loads only there (docs/research/model-library.md).

function hex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}

/** Hashes `bytes`. */
export async function sha256Hex(bytes: ArrayBuffer): Promise<string> {
  const subtle = globalThis.crypto.subtle as SubtleCrypto | undefined;
  if (subtle) return hex(new Uint8Array(await subtle.digest('SHA-256', bytes)));
  const { sha256 } = await import('@noble/hashes/sha2');
  return hex(sha256(new Uint8Array(bytes)));
}
