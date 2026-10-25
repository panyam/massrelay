import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { CollabClient } from '../src/CollabClient.js';
import { testKey, type TestKey } from './jwt.js';
import { startRelay, type Relay } from './relay.js';

// The relay ends a connection when its token's exp passes. Needs its own
// relay, since the suite's shared one runs without auth.

function until(cond: () => boolean, what: string, ms: number): Promise<void> {
  return new Promise((ok, fail) => {
    const start = Date.now();
    const tick = () => {
      if (cond()) return ok();
      if (Date.now() - start > ms) return fail(new Error(`timed out waiting for ${what}`));
      setTimeout(tick, 20);
    };
    tick();
  });
}

describe('token expiry against a real relay', () => {
  let relay: Relay;
  let key: TestKey;
  const open: CollabClient[] = [];

  beforeAll(async () => {
    key = testKey();
    relay = await startRelay(key.env);
  });

  afterAll(() => {
    open.forEach((c) => c.disconnect());
    relay?.stop();
    key?.remove();
  });

  it('ends the session with token_expired when a static token expires', async () => {
    const ids: string[] = [];
    const ended: string[] = [];
    const c = new CollabClient({ token: key.sign('alice', 2), onConnect: (id) => ids.push(id), onSessionEnded: (r) => ended.push(r) });
    open.push(c);
    c.connect(relay.url, `design:${crypto.randomUUID()}`, 'alice', {});
    await until(() => ids.length === 1, 'the join', 5000);
    await until(() => ended.length > 0, 'the relay to end the expired connection', 5000);

    expect(ended).toEqual(['token_expired']);
    expect(c.isConnected).toBe(false);
    expect(ids).toHaveLength(1);
  }, 15000);

  it('rejoins the same session on a fresh token when getToken is set', async () => {
    const room = `design:${crypto.randomUUID()}`;
    const steadyJoined: string[] = [];
    const steady = new CollabClient({ token: key.sign('bob', 3600), onPeerJoined: (p) => p.clientId && steadyJoined.push(p.clientId) });
    open.push(steady);
    steady.connect(relay.url, room, 'bob', {});
    await until(() => steady.isConnected, 'bob to join', 5000);

    const ids: string[] = [];
    const ended: string[] = [];
    let issued = 0;
    const c = new CollabClient({
      retryDelayMs: 100,
      getToken: () => {
        issued++;
        return key.sign('alice', issued === 1 ? 2 : 3600);
      },
      onConnect: (id) => ids.push(id),
      onSessionEnded: (r) => ended.push(r),
    });
    open.push(c);
    c.connect(relay.url, room, 'alice', {});
    await until(() => ids.length === 1, 'the first join', 5000);
    await until(() => ids.length === 2 && c.isConnected, 'the rejoin after expiry', 8000);

    expect(ended).toEqual([]);
    expect(issued).toBe(2);
    expect(c.sessionId).toBe(room);
    await until(() => steadyJoined.includes(ids[1]), 'bob to see alice rejoin', 3000);
  }, 20000);
});
