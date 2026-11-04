import { I18nProvider } from '@ownsolid/i18n';
import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Server } from 'lucide-react';
import type { ReactNode } from 'react';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { Button } from './button.tsx';
import { Segmented, Switch } from './controls.tsx';
import { CopyText } from './copy-text.tsx';
import { Dialog, Drawer } from './dialog.tsx';
import { EmptyState } from './empty-state.tsx';
import { Meter } from './meter.tsx';
import { Notice } from './notice.tsx';
import { ProblemNotice } from './problem-notice.tsx';
import { StatusPill } from './status-pill.tsx';
import { TextField } from './text-field.tsx';
import { ToastProvider, useToast } from './toast.tsx';

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

describe('Dialog', () => {
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
  function Trigger({ undo }: { undo?: () => void }) {
    const toast = useToast();
    return (
      <Button
        onClick={() => {
          toast({ message: 'Node drained', undo });
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
