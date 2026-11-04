import { I18nProvider } from '@ownsolid/i18n';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { Button } from './button.tsx';
import { Notice } from './notice.tsx';
import { ProblemNotice } from './problem-notice.tsx';
import { TextField } from './text-field.tsx';

const inEnglish = (node: ReactNode) => render(<I18nProvider locale="en">{node}</I18nProvider>);

describe('Button', () => {
  it('defaults to type button so it never submits a form by accident', () => {
    render(<Button>Save</Button>);
    expect(screen.getByRole('button', { name: 'Save' })).toHaveAttribute('type', 'button');
  });

  it('renders its child as the element with asChild', () => {
    render(
      <Button asChild>
        <a href="/nodes">Nodes</a>
      </Button>,
    );
    expect(screen.getByRole('link', { name: 'Nodes' })).toHaveClass('inline-flex');
  });
});

describe('TextField', () => {
  it('links the label, hint and error to the input', () => {
    render(<TextField label="Name" hint="Shown to others" error="Required" />);
    const input = screen.getByLabelText('Name');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription('Required Shown to others');
  });

  it('is not marked invalid without an error', () => {
    render(<TextField label="Name" />);
    expect(screen.getByLabelText('Name')).not.toHaveAttribute('aria-invalid');
  });
});

describe('Notice', () => {
  it('announces critical messages as alerts and others as status', () => {
    const { rerender } = render(<Notice tone="critical" title="Failed" />);
    expect(screen.getByRole('alert')).toHaveTextContent('Failed');
    rerender(<Notice title="Saved" />);
    expect(screen.getByRole('status')).toHaveTextContent('Saved');
  });
});

describe('ProblemNotice', () => {
  const base = { requestId: 'req-1', retryable: true };

  it('shows the cause, the request ID and a retry for a known code', async () => {
    const onRetry = vi.fn();
    inEnglish(<ProblemNotice problem={{ ...base, code: 'SERVICE_NOT_READY' }} onRetry={onRetry} />);
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('The control plane is not ready');
    expect(alert).toHaveTextContent('Request ID req-1');
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it('names the code when it has no translation', () => {
    inEnglish(<ProblemNotice problem={{ ...base, code: 'NEW_CODE' }} />);
    expect(screen.getByRole('alert')).toHaveTextContent('The request failed with code NEW_CODE');
  });

  it('offers no retry for failures that will not pass on their own', () => {
    inEnglish(
      <ProblemNotice
        problem={{ ...base, code: 'ACCESS_DENIED', retryable: false }}
        onRetry={() => undefined}
      />,
    );
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('shows how long to wait when the server said so', () => {
    inEnglish(<ProblemNotice problem={{ ...base, code: 'RATE_LIMITED', retryAfterSeconds: 1 }} />);
    expect(screen.getByRole('alert')).toHaveTextContent('You can try again in 1 second.');
  });
});
