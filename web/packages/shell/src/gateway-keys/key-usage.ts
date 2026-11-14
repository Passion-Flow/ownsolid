// The address and the sample calls that show a person how to use a key. Every sample reads the key
// from the OWNSOLID_KEY environment variable, so the secret is never written into code or shown on
// a page after it was issued (docs/research/portal-api-keys.md).

/** The languages a sample call is offered in. */
export const sampleLanguages = ['curl', 'python', 'node'] as const;

export type SampleLanguage = (typeof sampleLanguages)[number];

/** The base URL an OpenAI-compatible client takes. */
export function gatewayBaseUrl(origin: string): string {
  return `${origin}/v1`;
}

/** A call that works as typed once `OWNSOLID_KEY` holds a key for the name. */
export function sampleCall(language: SampleLanguage, origin: string, alias: string): string {
  const baseUrl = gatewayBaseUrl(origin);
  if (language === 'python') {
    return [
      'import os',
      'from openai import OpenAI',
      '',
      `client = OpenAI(base_url="${baseUrl}", api_key=os.environ["OWNSOLID_KEY"])`,
      'answer = client.chat.completions.create(',
      `    model="${alias}",`,
      '    messages=[{"role": "user", "content": "Hello"}],',
      ')',
      'print(answer.choices[0].message.content)',
    ].join('\n');
  }
  if (language === 'node') {
    return [
      'import OpenAI from "openai";',
      '',
      `const client = new OpenAI({ baseURL: "${baseUrl}", apiKey: process.env.OWNSOLID_KEY });`,
      'const answer = await client.chat.completions.create({',
      `  model: "${alias}",`,
      '  messages: [{ role: "user", content: "Hello" }],',
      '});',
      'console.log(answer.choices[0].message.content);',
    ].join('\n');
  }
  const body = JSON.stringify({ model: alias, messages: [{ role: 'user', content: 'Hello' }] });
  return [
    `curl ${baseUrl}/chat/completions \\`,
    '  -H "Authorization: Bearer $OWNSOLID_KEY" \\',
    '  -H "Content-Type: application/json" \\',
    `  -d '${body}'`,
  ].join('\n');
}
