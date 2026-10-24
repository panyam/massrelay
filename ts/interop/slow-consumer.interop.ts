import { connect, createServer, type Server, type Socket } from 'node:net';
import { afterEach, describe, expect, inject, it } from 'vitest';
import { CollabClient } from '../src/CollabClient.js';

// A browser that stops reading can't be made from Node's WebSocket, which has
// no pause. Instead the stuck client talks to the relay through this proxy,
// which can stop reading from the relay so the relay's writes back up on TCP
// the way they would for a stalled tab.

interface CloseFrame {
  code: number;
  reason: string;
}

interface StallProxy {
  url: string;
  pause(): void;
  resume(): void;
  /** Reads from the relay again but throws the bytes away, so the client
   * still sees nothing; only the sniffer and the close are observed. */
  discard(): void;
  /** Close frames the relay sent, read off the relay → client bytes. */
  relayCloses: CloseFrame[];
  /** Set once the relay end of the connection is gone. */
  relayGone: () => boolean;
  stop(): void;
}

function stallProxy(relayUrl: string): Promise<StallProxy> {
  const relayPort = Number(new URL(relayUrl).port);
  const relayCloses: CloseFrame[] = [];
  const sniff = closeFrameSniffer(relayCloses);
  let upstream: Socket | undefined;
  let gone = false;
  let paused = false;
  let discarding = false;
  const sockets: Socket[] = [];

  const server: Server = createServer((client) => {
    const relay = connect(relayPort, '127.0.0.1');
    upstream = relay;
    sockets.push(client, relay);
    if (paused) relay.pause();
    client.on('data', (d) => relay.write(d));
    relay.on('data', (d) => {
      sniff(d);
      if (!discarding) client.write(d);
    });
    relay.on('close', () => {
      gone = true;
      client.end();
    });
    client.on('close', () => relay.destroy());
    client.on('error', () => {});
    relay.on('error', () => {});
  });

  return new Promise((ok) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      ok({
        url: `ws://127.0.0.1:${port}`,
        pause: () => {
          paused = true;
          upstream?.pause();
        },
        resume: () => {
          paused = false;
          upstream?.resume();
        },
        discard: () => {
          discarding = true;
          paused = false;
          upstream?.resume();
        },
        relayCloses,
        relayGone: () => gone,
        stop: () => {
          for (const s of sockets) s.destroy();
          server.close();
        },
      });
    });
  });
}

/** Parses server → client WebSocket frames (unmasked) after the HTTP 101,
 * skipping payloads, and records close frames. */
function closeFrameSniffer(out: CloseFrame[]): (chunk: Buffer) => void {
  let upgraded = false;
  let pending = Buffer.alloc(0);
  let skip = 0;
  return (chunk) => {
    if (skip >= chunk.length) {
      skip -= chunk.length;
      return;
    }
    pending = Buffer.concat([pending, chunk.subarray(skip)]);
    skip = 0;
    if (!upgraded) {
      const end = pending.indexOf('\r\n\r\n');
      if (end < 0) return;
      pending = pending.subarray(end + 4);
      upgraded = true;
    }
    for (;;) {
      if (pending.length < 2) return;
      const opcode = pending[0] & 0x0f;
      let len = pending[1] & 0x7f;
      let off = 2;
      if (len === 126) {
        if (pending.length < 4) return;
        len = pending.readUInt16BE(2);
        off = 4;
      } else if (len === 127) {
        if (pending.length < 10) return;
        len = Number(pending.readBigUInt64BE(2));
        off = 10;
      }
      if (opcode === 0x8) {
        if (pending.length < off + len) return;
        const body = pending.subarray(off, off + len);
        out.push({ code: len >= 2 ? body.readUInt16BE(0) : 1005, reason: body.subarray(2).toString() });
      }
      if (pending.length >= off + len) {
        pending = pending.subarray(off + len);
        continue;
      }
      skip = off + len - pending.length;
      pending = Buffer.alloc(0);
      return;
    }
  };
}

interface Peer {
  client: CollabClient;
  clientId: string;
  received: { kind: string; from: string }[];
  left: { id: string }[];
  ended: string[];
  disconnected: boolean;
}

const cleanup: (() => void)[] = [];

afterEach(() => {
  while (cleanup.length) cleanup.pop()!();
});

function until(cond: () => boolean, what: string, ms = 3000): Promise<void> {
  return new Promise((ok, fail) => {
    const start = Date.now();
    const tick = () => {
      if (cond()) return ok();
      if (Date.now() - start > ms) return fail(new Error(`timed out waiting for ${what}`));
      setTimeout(tick, 10);
    };
    tick();
  });
}

