import { TypedEmitter } from './EventEmitter.js';
import { CollabClient } from './CollabClient.js';
import type { SyncAdapter, OutgoingUpdate } from './SyncAdapter.js';
import { encryptPayload, decryptPayload } from './crypto.js';
import type { PeerInfoJson, CollabEvent } from './gen/massrelay/v1/models/collab_pb.js';

// ─── Types ───

export interface CollabEngineConfig {
  adapter?: SyncAdapter | null;
  client: CollabClient;
  outgoingDebounceMs?: number;
  cursorThrottleMs?: number;
  timers?: TimerProvider;
}

export interface TimerProvider {
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(handle: unknown): void;
}

export interface CollabEngineState {
  phase: 'disconnected' | 'connecting' | 'connected';
  isInitialized: boolean;
  clientId: string;
  sessionId: string;
  isOwner: boolean;
  ownerClientId: string;
  peers: ReadonlyMap<string, PeerInfoJson>;
  error: string | null;
  roomEncrypted: boolean;
  maxPeers: number;
  roomTitle: string;
}

export interface ConnectParams {
  relayUrl: string;
  sessionId: string;
  username: string;
  metadata: Record<string, string>;
  isOwner?: boolean;
  browserId?: string;
  clientHint?: string;
  encrypted?: boolean;
  title?: string;
}

export type CollabEngineEvents = {
  [K in string]: (...args: any[]) => void;
} & {
  stateChange: (state: CollabEngineState) => void;
  peerJoined: (peer: PeerInfoJson) => void;
  peerLeft: (clientId: string) => void;
  sessionEnded: (reason: string) => void;
  credentialsChanged: (reason: string) => void;
  ownerChanged: (newOwnerClientId: string) => void;
  titleChanged: (title: string) => void;
  error: (error: { code?: string; message: string }) => void;
};

const INITIAL_STATE: CollabEngineState = {
  phase: 'disconnected',
  isInitialized: false,
  clientId: '',
  sessionId: '',
  isOwner: false,
  ownerClientId: '',
  peers: new Map(),
  error: null,
  roomEncrypted: false,
  maxPeers: 0,
  roomTitle: '',
};

// ─── Engine ───

export class CollabEngine extends TypedEmitter<CollabEngineEvents> {
  private _state: CollabEngineState = { ...INITIAL_STATE, peers: new Map() };
  private _adapter: SyncAdapter | null;
  private _client: CollabClient;
  private _encryptionKey: CryptoKey | null = null;
  private _debounceMs: number;
  private _cursorThrottleMs: number;
  private _timers: TimerProvider;

  private _debounceTimer: unknown = null;
  private _cursorTimer: unknown = null;
  private _cursorPending = false;
  private _initRequestSent = false;
  private _disposed = false;

  // Store connect params for callback use
  private _connectParams: ConnectParams | null = null;

  constructor(config: CollabEngineConfig) {
    super();
    this._adapter = config.adapter ?? null;
    this._client = config.client;
    this._debounceMs = config.outgoingDebounceMs ?? 100;
    this._cursorThrottleMs = config.cursorThrottleMs ?? 50;
    this._timers = config.timers ?? {
      setTimeout: (fn, ms) => setTimeout(fn, ms),
      clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
    };
  }

  // ─── Public API ───

  get state(): CollabEngineState {
    return this._state;
  }

  get client(): CollabClient {
    return this._client;
  }

  setAdapter(adapter: SyncAdapter | null): void {
    this._adapter = adapter;
    // If we're already connected and just got an adapter, check if we need scene init
    if (adapter && this._state.phase === 'connected' && !this._state.isInitialized && !this._initRequestSent) {
      this._triggerSceneInit();
    }
  }

  setEncryptionKey(key: CryptoKey | null): void {
    this._encryptionKey = key;
  }

  connect(params: ConnectParams): void {
    this._connectParams = params;
    this._updateState({ phase: 'connecting', error: null });
    this._wireClient();
    this._client.connect(
      params.relayUrl,
      params.sessionId,
      params.username,
      params.metadata,
      params.isOwner ?? false,
      params.browserId ?? '',
      params.clientHint ?? '',
      params.encrypted ?? false,
      params.title ?? '',
    );
  }

  disconnect(): void {
    this._client.disconnect();
    // onDisconnect callback will handle state reset
  }

  dispose(): void {
    this._disposed = true;
    if (this._state.phase !== 'disconnected') {
      this.disconnect();
    }
    this._clearTimers();
    this.removeAllListeners();
  }

