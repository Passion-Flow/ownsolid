import { describe, expect, it } from 'vitest';
import { gatewayBaseUrl, sampleCall, sampleLanguages } from './key-usage.ts';

const origin = 'https://gateway.lab.internal';
const alias = 'qwen3-chat';

describe('how a key is used', () => {
  it('gives the base URL an OpenAI-compatible client takes', () => {
    expect(gatewayBaseUrl(origin)).toBe('https://gateway.lab.internal/v1');
  });

  it('names the model and the address in every language', () => {
    for (const language of sampleLanguages) {
      const call = sampleCall(language, origin, alias);
      expect(call, language).toContain('https://gateway.lab.internal/v1');
      expect(call, language).toContain(alias);
    }
  });

  it('reads the key from the environment and never carries a secret', () => {
    expect(sampleCall('curl', origin, alias)).toContain('Bearer $OWNSOLID_KEY');
    expect(sampleCall('python', origin, alias)).toContain('os.environ["OWNSOLID_KEY"]');
    expect(sampleCall('node', origin, alias)).toContain('process.env.OWNSOLID_KEY');
    for (const language of sampleLanguages) {
      expect(sampleCall(language, origin, alias), language).not.toContain('pk1.');
    }
  });

  it('calls the chat completions route with the alias as the model', () => {
    const curl = sampleCall('curl', origin, alias);
    expect(curl).toContain('/v1/chat/completions');
    expect(curl).toContain('"model":"qwen3-chat"');
    expect(sampleCall('python', origin, alias)).toContain('model="qwen3-chat"');
    expect(sampleCall('node', origin, alias)).toContain('model: "qwen3-chat"');
  });
});
