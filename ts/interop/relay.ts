import { execFileSync, execSync, spawn, type ChildProcess } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

export interface Relay {
  /** WebSocket base, e.g. ws://127.0.0.1:1234 or wss://localhost. */
  url: string;
  /** HTTP base for the same relay (http:// or https://), for /health and the
   * REST routes. */
  httpUrl: string;
  /** Kills the relay and starts it again on the same port with the same env,
   * resolving once it is healthy. Every room is gone afterwards. */
  restart(): Promise<void>;
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

async function waitForHealth(httpUrl: string, deadlineMs = 15000): Promise<void> {
  const until = Date.now() + deadlineMs;
  while (Date.now() < until) {
    try {
      if ((await fetch(`${httpUrl}/health`)).ok) return;
    } catch {
      // not listening yet
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`relay did not become healthy at ${httpUrl}`);
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
  const httpUrl = `http://127.0.0.1:${port}`;
  const launch = async () => {
    const proc = spawn(bin, ['-port', String(port)], {
      env: { ...process.env, RELAY_PER_IP_RATE: '1000', RELAY_GLOBAL_RATE: '1000', ...env },
      stdio: ['ignore', 'ignore', 'inherit'],
    });
    const exited = new Promise<never>((_, fail) =>
      proc.once('exit', (code) => fail(new Error(`relay exited with code ${code} before it was healthy`))),
    );
    exited.catch(() => {});
    try {
      await Promise.race([waitForHealth(httpUrl), exited]);
    } catch (err) {
      proc.kill();
      throw err;
    }
    return proc;
  };
  const kill = (proc: ChildProcess) =>
    new Promise<void>((ok) => {
      if (proc.exitCode !== null || proc.signalCode !== null) return ok();
      proc.once('exit', () => ok());
      proc.kill();
    });

  let relay: ChildProcess;
  try {
    relay = await launch();
  } catch (err) {
    rmSync(dir, { recursive: true, force: true });
    throw err;
  }
  return {
    url: `ws://127.0.0.1:${port}`,
    httpUrl,
    restart: async () => {
      await kill(relay);
      relay = await launch();
    },
    stop: () => {
      relay.kill();
      rmSync(dir, { recursive: true, force: true });
    },
  };
}

/** The relay a mission exercise runs against. By default it builds and starts
 * the binary with env, as startRelay does. With MASSRELAY_RELAY_URL set (e.g.
 * wss://localhost, the `make prod-up` stack) it uses that relay instead and
 * ignores env, since the stack was started with its own config; restart()
 * then runs MASSRELAY_RELAY_RESTART and waits for /health. That relay keeps
 * its own rate limits, which startRelay raises. */
export async function relayForExercise(env: Record<string, string> = {}): Promise<Relay> {
  const url = process.env.MASSRELAY_RELAY_URL;
  if (!url) return startRelay(env);
  const restartCmd = process.env.MASSRELAY_RELAY_RESTART;
  const httpUrl = url.replace(/^ws/i, 'http').replace(/\/+$/, '');
  await waitForHealth(httpUrl);
  return {
    url: url.replace(/\/+$/, ''),
    httpUrl,
    restart: async () => {
      if (!restartCmd) throw new Error('MASSRELAY_RELAY_RESTART is not set, so this relay cannot be restarted');
      execSync(restartCmd, { stdio: 'inherit' });
      await waitForHealth(httpUrl, 60000);
    },
    stop: () => {},
  };
}