  notifyLocalChange(): void {
    if (this._debounceTimer !== null) this._timers.clearTimeout(this._debounceTimer);
    this._debounceTimer = this._timers.setTimeout(() => this._flushOutgoing(), this._debounceMs);
  }

  notifyCursorMove(): void {
    if (!this._adapter || this._state.phase !== 'connected') return;
    if (this._cursorPending) return;

    this._cursorPending = true;
    this._cursorTimer = this._timers.setTimeout(() => {
      this._cursorPending = false;
      const cursor = this._adapter?.getCursorData();
      if (cursor) {
        this._client.send({ cursorUpdate: cursor });
      }
    }, this._cursorThrottleMs);
  }

  notifyCredentialsChanged(reason: string): void {
    this._client.send({ credentialsChanged: { reason } });
  }

  notifyTitleChanged(title: string): void {
    this._client.send({ titleChanged: { title } });
  }

  // ─── Private ───

  private _wireClient(): void {
    const client = this._client;
    const params = this._connectParams!;

    client.options.onConnect = (clientId: string) => {
      this._updateState({ phase: 'connected', clientId, error: null });
    };

    client.options.onPeerJoined = (peer: PeerInfoJson) => {
      const id = peer.clientId || '';
      if (!id) return;
      const peers = new Map(this._state.peers);
      peers.set(id, peer);
      this._updateState({ peers });
      this.emit('peerJoined', peer);
    };

    client.options.onPeerLeft = (clientId: string) => {
      const peers = new Map(this._state.peers);
      peers.delete(clientId);
      this._updateState({ peers });
      this._adapter?.removePeerCursor(clientId);
      this.emit('peerLeft', clientId);
    };

    client.options.onError = (err: Error) => {
      this._updateState({ error: err.message });
    };

    client.options.onErrorEvent = (code: string, message: string) => {
      this._updateState({ error: `${code}: ${message}`, phase: 'disconnected' });
      this.emit('error', { code, message });
    };

    client.options.onCredentialsChanged = (reason: string) => {
      this._resetState();
      const errorMsg = reason === 'password_removed'
        ? 'Encryption was removed — please reconnect'
        : 'Password changed — please reconnect with the new password';
      this._updateState({ error: errorMsg });
      this.emit('credentialsChanged', reason);
    };

    client.options.onTitleChanged = (newTitle: string) => {
      this._updateState({ roomTitle: newTitle });
      this.emit('titleChanged', newTitle);
    };

    client.options.onDisconnect = () => {
      this._resetState();
    };

    client.options.onSessionEnded = (reason: string) => {
      this._resetState();
      this._updateState({ error: 'The owner ended the sharing session' });
      this.emit('sessionEnded', reason || 'The owner ended the sharing session');
    };

    client.options.onOwnerChanged = (newOwnerClientId: string) => {
      this._updateState({
        ownerClientId: newOwnerClientId,
        isOwner: this._state.clientId === newOwnerClientId,
      });
      this.emit('ownerChanged', newOwnerClientId);
    };

    client.options.onEvent = (event: CollabEvent) => {
      // Extract room info from RoomJoined
      if (event.event.case === 'roomJoined') {
        const rj = event.event.value;
        const room = rj.room;
        const ownerClientId = room?.ownerClientId || '';
        const returnedSessionId = room?.sessionId || params.sessionId;
        this._updateState({
          sessionId: returnedSessionId,
          ownerClientId,
          isOwner: this._state.clientId === ownerClientId || (params.isOwner ?? false),
          roomEncrypted: !!room?.encrypted,
          maxPeers: rj.maxPeers,
          roomTitle: room?.title || '',
        });
      }

      // Trigger scene init after roomJoined (all peers now in map)
      if (event.event.case === 'roomJoined' && !this._state.isInitialized && !this._initRequestSent && this._adapter) {
        this._triggerSceneInit();
      }

      // Route sync events
      this._handleSyncEvent(event);
    };
  }

