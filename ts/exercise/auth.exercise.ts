import { request } from 'node:http';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { CollabClient } from '../src/CollabClient.js';
import { testKey, type TestKey } from '../interop/jwt.js';
import { startRelay, type Relay } from '../interop/relay.js';

// Mission #53: a relay binary started with RELAY_AUTH_REQUIRED=true and a
// signing key lets a CollabClient with a valid JWT into the rooms its claim
// allows, refuses it others, and refuses an upgrade with no token.

/** The HTTP status of a WebSocket upgrade, which a WebSocket client can't see. */
function upgradeStatus(port: number, path: string): Promise<number> {
  return new Promise((ok, fail) => {
    const req = request({
      host: '127.0.0.1',
      port,
      path,
      headers: { Connection: 'Upgrade', Upgrade: 'websocket', 'Sec-WebSocket-Version': '13', 'Sec-WebSocket-Key': Buffer.from('exercise-key-16b').toString('base64') },
    });
    req.on('upgrade', (res, socket) => {
      socket.destroy();
      ok(res.statusCode ?? 0);
    });
    req.on('response', (res) => {
      res.resume();
      ok(res.statusCode ?? 0);
    });
    req.on('error', fail);
    req.end();
  });
}

interface Outcome {
  joined: string;
  errors: string[];
  wsErrors: number;
  closed: boolean;
}

/** Joins sessionId and reports what the relay did within ms. */
function attempt(relayUrl: string, sessionId: string, tok: string | undefined, ms = 2000): Promise<Outcome> {
  const out: Outcome = { joined: '', errors: [], wsErrors: 0, closed: false };
  const client = new CollabClient({
    token: tok,
    onConnect: (id) => (out.joined = id),
    onErrorEvent: (code) => out.errors.push(code),
    onError: () => out.wsErrors++,
  });
  client.connect(relayUrl, sessionId, 'alice', {});
  return new Promise((ok) => {
    const start = Date.now();
    const tick = () => {
      if (out.joined || out.errors.length || out.wsErrors || Date.now() - start > ms) {
        client.disconnect();
        return ok(out);
      }
      setTimeout(tick, 10);
    };
    tick();
  });
}

function describeOutcome(o: Outcome): string {
  if (o.joined) return `RoomJoined (clientId ${o.joined})`;
  if (o.errors.length) return `join error ${o.errors.join(',')}`;
  if (o.wsErrors) return 'upgrade failed';
  return 'nothing within 2s';
}

describe('exercise-auth: relay binary with RELAY_AUTH_REQUIRED=true', () => {
  let relay: Relay;
  let key: TestKey;
  const token = (rooms: string[]) => key.sign('alice', 3600, { rooms });

  beforeAll(async () => {
    key = testKey('exercise-app', 'exercise-issuer');
    relay = await startRelay(key.env);
  });

  afterAll(() => {
    relay?.stop();
    key?.remove();
  });

  it('no token: the upgrade is a 401', async () => {
    const status = await upgradeStatus(relay.port, '/ws/v1/room-a/sync');
    const o = await attempt(relay.url, 'room-a', undefined);
    console.log(`no token            → HTTP ${status}; CollabClient: ${describeOutcome(o)}`);
    expect(status).toBe(401);
    expect(o.joined).toBe('');
  });

  it('room outside the rooms claim: a join error', async () => {
    const o = await attempt(relay.url, 'other-room', token(['room-*']));
    console.log(`off-claim room      → ${describeOutcome(o)}`);
    expect(o.errors).toEqual(['ROOM_FORBIDDEN']);
  });

  it('room the claim allows: RoomJoined', async () => {
    const o = await attempt(relay.url, 'room-a', token(['room-*']));
    console.log(`allowed room        → ${describeOutcome(o)}`);
    expect(o.joined).not.toBe('');
  });
});
