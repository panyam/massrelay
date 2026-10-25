import type { TestProject } from 'vitest/node';
import { startRelay } from './relay.js';

declare module 'vitest' {
  export interface ProvidedContext {
    relayUrl: string;
  }
}

/** Builds the Go relay from this checkout and runs it for the interop suite. */
export default async function setup(project: TestProject) {
  const relay = await startRelay();
  project.provide('relayUrl', relay.url);
  return () => relay.stop();
}
