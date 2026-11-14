import { I18nProvider } from '@ownsolid/i18n';
import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Server } from 'lucide-react';
import type { ReactNode } from 'react';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { Button } from './button.tsx';
import { Segmented, Switch } from './controls.tsx';
import { CodeBlock } from './code-block.tsx';
import { CopyText } from './copy-text.tsx';
import { Dialog, Drawer } from './dialog.tsx';
import { EmptyState } from './empty-state.tsx';
import { FilterTiles } from './filter-tiles.tsx';
import { Meter } from './meter.tsx';
import { Notice } from './notice.tsx';
import { ProblemNotice } from './problem-notice.tsx';
import { TypedConfirmation } from './typed-confirmation.tsx';
import { StatusPill } from './status-pill.tsx';
import { TextField } from './text-field.tsx';
import { ToastProvider, useToast } from './toast.tsx';

beforeAll(() => {
  // jsdom has no top layer; these stand in for the browser's behavior.
  Object.assign(HTMLDialogElement.prototype, {
    showModal(this: HTMLDialogElement) {
      this.open = true;
    },
    close(this: HTMLDialogElement) {
      this.open = false;
    },
  });
});

const inEnglish = (node: ReactNode) =>
  render(
    <I18nProvider locale="en" catalog={english}>
      {node}
    </I18nProvider>,
  );

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

describe('FilterTiles', () => {
  it('shows a tile that is not ready with its reason, and does not let it be chosen', async () => {
    const onChange = vi.fn();
    inEnglish(
      <FilterTiles
        label="Filter"
        value="all"
        tiles={[
          { value: 'all', label: 'All', count: 3 },
          { value: 'later', label: 'Over limit', unavailable: 'Not ready: no limits yet' },
        ]}
        onChange={onChange}
      />,
    );
    const later = screen.getByRole('button', { name: /Over limit/ });
    expect(later).toBeDisabled();
    expect(later).toHaveTextContent('Not ready: no limits yet');
    await userEvent.click(later);
    expect(onChange).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: /All/ }));
    expect(onChange).toHaveBeenCalledWith('all');
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
  it('can title the page as its heading', () => {
    render(<Notice title="Page not found" titleAs="h1" />);
    expect(screen.getByRole('heading', { level: 1, name: 'Page not found' })).toBeInTheDocument();
  });

  it('announces critical messages as alerts and others as status', () => {
    const { rerender } = render(<Notice tone="critical" title="Failed" />);
    expect(screen.getByRole('alert')).toHaveTextContent('Failed');
    rerender(<Notice title="Saved" />);
    expect(screen.getByRole('status')).toHaveTextContent('Saved');
  });
});

