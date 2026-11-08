import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { CollabClient } from '../src/CollabClient.js';
import { testKey, type TestKey } from '../interop/jwt.js';
import { relayForExercise, type Relay } from '../interop/relay.js';

// Mission #66: a CollabClient on 3s tokens with getToken refreshes them over
// the open socket and stays in its room, same clientId, with no peer seeing it
// leave. When the refresh is refused, the relay ends it at the old exp and the
// one rejoin is refused too, so the session ends with token_refused.

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

const sleep = (ms: number) => new Promise((ok) => setTimeout(ok, ms));

describe('exercise-token-refresh: relay with RELAY_AUTH_REQUIRED=true', () => {
  let relay: Relay;
  let key: TestKey;
  const room = `room-${crypto.randomUUID()}`;
  const open: CollabClient[] = [];

  // A's state, shared by both steps.
  let rooms = [room];
  let issued = 0;
  const aIds: string[] = [];
  const aEvents: string[] = [];
  const refreshes: string[] = [];
  let aDisconnects = 0;
  let aReconnects = 0;
  let a: CollabClient;
  let b: CollabClient;
  const bLeft: string[] = [];

  beforeAll(async () => {
    key = testKey('exercise-app', 'exercise-issuer');
    relay = await relayForExercise(key.env);
  });

  afterAll(() => {
    open.forEach((c) => c.disconnect());
    relay?.stop();
    key?.remove();
  });

  it('A refreshes its 3s token over the open socket for three lifetimes', async () => {
    const atA: number[] = [];
    const atB: number[] = [];
    b = new CollabClient({
      token: key.sign('bob', 3600, { rooms: [room] }),
      onPeerLeft: (id) => bLeft.push(id),
      onAppMessage: (_k, p) => atB.push(p[0] | (p[1] << 8)),
    });
    open.push(b);
    a = new CollabClient({
      retryDelayMs: 100,
      getToken: () => {
        issued++;
        return key.sign('alice', 3, { rooms });
      },
      onConnect: (id) => aIds.push(id),
      onDisconnect: () => aDisconnects++,
      onReconnecting: () => aReconnects++,
      onEvent: (e) => {
        if (e.event.case === 'sessionEnded') aEvents.push(`ended:${e.event.value.reason}`);
      },
      onTokenRefresh: (r) => refreshes.push(r.ok ? 'ok' : `refused:${r.error}`),
      onSessionEnded: (r) => aEvents.push(`session:${r}`),
      onAppMessage: (_k, p) => atA.push(p[0] | (p[1] << 8)),
    });
    open.push(a);
    b.connect(relay.url, room, 'bob', {});
    await until(() => b.isConnected, 'B to join', 5000);
    a.connect(relay.url, room, 'alice', {});
    await until(() => a.isConnected, 'A to join', 5000);

    let sent = 0;
    const start = Date.now();
    while (Date.now() - start < 10000) {
      const p = new Uint8Array([sent & 0xff, sent >> 8]);
      if (a.isConnected) a.sendAppMessage('tick', p);
      if (b.isConnected) b.sendAppMessage('tick', p);
      sent++;
      await sleep(250);
    }
    await sleep(500);

    const want = Array.from({ length: sent }, (_, i) => i);
    console.log(`after ${sent} ticks: A ids ${aIds.join(',')}, tokens ${issued}, refreshes [${refreshes.join(',')}], A disconnects ${aDisconnects}, B saw leaves [${bLeft.join(',')}], at A ${atA.length}/${sent}, at B ${atB.length}/${sent}`);
    expect(aIds).toHaveLength(1);
    expect(aDisconnects).toBe(0);
    expect(bLeft).toEqual([]);
    expect(atB).toEqual(want);
    expect(atA).toEqual(want);
    expect(refreshes.length).toBeGreaterThanOrEqual(3);
    expect(refreshes.every((r) => r === 'ok')).toBe(true);
  }, 20000);

  it('a refused refresh ends at the old exp, and the one rejoin ends with token_refused', async () => {
    rooms = ['elsewhere'];
    refreshes.length = 0;
    await until(() => aEvents.some((e) => e.startsWith('session:')), 'A to end', 10000);

    console.log(`refreshes [${refreshes.join(',')}], A events [${aEvents.join(',')}], reconnect attempts ${aReconnects}`);
    expect(refreshes.length).toBeGreaterThanOrEqual(1);
    expect(refreshes.every((r) => r.startsWith('refused:'))).toBe(true);
    expect(aEvents).toEqual(['ended:token_expired', 'session:token_refused']);
    expect(aReconnects).toBe(1);
    expect(aIds).toHaveLength(1);
  }, 15000);
});