  private async _handleSyncEvent(event: CollabEvent): Promise<void> {
    const adapter = this._adapter;

    switch (event.event.case) {
      case 'sceneUpdate': {
        if (!adapter) return;
        const su = event.event.value;
        if (this._encryptionKey && su.elements.length > 0) {
          try {
            for (const el of su.elements) {
              if (el.data) {
                el.data = await decryptPayload(this._encryptionKey, el.data);
              }
            }
          } catch {
            return; // wrong key
          }
        }
        adapter.applyRemote(event.fromClientId, {
          elements: su.elements.map(el => ({
            id: el.id,
            version: el.version,
            versionNonce: el.versionNonce,
            data: el.data,
            deleted: el.deleted,
          })),
        });
        break;
      }

      case 'textUpdate': {
        if (!adapter) return;
        const tu = event.event.value;
        let text = tu.text;
        if (this._encryptionKey && typeof text === 'string') {
          try {
            text = await decryptPayload(this._encryptionKey, text);
          } catch {
            return;
          }
        }
        adapter.applyRemote(event.fromClientId, {
          text,
          version: tu.version,
          cursorPosition: tu.cursorPosition,
        });
        break;
      }

      case 'cursorUpdate': {
        if (!adapter || !event.fromClientId) return;
        const cu = event.event.value;
        const peer = this._state.peers.get(event.fromClientId);
        adapter.applyRemoteCursor({
          clientId: event.fromClientId,
          username: peer?.username || event.fromClientId.slice(0, 6),
          x: cu.x,
          y: cu.y,
          tool: cu.tool,
          button: cu.button,
          selectedElementIds: cu.selectedElementIds,
        });
        break;
      }

      case 'sceneInitResponse': {
        if (!adapter) return;
        let payload = event.event.value.payload || '{}';
        if (this._encryptionKey && payload !== '{}') {
          try {
            payload = await decryptPayload(this._encryptionKey, payload);
          } catch {
            return;
          }
        }
        adapter.applySceneInit(payload);
        this._updateState({ isInitialized: true });
        break;
      }

      case 'sceneInitRequest': {
        if (!adapter || !event.fromClientId) return;
        const fromClientId = event.fromClientId;
        const shouldRespond = this._state.isOwner || (() => {
          const myClientId = this._state.clientId;
          if (!myClientId) return false;
          const peerIds = Array.from(this._state.peers.keys());
          const candidates = peerIds.filter(id => id !== fromClientId);
          if (candidates.length === 0) return false;
          candidates.sort();
          return candidates[0] === myClientId;
        })();

        if (shouldRespond) {
          let snapshot = adapter.getSceneSnapshot();
          if (this._encryptionKey) {
            try {
              snapshot = await encryptPayload(this._encryptionKey, snapshot);
            } catch {
              return;
            }
          }
          this._client.send({ sceneInitResponse: { payload: snapshot } });
        }
        break;
      }

      case 'peerLeft':
        // Already handled in onPeerLeft callback, but adapter cursor cleanup
        // is also done there, so this is a no-op for the sync layer.
        break;
    }
  }

  private async _flushOutgoing(): Promise<void> {
    if (!this._adapter || this._state.phase !== 'connected') return;

    const update = this._adapter.computeOutgoing();
    if (!update) return;

    if (this._encryptionKey) {
      try {
        const payload = update.payload as any;
        if (update.type === 'sceneUpdate' && payload.elements) {
          for (const el of payload.elements) {
            if (el.data) {
              el.data = await encryptPayload(this._encryptionKey, el.data);
            }
          }
        } else if (update.type === 'textUpdate' && typeof payload.text === 'string') {
          payload.text = await encryptPayload(this._encryptionKey, payload.text);
        }
      } catch {
        return;
      }
    }

    this._client.send({ [update.type]: update.payload });
  }

  private _triggerSceneInit(): void {
    if (this._state.peers.size <= 1) {
      this._updateState({ isInitialized: true });
    } else {
      this._client.send({ sceneInitRequest: {} });
      this._initRequestSent = true;
    }
  }

  private _resetState(): void {
    this._clearTimers();
    this._initRequestSent = false;
    this._updateState({
      phase: 'disconnected',
      isInitialized: false,
      clientId: '',
      sessionId: '',
      isOwner: false,
      ownerClientId: '',
      peers: new Map(),
      roomEncrypted: false,
      maxPeers: 0,
      roomTitle: '',
    });
  }

  private _clearTimers(): void {
    if (this._debounceTimer) {
      this._timers.clearTimeout(this._debounceTimer);
      this._debounceTimer = null;
    }
    if (this._cursorTimer) {
      this._timers.clearTimeout(this._cursorTimer);
      this._cursorTimer = null;
    }
  }

  private _updateState(partial: Partial<CollabEngineState>): void {
    this._state = { ...this._state, ...partial };
    this.emit('stateChange', this._state);
  }
}
