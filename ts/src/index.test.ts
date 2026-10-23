import { describe, expect, it } from 'vitest';
import * as root from './index.js';

describe('package root entry', () => {
  it('exports the client, the engine and the model schemas', () => {
    expect(root.CollabClient).toBeTypeOf('function');
    expect(root.CollabEngine).toBeTypeOf('function');
    expect(root.CollabEventSchema).toBeDefined();
    expect(root.resolveRelayUrl).toBeTypeOf('function');
  });
});
