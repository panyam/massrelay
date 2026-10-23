import { defineConfig } from 'vitest/config';

// Runs the TS client against a Go relay built from this checkout. Needs Go on
// PATH and a global WebSocket (Node 22+). `npm run test:interop`.
export default defineConfig({
  test: {
    include: ['interop/**/*.interop.ts'],
    globalSetup: ['interop/relay-setup.ts'],
    testTimeout: 15000,
    hookTimeout: 120000,
  },
});