describe('TypedConfirmation', () => {
  const props = {
    title: 'Remove this',
    consequence: 'It is gone for good.',
    expected: 'Node-1',
    confirmLabel: 'Remove',
    pending: false,
    onClose: () => undefined,
  };

  it('stays closed to the action until the target is typed', async () => {
    const onConfirm = vi.fn();
    inEnglish(<TypedConfirmation {...props} onConfirm={onConfirm} />);
    const field = screen.getByLabelText('Type Node-1 to confirm', {});
    const remove = screen.getByRole('button', { name: 'Remove', hidden: true });
    expect(remove).toBeDisabled();
    await userEvent.type(field, 'node');
    expect(remove).toBeDisabled();
    await userEvent.type(field, '-1 {Enter}');
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it('does not act on Enter before the target is typed, or while it is working', async () => {
    const onConfirm = vi.fn();
    const { rerender } = inEnglish(<TypedConfirmation {...props} onConfirm={onConfirm} />);
    const field = screen.getByLabelText('Type Node-1 to confirm', {});
    await userEvent.type(field, 'wrong{Enter}');
    expect(onConfirm).not.toHaveBeenCalled();
    rerender(
      <I18nProvider locale="en">
        <TypedConfirmation {...props} pending onConfirm={onConfirm} />
      </I18nProvider>,
    );
    await userEvent.clear(field);
    await userEvent.type(field, 'Node-1{Enter}');
    expect(onConfirm).not.toHaveBeenCalled();
  });
});

describe('ProblemNotice', () => {
  const base = { requestId: 'req-1', retryable: true };

  it('shows the cause, the request ID and a retry for a known code', async () => {
    const onRetry = vi.fn();
    inEnglish(<ProblemNotice problem={{ ...base, code: 'SERVICE_NOT_READY' }} onRetry={onRetry} />);
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('The control plane is not ready');
    expect(alert).toHaveTextContent('Request ID');
    expect(alert).toHaveTextContent('req-1');
    expect(screen.getByRole('button', { name: 'Copy' })).toBeInTheDocument();
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
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
  });

  it('shows how long to wait when the server said so', () => {
    inEnglish(
      <ProblemNotice problem={{ ...base, code: 'RATE_LIMITED', retryAfterSeconds: 526 }} />,
    );
    expect(screen.getByRole('alert')).toHaveTextContent('You can try again in 8m 46s.');
  });

  it('holds the retry until the wait the server asked for is over', () => {
    vi.useFakeTimers();
    try {
      const onRetry = vi.fn();
      inEnglish(
        <ProblemNotice
          problem={{ ...base, code: 'RATE_LIMITED', retryAfterSeconds: 2 }}
          onRetry={onRetry}
        />,
      );
      const retry = screen.getByRole('button', { name: 'Try again' });
      expect(retry).toBeDisabled();
      expect(screen.getByRole('timer')).toHaveTextContent('2s');
      act(() => {
        vi.advanceTimersByTime(2_100);
      });
      expect(retry).toBeEnabled();
      expect(screen.queryByRole('timer')).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('offers a reload for a page that is out of date, instead of repeating the request', () => {
    const onRetry = vi.fn();
    inEnglish(
      <ProblemNotice
        problem={{ ...base, code: 'STORAGE_COMMIT_OUTCOME_UNKNOWN', retryable: true }}
        onRetry={onRetry}
      />,
    );
    expect(screen.getByRole('button', { name: 'Reload the page' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
  });
});

describe('Dialog', () => {
  it('opens with its title as the accessible name and closes from the button', async () => {
    const onClose = vi.fn();
    inEnglish(
      <Dialog open onClose={onClose} title="Add node">
        <p>Body</p>
      </Dialog>,
    );
    expect(screen.getByRole('dialog', { name: 'Add node' })).toHaveAttribute('open');
    await userEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('renders no content while closed', () => {
    inEnglish(
      <Dialog open={false} onClose={() => undefined} title="Add node">
        <p>Body</p>
      </Dialog>,
    );
    expect(screen.queryByText('Body')).not.toBeInTheDocument();
  });

  it('closes on Escape, which the browser reports as cancel', () => {
    const onClose = vi.fn();
    inEnglish(
      <Drawer open onClose={onClose} title="gpu-a01">
        <p>Body</p>
      </Drawer>,
    );
    fireEvent(screen.getByRole('dialog'), new Event('cancel', { cancelable: true }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('closes on a backdrop click but not on a click inside', async () => {
    const onClose = vi.fn();
    inEnglish(
      <Dialog open onClose={onClose} title="Add node">
        <p>Body</p>
      </Dialog>,
    );
    await userEvent.click(screen.getByText('Body'));
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('dialog'));
    expect(onClose).toHaveBeenCalledOnce();
  });
});

describe('Toast', () => {
  function Trigger({ undo, retry }: { undo?: () => void; retry?: () => void }) {
    const toast = useToast();
    return (
      <Button
        onClick={() => {
          toast({ message: 'Node drained', undo, retry });
        }}
      >
        Go
      </Button>
    );
  }

  afterEach(() => {
    vi.useRealTimers();
  });

  it('offers undo, then confirms it', async () => {
    const undo = vi.fn();
    inEnglish(
      <ToastProvider>
        <Trigger undo={undo} />
      </ToastProvider>,
    );
    await userEvent.click(screen.getByRole('button', { name: 'Go' }));
    expect(screen.getByRole('status')).toHaveTextContent('Node drained');
    await userEvent.click(screen.getByRole('button', { name: 'Undo' }));
    expect(undo).toHaveBeenCalledOnce();
    expect(screen.getByRole('status')).toHaveTextContent('Change undone');
  });

  it('offers a retry, then steps aside', async () => {
    const retry = vi.fn();
    inEnglish(
      <ToastProvider>
        <Trigger retry={retry} />
      </ToastProvider>,
    );
    await userEvent.click(screen.getByRole('button', { name: 'Go' }));
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(retry).toHaveBeenCalledOnce();
    expect(screen.getByRole('status')).toBeEmptyDOMElement();
  });

  it('disappears on its own', () => {
    vi.useFakeTimers();
    inEnglish(
      <ToastProvider>
        <Trigger />
      </ToastProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    expect(screen.getByRole('status')).toHaveTextContent('Node drained');
    act(() => {
      vi.advanceTimersByTime(2_500);
    });
    expect(screen.getByRole('status')).toBeEmptyDOMElement();
  });
});

describe('small components', () => {
  it('StatusPill always shows its label', () => {
    render(<StatusPill tone="critical">Offline</StatusPill>);
    expect(screen.getByText('Offline')).toBeInTheDocument();
  });

  it('Meter clamps its value and reports it', () => {
    render(<Meter value={140} label="CPU" />);
    expect(screen.getByRole('meter', { name: 'CPU' })).toHaveAttribute('aria-valuenow', '100');
  });

  it('Segmented marks the current choice and reports changes', async () => {
    const onChange = vi.fn();
    render(
      <Segmented
        label="View"
        value="list"
        onChange={onChange}
        options={[
          { value: 'list', label: 'List' },
          { value: 'grid', label: 'Grid' },
        ]}
      />,
    );
    expect(screen.getByRole('button', { name: 'List' })).toHaveAttribute('aria-pressed', 'true');
    await userEvent.click(screen.getByRole('button', { name: 'Grid' }));
    expect(onChange).toHaveBeenCalledWith('grid');
  });

  it('Switch toggles', async () => {
    const onChange = vi.fn();
    render(<Switch label="Offline mode" checked={false} onChange={onChange} />);
    await userEvent.click(screen.getByRole('switch', { name: 'Offline mode' }));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it('CodeBlock copies every line as written and confirms', async () => {
    const user = userEvent.setup();
    inEnglish(<CodeBlock code={'first line\nsecond line'} label="Join commands" />);
    await user.click(screen.getByRole('button', { name: 'Copy: Join commands' }));
    expect(await navigator.clipboard.readText()).toBe('first line\nsecond line');
    expect(
      await screen.findByRole('button', { name: 'Copied: Join commands' }),
    ).toBeInTheDocument();
  });

  it('CopyText copies and confirms', async () => {
    const user = userEvent.setup();
    inEnglish(<CopyText text="gpu-a01" />);
    await user.click(screen.getByRole('button', { name: 'Copy' }));
    expect(await navigator.clipboard.readText()).toBe('gpu-a01');
    expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument();
  });

  it('EmptyState names the next action', () => {
    render(
      <EmptyState icon={Server} title="No nodes" action={<Button>Add node</Button>}>
        Add a node to run workloads.
      </EmptyState>,
    );
    expect(screen.getByRole('heading', { name: 'No nodes' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add node' })).toBeInTheDocument();
  });
});
