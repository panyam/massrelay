import { defineConfig } from 'vitest/config';

// Mission exercises: each starts its own relay binary from this checkout and
// prints what the relay did. Needs Go on PATH and Node 22+. `make exercise-auth`.
export default defineConfig({
  test: {
    include: ['exercise/**/*.exercise.ts'],
    testTimeout: 15000,
    hookTimeout: 120000,
    fileParallelism: false,
    reporters: ['verbose'],
  },
});
