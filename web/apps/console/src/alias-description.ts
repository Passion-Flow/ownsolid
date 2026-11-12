import type { ModelAlias } from '@ownsolid/api';

/** An alias served by a version that includes the paired catalogue fields. */
export type DescribedAlias = ModelAlias & { description: string; description_revision: number };

/** Predecessor servers omit both fields; never invent a revision for a write. */
export function hasAliasDescription(alias: ModelAlias): alias is DescribedAlias {
  return typeof alias.description === 'string' && typeof alias.description_revision === 'number';
}

/** Matches the server's CRLF/Unicode-space normalization and Unicode code-point limit. */
export function descriptionInput(draft: string): {
  text: string;
  length: number;
  problem: 'invalid' | 'tooLong' | null;
} {
  const lines = draft.replaceAll('\r\n', '\n');
  const text = lines.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '');
  const length = Array.from(text).length;
  const invalid = /\p{Cs}/u.test(lines) || /\p{Cc}/u.test(lines.replace(/[\n\t]/g, ''));
  return { text, length, problem: invalid ? 'invalid' : length > 500 ? 'tooLong' : null };
}
