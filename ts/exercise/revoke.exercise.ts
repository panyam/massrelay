import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { CollabClient } from '../src/CollabClient.js';
import { testKey, type TestKey } from '../interop/jwt.js';
import { relayForExercise, upgradeStatus, type Relay } from '../interop/relay.js';

// Mission #72: an operator revokes a user with one admin call. Every
// connection the user has open ends within a second with SessionEnded{denied},
// their peers see them leave, their CollabClient doesn't reconnect, and a new
// connection gets a 403 until the operator allows them again.

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

interface Peer {
  client: CollabClient;
  ids: string[];
  ended: string[];
  reconnects: number;
  /** clientId → PeerLeft reason, as this peer saw it. */
  left: Map<string, string>;
}

describe('exercise-revoke: relay with RELAY_AUTH_REQUIRED=true and an admin token', () => {
  let relay: Relay;
  let key: TestKey;
  const rooms = [`room-${crypto.randomUUID()}`, `room-${crypto.randomUUID()}`];
  const open: CollabClient[] = [];
  let alice: Peer[];
  let bob: Peer;

  const admin = (method: string, path: string) =>
    fetch(`${relay.httpUrl}/admin${path}`, { method, headers: { Authorization: `Bearer ${relay.adminToken}` } });

  function peer(name: string, room: string): Peer {
    const p: Peer = { client: undefined as unknown as CollabClient, ids: [], ended: [], reconnects: 0, left: new Map() };
    p.client = new CollabClient({
      retryDelayMs: 100,
      getToken: () => key.sign(name, 3600),
      onConnect: (id) => p.ids.push(id),
      onSessionEnded: (r) => p.ended.push(r),
      onReconnecting: () => p.reconnects++,
      onEvent: (e) => {
        if (e.event.case === 'peerLeft') p.left.set(e.event.value.clientId, e.event.value.reason);
      },
    });
    open.push(p.client);
    p.client.connect(relay.url, room, name, {});
    return p;
  }

  beforeAll(async () => {
    key = testKey('exercise-app', 'exercise-issuer');
    relay = await relayForExercise({ ...key.env, RELAY_ADMIN_TOKEN: 'exercise-admin-token' });
  });

  afterAll(async () => {
    open.forEach((c) => c.disconnect());
    await admin('DELETE', '/subjects/alice/deny').catch(() => {});
    relay?.stop();
    key?.remove();
  });

  it('alice has two connections in two rooms, and bob shares one of them', async () => {
    bob = peer('bob', rooms[0]);
    await until(() => bob.client.isConnected, 'bob to join');
    alice = [peer('alice', rooms[0]), peer('alice', rooms[1])];
    await until(() => alice.every((a) => a.client.isConnected), 'both alice connections to join');
  });

  it('the operator denies alice, and both her connections end within a second', async () => {
    const start = Date.now();
    const res = await admin('POST', '/subjects/alice/deny');
    console.log(`deny alice          → HTTP ${res.status}`);
    expect(res.status).toBe(204);

    try {
      await until(() => alice.every((a) => a.ended.length > 0), "alice's connections to end", 1000);
    } finally {
      const took = Date.now() - start;
      console.log(`alice's connections → ${alice.map((a) => a.ended.join(',') || 'still open').join(' / ')} after ${took}ms; reconnect attempts ${alice.map((a) => a.reconnects).join('/')}`);
    }
    await until(() => bob.left.has(alice[0].ids[0]), 'bob to see alice leave', 2000).catch(() => {});
    console.log(`bob                 → saw alice leave with reason "${bob.left.get(alice[0].ids[0]) ?? '(not seen)'}"; ${bob.client.isConnected ? 'still connected' : 'disconnected'}`);

    await new Promise((r) => setTimeout(r, 500));
    expect(alice.map((a) => a.ended)).toEqual([['denied'], ['denied']]);
    expect(alice.map((a) => a.reconnects)).toEqual([0, 0]);
    expect(bob.left.get(alice[0].ids[0])).toBe('denied');
    expect(bob.client.isConnected).toBe(true);
  });

  it('a new connection with a fresh alice token gets a 403', async () => {
    const status = await upgradeStatus(relay.httpUrl, `/ws/v1/${rooms[0]}/sync?token=${key.sign('alice', 3600)}`);
    console.log(`new alice upgrade   → HTTP ${status}`);
    expect(status).toBe(403);
  });

  it('the operator allows alice again, and she can join', async () => {
    const res = await admin('DELETE', '/subjects/alice/deny');
    console.log(`allow alice         → HTTP ${res.status}`);
    expect(res.status).toBe(204);
    const again = peer('alice', rooms[0]);
    await until(() => again.client.isConnected, 'alice to join again');
    console.log(`alice joins again   → RoomJoined (clientId ${again.ids[0]})`);
  });
});