async function join(url: string, sessionId: string, name: string): Promise<Peer> {
  const peer: Peer = { client: undefined as unknown as CollabClient, clientId: '', received: [], left: [], ended: [], disconnected: false };
  let connected = '';
  peer.client = new CollabClient({
    onConnect: (id) => (connected = id),
    onAppMessage: (kind, _payload, from) => peer.received.push({ kind, from }),
    onPeerLeft: (id) => peer.left.push({ id }),
    onSessionEnded: (reason) => peer.ended.push(reason),
    onDisconnect: () => (peer.disconnected = true),
  });
  cleanup.push(() => peer.client.disconnect());
  peer.client.connect(url, sessionId, name, {});
  await until(() => connected !== '', `${name} to join`);
  peer.clientId = connected;
  return peer;
}

// Large payloads fill the loopback TCP buffers quickly; 25/s per sender stays
// under the relay's 30/s per-client limit, so nothing is dropped as
// RATE_LIMITED and each sender's messages are numbered without gaps.
const payload = new Uint8Array(256 * 1024);

/** Senders address numbered messages to target until each sees it leave. */
async function floodUntilEvicted(senders: Peer[], target: Peer, ms: number): Promise<void> {
  const timers = senders.map((s) => {
    let n = 0;
    return setInterval(() => {
      if (s.left.some((l) => l.id === target.clientId)) return;
      s.client.sendAppMessage(String(n++), payload, { to: target.clientId });
    }, 40);
  });
  try {
    await until(() => senders.every((s) => s.left.some((l) => l.id === target.clientId)), 'senders to see the stuck peer leave', ms);
  } finally {
    timers.forEach(clearInterval);
  }
}

/** What arrived from each sender is 0, 1, 2, ... with nothing skipped. */
function expectUnbrokenPrefixes(received: Peer['received'], senders: Peer[]) {
  for (const s of senders) {
    const kinds = received.filter((r) => r.from === s.clientId).map((r) => r.kind);
    expect(kinds).toEqual(kinds.map((_, i) => String(i)));
  }
}

describe('slow consumer against a real relay', () => {
  it('a client that falls behind and catches up gets SessionEnded, then the relay closes the socket', async () => {
    const relayUrl = inject('relayUrl');
    const proxy = await stallProxy(relayUrl);
    cleanup.push(() => proxy.stop());
    const room = `design:${crypto.randomUUID()}`;
    const stuck = await join(proxy.url, room, 'stuck');
    const senders = [await join(relayUrl, room, 's1'), await join(relayUrl, room, 's2'), await join(relayUrl, room, 's3')];

    proxy.pause();
    await floodUntilEvicted(senders, stuck, 15000);
    proxy.resume();

    await until(() => stuck.disconnected && proxy.relayGone(), 'the stuck client to be closed', 5000);
    expect(stuck.ended).toEqual(['slow_consumer']);
    expect(proxy.relayCloses[0]).toEqual({ code: 1000, reason: 'stream_end' });
    expect(stuck.received.length).toBeGreaterThan(0);
    expectUnbrokenPrefixes(stuck.received, senders);
  }, 30000);

  it('a client that stops reading for good is dropped after the write timeout', async () => {
    const relayUrl = inject('relayUrl');
    const proxy = await stallProxy(relayUrl);
    cleanup.push(() => proxy.stop());
    const room = `design:${crypto.randomUUID()}`;
    const stuck = await join(proxy.url, room, 'stuck');
    const senders = [await join(relayUrl, room, 's1'), await join(relayUrl, room, 's2'), await join(relayUrl, room, 's3')];

    proxy.pause();
    await floodUntilEvicted(senders, stuck, 15000);

    // Past the relay's 10s WriteTimeout. A paused socket can't see the relay
    // hang up, so read what's left without passing it on: if the relay already
    // gave up, the connection ends at once and carries no close frame. (A relay
    // that was still waiting would now get through and send stream_end.)
    await new Promise((r) => setTimeout(r, 12000));
    proxy.discard();
    await until(() => proxy.relayGone(), 'the relay end of the connection to be gone', 1000);
    expect(proxy.relayCloses).toEqual([]);
    await until(() => stuck.disconnected, 'the stuck client to see the disconnect');
    expect(stuck.ended).toEqual([]);
  }, 45000);
});
