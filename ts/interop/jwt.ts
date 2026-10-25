import { createHmac } from 'node:crypto';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

/** One HS256 signing key, as a RELAY_AUTH_KEYS_FILE the relay binary loads,
 * and a signer for tokens it accepts. */
export interface TestKey {
  clientId: string;
  issuer: string;
  /** Env for startRelay: auth required, this key, this issuer. */
  env: Record<string, string>;
  /** Signs an access token for subject, valid for ttlSeconds, with extra
   * claims (e.g. rooms) on top. */
  sign(subject: string, ttlSeconds: number, extra?: Record<string, unknown>): string;
  remove(): void;
}

function b64url(s: string): string {
  return Buffer.from(s).toString('base64url');
}

export function testKey(clientId = 'interop-app', issuer = 'interop-issuer'): TestKey {
  const secret = `${clientId}-secret-at-least-32-bytes-long`;
  const dir = mkdtempSync(join(tmpdir(), 'massrelay-keys-'));
  const keysFile = join(dir, 'keys.json');
  writeFileSync(keysFile, JSON.stringify([{ client_id: clientId, alg: 'HS256', key: secret }]));
  return {
    clientId,
    issuer,
    env: { RELAY_AUTH_REQUIRED: 'true', RELAY_AUTH_KEYS_FILE: keysFile, RELAY_AUTH_ISSUER: issuer },
    sign: (subject, ttlSeconds, extra = {}) => {
      const now = Math.floor(Date.now() / 1000);
      const head = b64url(JSON.stringify({ alg: 'HS256', typ: 'JWT' }));
      const body = b64url(JSON.stringify({ sub: subject, client_id: clientId, type: 'access', iss: issuer, iat: now, exp: now + ttlSeconds, ...extra }));
      const sig = createHmac('sha256', secret).update(`${head}.${body}`).digest('base64url');
      return `${head}.${body}.${sig}`;
    },
    remove: () => rmSync(dir, { recursive: true, force: true }),
  };
}
