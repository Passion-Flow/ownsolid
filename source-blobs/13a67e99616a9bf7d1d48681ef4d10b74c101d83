import { describe, expect, it } from 'vitest';
import { descriptionInput } from './alias-description.ts';

describe('descriptionInput', () => {
  it('counts supplementary characters once and trims the server whitespace set', () => {
    const value = '\u{1f680}'.repeat(500);
    expect(descriptionInput(`\u2003 ${value}\r\n`)).toEqual({
      text: value,
      length: 500,
      problem: null,
    });
    expect(descriptionInput(`${value}x`).problem).toBe('tooLong');
    // ECMAScript trim removes BOM; Go TrimSpace preserves it as catalogue content.
    expect(descriptionInput('\ufeff').length).toBe(1);
  });

  it('allows internal line feeds and tabs, and refuses malformed or control input before trimming', () => {
    expect(descriptionInput(' one\r\n\ttwo ').text).toBe('one\n\ttwo');
    for (const value of ['\ud800', '\u0000', '\u0085', '\r', 'text\u007f']) {
      expect(descriptionInput(value).problem).toBe('invalid');
    }
    expect(descriptionInput(' \r\n\t ').text).toBe('');
  });
});
