import type { Deployment, ModelAlias, ModelVersion } from '@ownsolid/api';
import {
  deploymentAlias,
  derivedModel,
  readyModel,
  runningDeployment,
  unavailableAlias,
} from '@ownsolid/shell/testing';
import { describe, expect, it } from 'vitest';
import {
  catalogueEntries,
  contextTokens,
  inCatalogueTile,
  matchesCatalogueTerm,
  sourceRepository,
} from './model-catalogue.ts';

const aliases: ModelAlias[] = [deploymentAlias, unavailableAlias];
const deployments: Deployment[] = [{ ...runningDeployment, model_id: readyModel.id }];
const models: ModelVersion[] = [readyModel];

describe('catalogueEntries', () => {
  it('joins a name to the model behind its deployment', () => {
    const [entry] = catalogueEntries([deploymentAlias], deployments, models);
    expect(entry?.model?.name).toBe('Qwen3-0.6B');
  });

  it('keeps a name whose deployment or model could not be read', () => {
    expect(catalogueEntries(aliases, [], models).map((entry) => entry.model)).toEqual([
      undefined,
      undefined,
    ]);
    expect(catalogueEntries([deploymentAlias], deployments, [])[0]?.model).toBeUndefined();
  });

  it('leaves out a name that can never be called again', () => {
    const disabled = { ...deploymentAlias, disabled_at: '2026-11-07T00:00:00Z' };
    expect(catalogueEntries([disabled], deployments, models)).toEqual([]);
  });
});

describe('inCatalogueTile', () => {
  const [callable, stopped] = catalogueEntries(aliases, deployments, models);
  if (!callable || !stopped) throw new Error('two entries expected');

  it('counts everything under all', () => {
    expect(inCatalogueTile(callable, 'all')).toBe(true);
    expect(inCatalogueTile(stopped, 'all')).toBe(true);
  });

  it('splits by whether a call would be taken now', () => {
    expect(inCatalogueTile(callable, 'callable')).toBe(true);
    expect(inCatalogueTile(callable, 'unavailable')).toBe(false);
    expect(inCatalogueTile(stopped, 'callable')).toBe(false);
    expect(inCatalogueTile(stopped, 'unavailable')).toBe(true);
  });
});

describe('matchesCatalogueTerm', () => {
  const [entry] = catalogueEntries([deploymentAlias], deployments, models);
  if (!entry) throw new Error('one entry expected');

  it('finds the name a person calls and the model behind it', () => {
    expect(matchesCatalogueTerm(entry, 'support')).toBe(true);
    expect(matchesCatalogueTerm(entry, 'qwen')).toBe(true);
    expect(matchesCatalogueTerm(entry, 'nothing')).toBe(false);
  });

  it('does not fail on a row without its model', () => {
    expect(matchesCatalogueTerm({ alias: deploymentAlias, model: undefined }, 'qwen')).toBe(false);
  });
});

describe('model facts', () => {
  it('reads the context length of a published and a fine-tuned model', () => {
    expect(contextTokens(readyModel)).toBe(2048);
    expect(contextTokens(derivedModel)).toBeGreaterThan(0);
  });

  it('names where a model came from', () => {
    expect(sourceRepository(readyModel)).toBe('Qwen/Qwen3-0.6B');
    expect(sourceRepository(derivedModel)).toBe('Qwen/Qwen3-0.6B');
  });
});
