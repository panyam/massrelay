/** Tiny typed event emitter. No external deps. */
export class TypedEmitter<T extends Record<string, (...args: any[]) => void>> {
  private listeners = new Map<keyof T, Set<Function>>();

  on<K extends keyof T>(event: K, fn: T[K]): () => void {
    let set = this.listeners.get(event);
    if (!set) {
      set = new Set();
      this.listeners.set(event, set);
    }
    set.add(fn);
    return () => this.off(event, fn);
  }

  off<K extends keyof T>(event: K, fn: T[K]): void {
    this.listeners.get(event)?.delete(fn);
  }

  protected emit<K extends keyof T>(event: K, ...args: Parameters<T[K]>): void {
    const set = this.listeners.get(event);
    if (!set) return;
    for (const fn of set) {
      fn(...args);
    }
  }

  /** Remove all listeners. */
  removeAllListeners(): void {
    this.listeners.clear();
  }
}
