import { connect, createServer, type Server, type Socket } from 'node:net';

// A browser that stops reading can't be made from Node's WebSocket, which has
// no pause. Instead the stuck client talks to the relay through this proxy,
// which can stop reading from the relay so the relay's writes back up on TCP
// the way they would for a stalled tab.

export interface CloseFrame {
  code: number;
  reason: string;
}

export interface StallProxy {
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

/** Listens on a free loopback port and forwards each connection to the relay
 * at relayUrl, with the relay → client direction under the returned controls. */
export function stallProxy(relayUrl: string): Promise<StallProxy> {
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
