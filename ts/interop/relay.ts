import { execFileSync, spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

export interface Relay {
  url: string;
  port: number;
  stop(): void;
}

function freePort(): Promise<number> {
  return new Promise((ok, fail) => {
    const srv = createServer();
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      srv.close(() => (typeof addr === 'object' && addr ? ok(addr.port) : fail(new Error('no port'))));
    });
  });
}

async function waitForHealth(port: number, deadlineMs = 15000): Promise<void> {
  const until = Date.now() + deadlineMs;
  while (Date.now() < until) {
    try {
      if ((await fetch(`http://127.0.0.1:${port}/health`)).ok) return;
    } catch {
      // not listening yet
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`relay did not become healthy on port ${port}`);
}

/** Builds the Go relay from this checkout and starts it on a free port. env
 * is added to the relay's environment; the per-IP and global connection
 * rates are raised since every test client dials from loopback. */
export async function startRelay(env: Record<string, string> = {}): Promise<Relay> {
  const repoRoot = resolve(import.meta.dirname, '../..');
  const dir = mkdtempSync(join(tmpdir(), 'massrelay-interop-'));
  const bin = join(dir, 'relay');
  execFileSync('go', ['build', '-o', bin, '.'], { cwd: repoRoot, stdio: 'inherit' });

  const port = await freePort();
  const relay = spawn(bin, ['-port', String(port)], {
    env: { ...process.env, RELAY_PER_IP_RATE: '1000', RELAY_GLOBAL_RATE: '1000', ...env },
    stdio: ['ignore', 'ignore', 'inherit'],
  });
  const exited = new Promise<never>((_, fail) =>
    relay.once('exit', (code) => fail(new Error(`relay exited with code ${code} before it was healthy`))),
  );
  try {
    await Promise.race([waitForHealth(port), exited]);
  } catch (err) {
    relay.kill();
    rmSync(dir, { recursive: true, force: true });
    throw err;
  }
  exited.catch(() => {});
  return {
    url: `ws://127.0.0.1:${port}`,
    port,
    stop: () => {
      relay.kill();
      rmSync(dir, { recursive: true, force: true });
    },
  };
}
