import type { Deployment, GatewayInstance, ModelAlias } from '@ownsolid/api';
import { describe, expect, it } from 'vitest';
import {
  aliasNameProblem,
  gatewayOrigin,
  nodeGateway,
  publishedNames,
  sampleCall,
  suggestedAliasName,
  unpublishable,
} from './gateway-publish-state.ts';

const ready = {
  removed_at: null,
  desired_state: 'running',
  observed_state: 'running',
  stale: false,
  resource_state: 'held',
  observation: { ready: true, verification: { observed_at: '2026-11-08T00:00:00Z' } },
} as unknown as Deployment;

const deployment = (change: Partial<Deployment>) => ({ ...ready, ...change });

describe('whether a deployment can be published', () => {
  it('accepts a running, verified deployment', () => {
    expect(unpublishable(ready)).toBeNull();
  });

  it('refuses a removed one', () => {
    expect(unpublishable(deployment({ removed_at: '2026-11-08T00:00:00Z' }))).toBe('removed');
    expect(unpublishable(deployment({ desired_state: 'removed' }))).toBe('removed');
  });

  it('refuses one that is not running as asked, or not reported fresh', () => {
    expect(unpublishable(deployment({ desired_state: 'stopped' }))).toBe('notRunning');
    expect(unpublishable(deployment({ observed_state: 'loading' }))).toBe('notRunning');
    expect(unpublishable(deployment({ stale: true }))).toBe('notRunning');
    expect(unpublishable(deployment({ resource_state: 'recovery_required' }))).toBe('notRunning');
  });

  it('refuses one whose answer was never checked', () => {
    expect(unpublishable(deployment({ observation: null }))).toBe('notVerified');
    expect(
      unpublishable(
        deployment({ observation: { ready: true } as unknown as Deployment['observation'] }),
      ),
    ).toBe('notVerified');
    expect(
      unpublishable(
        deployment({
          observation: { ready: false, verification: {} } as unknown as Deployment['observation'],
        }),
      ),
    ).toBe('notVerified');
  });
});

describe('the name callers use', () => {
  it('is made from the model name in the gateway alphabet', () => {
    expect(suggestedAliasName('Qwen3-32B Instruct')).toBe('qwen3-32b-instruct');
    expect(suggestedAliasName('meta/Llama 3.1  8B__chat')).toBe('meta-llama-3.1-8b-chat');
    expect(suggestedAliasName('3d-model')).toBe('d-model');
    expect(suggestedAliasName('model.')).toBe('model');
  });

  it('is empty when nothing usable is left', () => {
    expect(suggestedAliasName(String.fromCodePoint(0x6a21, 0x578b))).toBe('');
    expect(suggestedAliasName('123')).toBe('');
  });

  it('stays within the length the gateway accepts', () => {
    const long = suggestedAliasName(`a${'b'.repeat(100)}`);
    expect(long).toHaveLength(64);
    expect(aliasNameProblem(long)).toBeNull();
  });

  it('explains what is wrong with a typed name', () => {
    expect(aliasNameProblem('')).toBe('empty');
    expect(aliasNameProblem('1model')).toBe('start');
    expect(aliasNameProblem('Model')).toBe('start');
    expect(aliasNameProblem('my model')).toBe('characters');
    expect(aliasNameProblem(`a${'b'.repeat(64)}`)).toBe('characters');
    expect(aliasNameProblem('qwen3.5-chat')).toBeNull();
  });
});

describe('the address of a new gateway', () => {
  it('accepts an HTTPS origin and normalizes what the server would refuse for no reason', () => {
    expect(gatewayOrigin('https://gateway.lab.internal')).toBe('https://gateway.lab.internal');
    expect(gatewayOrigin(' https://Gateway.Lab.Internal/ ')).toBe('https://gateway.lab.internal');
    expect(gatewayOrigin('https://gateway.lab.internal:443')).toBe('https://gateway.lab.internal');
    expect(gatewayOrigin('https://gateway.lab.internal:8443')).toBe(
      'https://gateway.lab.internal:8443',
    );
    expect(gatewayOrigin('https://10.0.0.7:8443')).toBe('https://10.0.0.7:8443');
  });

  it('refuses anything that is not only an HTTPS origin', () => {
    for (const value of [
      '',
      'gateway.lab.internal',
      'http://gateway.lab.internal',
      'https://gateway.lab.internal/v1',
      'https://gateway.lab.internal?x=1',
      'https://gateway.lab.internal#top',
      'https://gateway.lab.internal?',
      'https://user:secret@gateway.lab.internal',
      'https://gateway lab.internal',
      'https://gateway.lab.internal:70000',
      'ftp://gateway.lab.internal',
    ]) {
      expect(gatewayOrigin(value), value).toBeNull();
    }
  });
});

describe('how a published deployment is called', () => {
  it('gives a call that names the model and reads the key from the environment', () => {
    const call = sampleCall('https://gateway.lab.internal', 'qwen3-chat');
    expect(call).toContain('https://gateway.lab.internal/v1/chat/completions');
    expect(call).toContain('"model":"qwen3-chat"');
    expect(call).toContain('Bearer $OWNSOLID_KEY');
  });

  it('lists only the names that still send callers to the deployment', () => {
    const aliases = [
      { id: 'a', deployment_id: 'd1', disabled_at: null },
      { id: 'b', deployment_id: 'd1', disabled_at: '2026-11-08T00:00:00Z' },
      { id: 'c', deployment_id: 'd2', disabled_at: null },
    ] as unknown as ModelAlias[];
    expect(publishedNames(aliases, 'd1').map((alias) => alias.id)).toEqual(['a']);
  });

  it('finds the active gateway of a node', () => {
    const gateways = [
      { id: 'old', node_id: 'n1', state: 'revoked' },
      { id: 'now', node_id: 'n1', state: 'active' },
      { id: 'other', node_id: 'n2', state: 'active' },
    ] as unknown as GatewayInstance[];
    expect(nodeGateway(gateways, 'n1')?.id).toBe('now');
    expect(nodeGateway(gateways, 'n3')).toBeUndefined();
  });
});
