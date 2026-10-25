import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { CollabClient } from '../src/CollabClient.js';
import { startRelay, type Relay } from '../interop/relay.js';
import { stallProxy, type StallProxy } from '../interop/stall-proxy.js';

// Mission #54: a CollabClient keeps its session when the relay drops it. One
// client is evicted as a slow consumer and should rejoin the same session;
// then the relay restarts and both should report the session gone rather
// than recreate it.

interface Peer {
  name: string;
  client: CollabClient;
  ids: string[];
  joinedPeers: string[];
  left: string[];
  ended: string[];
  reconnecting: number[];
  disconnects: number;
}

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

function peer(name: string): Peer {
  const p: Peer = { name, client: undefined as unknown as CollabClient, ids: [], joinedPeers: [], left: [], ended: [], reconnecting: [], disconnects: 0 };
  p.client = new CollabClient({
    retryDelayMs: 200,
    onConnect: (id) => p.ids.push(id),
    onPeerJoined: (info) => info.clientId && p.joinedPeers.push(info.clientId),
    onPeerLeft: (id) => p.left.push(id),
    onSessionEnded: (reason) => p.ended.push(reason),
    onReconnecting: (n) => p.reconnecting.push(n),
    onDisconnect: () => p.disconnects++,
  });
  return p;
}

function report(p: Peer): string {
  const parts = [`joined as ${p.ids.map((id) => id.slice(0, 8)).join(' then ')}`];
  if (p.disconnects) parts.push(`dropped ${p.disconnects}x`);
  if (p.reconnecting.length) parts.push(`reconnect attempts ${p.reconnecting.length}`);
  if (p.ended.length) parts.push(`session ended: ${p.ended.join(',')}`);
  parts.push(p.client.isConnected ? `in room ${p.client.sessionId}` : 'not connected');
  return `${p.name.padEnd(8)} ${parts.join('; ')}`;
}

const payload = new Uint8Array(256 * 1024);

describe('exercise-reconnect: relay binary drops a client, then restarts', () => {
  let relay: Relay;
  let proxy: StallProxy;
  let a: Peer;
  let b: Peer;
  const room = `design:${crypto.randomUUID()}`;

  beforeAll(async () => {
    relay = await startRelay();
    proxy = await stallProxy(relay.url);
    a = peer('steady');
    b = peer('stalled');
    b.client.connect(proxy.url, room, 'stalled', {});
    await until(() => b.ids.length === 1, 'stalled to join', 5000);
    a.client.connect(relay.url, room, 'steady', {});
    await until(() => a.ids.length === 1, 'steady to join', 5000);
  });

  afterAll(() => {
    a?.client.disconnect();
    b?.client.disconnect();
    proxy?.stop();
    relay?.stop();
  });

  it('after a slow_consumer eviction the stalled client rejoins the same session', async () => {
    const firstId = b.ids[0];
    proxy.pause();
    // 25/s stays under the relay's 30/s per-client limit, so nothing is
    // dropped as RATE_LIMITED before the stalled client's queue fills.
    const flood = setInterval(() => a.client.sendAppMessage('flood', payload, { to: firstId }), 40);
    try {
      await until(() => a.left.includes(firstId), 'the relay to evict the stalled client', 20000);
    } finally {
      clearInterval(flood);
    }
    proxy.resume();
    try {
      await until(() => b.ids.length === 2 && b.client.isConnected, 'the stalled client to rejoin', 10000);
      await until(() => a.joinedPeers.includes(b.ids[1]), 'steady to see it rejoin', 5000);
    } finally {
      const rejoined = b.ids[1] && a.joinedPeers.includes(b.ids[1]) ? `${b.ids[1].slice(0, 8)} join` : 'nobody rejoin';
      console.log(`eviction → ${report(b)}`);
      console.log(`eviction → ${report(a)}; saw ${firstId.slice(0, 8)} leave and ${rejoined}`);
    }
    expect(b.client.sessionId).toBe(room);
    expect(b.ended).toEqual([]);
  }, 40000);

  it('after a relay restart both clients report the session gone', async () => {
    await relay.restart();
    let status = 0;
    try {
      await until(() => a.ended.length > 0 && b.ended.length > 0, 'both clients to give up on the session', 15000);
    } finally {
      status = (await fetch(`http://127.0.0.1:${relay.port}/api/v1/rooms/${encodeURIComponent(room)}`)).status;
      console.log(`restart  → ${report(a)}`);
      console.log(`restart  → ${report(b)}`);
      console.log(`restart  → GET /api/v1/rooms/{session} is ${status}${status === 404 ? ' (no phantom room)' : ''}`);
    }
    expect(a.ended).toEqual(['session_gone']);
    expect(b.ended).toEqual(['session_gone']);
    expect(status).toBe(404);
  }, 30000);
});
