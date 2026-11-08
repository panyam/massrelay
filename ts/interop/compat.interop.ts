import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { CollabClient as OldClient } from 'massrelay-v033';
import { CollabClient } from '../src/CollabClient.js';
import { testKey, type TestKey } from './jwt.js';
import { startRelay, type Relay } from './relay.js';

// The published v0.3.3 client against this checkout's relay. Apps upgrade
// their client after the relay they talk to, so a relay release must not send
// an older client anything it can't decode. Needs its own relay with auth,
// since token_expires_at only exists for an authenticated connection.

function until(cond: () => boolean, what: string, ms = 5000): Promise<void> {
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

describe('a v0.3.3 CollabClient against this relay, auth required', () => {
  let relay: Relay;
  let key: TestKey;
  const open: Array<{ disconnect(): void }> = [];

  beforeAll(async () => {
    key = testKey();
    relay = await startRelay(key.env);
  });

  afterAll(() => {
    open.forEach((c) => c.disconnect());
    relay?.stop();
    key?.remove();
  });

  it('joins, and gets app messages from a current client in the same room', async () => {
    const room = `design:${crypto.randomUUID()}`;
    const oldIds: string[] = [];
    const got: string[] = [];
    const old = new OldClient({
      token: key.sign('alice', 3600),
      onConnect: (id) => oldIds.push(id),
      onAppMessage: (kind) => got.push(kind),
    });
    open.push(old);
    old.connect(relay.url, room, 'alice', {});
    await until(() => oldIds.length === 1, 'the v0.3.3 client to join');

    const cur = new CollabClient({ getToken: () => key.sign('bob', 3600) });
    open.push(cur);
    cur.connect(relay.url, room, 'bob', {});
    await until(() => cur.isConnected, 'the current client to join');
    cur.sendAppMessage('hello', new Uint8Array([1]), { to: oldIds[0] });
    await until(() => got.length === 1, 'the v0.3.3 client to get the message');

    expect(got).toEqual(['hello']);
    expect(old.isConnected).toBe(true);
  });
});
