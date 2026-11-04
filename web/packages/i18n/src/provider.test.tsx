import { act, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import chinese from '../locales/zh-cn.json';
import { I18nProvider, useI18n } from './provider.tsx';

function Probe() {
  const { t, locale, setLocale } = useI18n();
  return (
    <button
      type="button"
      onClick={() => {
        setLocale(locale === 'en' ? 'zh-CN' : 'en');
      }}
    >
      {t('signIn.title')}
    </button>
  );
}

afterEach(() => {
  localStorage.clear();
});

describe('I18nProvider', () => {
  it('renders English at once and switches language on demand', async () => {
    render(
      <I18nProvider locale="en">
        <Probe />
      </I18nProvider>,
    );
    const button = screen.getByRole('button');
    expect(button).toHaveTextContent('Sign in');
    expect(document.documentElement.lang).toBe('en');
    act(() => {
      button.click();
    });
    expect(
      await screen.findByRole('button', { name: chinese['signIn.title'] }),
    ).toBeInTheDocument();
    expect(document.documentElement.lang).toBe('zh-CN');
    expect(localStorage.getItem('ownsolid.locale')).toBe('zh-CN');
  });

  it('starts with the stored choice', async () => {
    localStorage.setItem('ownsolid.locale', 'zh-CN');
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>,
    );
    expect(
      await screen.findByRole('button', { name: chinese['signIn.title'] }),
    ).toBeInTheDocument();
  });
});
