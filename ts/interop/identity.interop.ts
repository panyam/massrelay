import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { CollabClient } from '../src/CollabClient.js';
import type { PeerInfoJson } from '../src/gen/massrelay/v1/models/collab_pb.js';
import { testKey, type TestKey } from './jwt.js';
import { startRelay, type Relay } from './relay.js';

// What the mcpkit room bridge relies on, through the relay binary with keys
// loaded from RELAY_AUTH_KEYS_FILE: each peer's identity is the subject the
// relay validated, and a page answers an addressed call only after checking
// the caller's getPeer(from)?.identity?.subject.

type Received = { kind: string; text: string; from: string; to: string };

interface Peer {
  client: CollabClient;
  clientId: string;
  joined: PeerInfoJson[];
  received: Received[];
}

const open: CollabClient[] = [];

afterEach(() => {
  while (open.length) open.pop()!.disconnect();
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

const settle = () => new Promise((r) => setTimeout(r, 200));
const text = (s: string) => new TextEncoder().encode(s);

async function join(relay: Relay, room: string, name: string, token?: string): Promise<Peer> {
  const peer = { joined: [], received: [] } as unknown as Peer;
  let connected = '';
  peer.client = new CollabClient({
    token,
    onConnect: (id) => (connected = id),
    onPeerJoined: (p) => peer.joined.push(p),
    onAppMessage: (kind, payload, from, to) => peer.received.push({ kind, text: new TextDecoder().decode(payload), from, to }),
  });
  open.push(peer.client);
  peer.client.connect(relay.url, room, name, {});
  await until(() => connected !== '', `${name} to join`);
  peer.clientId = connected;
  return peer;
}

// The page side of the bridge: answer a call only from a peer whose validated
// subject is trusted, addressed back to the caller.
function answerCallsFrom(page: Peer, trusted: string) {
  page.client.options.onAppMessage = (kind, payload, from) => {
    page.received.push({ kind, text: new TextDecoder().decode(payload), from, to: page.clientId });
    if (kind !== 'call' || page.client.getPeer(from)?.identity?.subject !== trusted) return;
    page.client.sendAppMessage('reply', text(`ran ${new TextDecoder().decode(payload)}`), { to: from });
  };
}

describe('peer identity through the relay binary, auth required', () => {
  let relay: Relay;
  let key: TestKey;

  beforeAll(async () => {
    key = testKey();
    relay = await startRelay(key.env);
  });

  afterAll(() => {
    relay?.stop();
    key?.remove();
  });

  it("gives a later joiner's onPeerJoined and getPeer the subject from its token", async () => {
    const room = `design:${crypto.randomUUID()}`;
    const bob = await join(relay, room, 'bob', key.sign('bob', 3600));
    const alice = await join(relay, room, 'alice', key.sign('alice', 3600));

    await until(() => bob.joined.some((p) => p.clientId === alice.clientId), 'bob to see alice join');
    const seen = bob.joined.find((p) => p.clientId === alice.clientId)!;
    expect(seen.identity).toEqual({ subject: 'alice', clientId: key.clientId });
    expect(bob.client.getPeer(alice.clientId)?.identity).toEqual({ subject: 'alice', clientId: key.clientId });
  });

  it('carries identity in the roster a joiner gets for peers already in the room', async () => {
    const room = `design:${crypto.randomUUID()}`;
    const bob = await join(relay, room, 'bob', key.sign('bob', 3600));
    const alice = await join(relay, room, 'alice', key.sign('alice', 3600));

    expect(alice.client.getPeer(bob.clientId)?.identity).toEqual({ subject: 'bob', clientId: key.clientId });
    expect(alice.joined.find((p) => p.clientId === bob.clientId)?.identity?.subject).toBe('bob');
  });

  it("answers an addressed call from the page's own user, and the reply reaches only the caller", async () => {
    const room = `design:${crypto.randomUUID()}`;
    const page = await join(relay, room, 'page', key.sign('alice', 3600));
    answerCallsFrom(page, 'alice');
    const agent = await join(relay, room, 'agent', key.sign('alice', 3600));
    const bystander = await join(relay, room, 'bystander', key.sign('carol', 3600));

    agent.client.sendAppMessage('call', text('select_part'), { to: page.clientId });
    await until(() => agent.received.length === 1, 'the reply');
    await settle();

    expect(page.received.map((m) => [m.kind, m.from])).toEqual([['call', agent.clientId]]);
    expect(agent.received).toEqual([{ kind: 'reply', text: 'ran select_part', from: page.clientId, to: agent.clientId }]);
    expect(bystander.received).toEqual([]);
  });

  it("ignores a call from a peer whose token names another subject", async () => {
    const room = `design:${crypto.randomUUID()}`;
    const page = await join(relay, room, 'page', key.sign('alice', 3600));
    answerCallsFrom(page, 'alice');
    const intruder = await join(relay, room, 'alice', key.sign('mallory', 3600));

    intruder.client.sendAppMessage('call', text('select_part'), { to: page.clientId });
    await until(() => page.received.length === 1, 'the call to arrive');
    await settle();

    expect(page.client.getPeer(intruder.clientId)?.identity?.subject).toBe('mallory');
    expect(intruder.received).toEqual([]);
  });
});

describe('peer identity through the relay binary, auth optional', () => {
  let relay: Relay;
  let key: TestKey;

  beforeAll(async () => {
    key = testKey();
    relay = await startRelay({ ...key.env, RELAY_AUTH_REQUIRED: 'false' });
  });

  afterAll(() => {
    relay?.stop();
    key?.remove();
  });

  it('gives a peer without a token no identity, so the page ignores its calls', async () => {
    const room = `design:${crypto.randomUUID()}`;
    const page = await join(relay, room, 'page', key.sign('alice', 3600));
    answerCallsFrom(page, 'alice');
    const anon = await join(relay, room, 'alice');
    const agent = await join(relay, room, 'agent', key.sign('alice', 3600));

    expect(page.client.getPeer(anon.clientId)?.identity).toBeUndefined();
    expect(page.client.getPeer(agent.clientId)?.identity?.subject).toBe('alice');

    anon.client.sendAppMessage('call', text('select_part'), { to: page.clientId });
    agent.client.sendAppMessage('call', text('select_part'), { to: page.clientId });
    await until(() => agent.received.length === 1, 'the reply to the authed agent');
    await settle();

    expect(page.received.map((m) => m.from)).toEqual([anon.clientId, agent.clientId]);
    expect(anon.received).toEqual([]);
  });
});
