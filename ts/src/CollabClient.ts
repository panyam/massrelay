import { GRPCWSClient } from '@panyam/servicekit-client';
import type { PeerInfo } from './gen/massrelay/v1/models/collab_pb.js';
import { resolveRelayUrl } from './url-params.js';

export interface CollabClientOptions {
  onEvent?: (event: any) => void;
  onPeerJoined?: (peer: PeerInfo) => void;
  onPeerLeft?: (clientId: string) => void;
  onError?: (error: Error) => void;
  onConnect?: (clientId: string) => void;
  onDisconnect?: () => void;
  onSessionEnded?: (reason: string) => void;
  onOwnerChanged?: (newOwnerClientId: string) => void;
  onCredentialsChanged?: (reason: string) => void;
  onTitleChanged?: (title: string) => void;
  /** Called when relay returns an ErrorEvent (e.g. ROOM_FULL). */
  onErrorEvent?: (code: string, message: string) => void;
  maxRetries?: number;
  /** Factory for creating GRPCWSClient instances. Defaults to `() => new GRPCWSClient()`.
   *  Override in tests with `GRPCWSClient.createMock()`. */
  _grpcFactory?: () => GRPCWSClient;
}

/**
 * Framework-agnostic WebSocket client for the collab relay.
 * Uses @panyam/servicekit-client GRPCWSClient for envelope protocol
 * and auto ping/pong. Adds reconnect with exponential backoff on top.
 */
export class CollabClient {
  private grpc: GRPCWSClient | null = null;
  private _clientId: string = '';
  private _isConnected: boolean = false;
  private _isConnecting: boolean = false;
  private _isOwner: boolean = false;
  private _browserId: string = '';
  private _clientHint: string = '';
  private _relayUrl: string = '';
  private _sessionId: string = '';
  private _username: string = '';
  private _metadata: Record<string, string> = {};
  private _title: string = '';
  private _encrypted: boolean = false;
  private _maxPeers: number = 0;
  private _roomEncrypted: boolean = false;
  /** Callback options — public so CollabEngine can wire callbacks after construction. */
  options: CollabClientOptions;
  private retryCount: number = 0;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private explicitDisconnect: boolean = false;
  private maxRetries: number;
  private boundBeforeUnload: (() => void) | null = null;

  constructor(options: CollabClientOptions = {}) {
    this.options = options;
    this.maxRetries = options.maxRetries ?? 5;
  }

  get clientId(): string { return this._clientId; }
  get sessionId(): string { return this._sessionId; }
  get isConnected(): boolean { return this._isConnected; }
  get isConnecting(): boolean { return this._isConnecting; }
  get isOwner(): boolean { return this._isOwner; }
  get maxPeers(): number { return this._maxPeers; }
  get roomEncrypted(): boolean { return this._roomEncrypted; }
  get title(): string { return this._title; }

  connect(relayUrl: string, sessionId: string, username: string, metadata: Record<string, string>, isOwner: boolean = false, browserId: string = '', clientHint: string = '', encrypted: boolean = false, title: string = ''): void {
    if (this._isConnected) {
      throw new Error('Already connected');
    }

    this._relayUrl = relayUrl;
    this._sessionId = sessionId;
    this._username = username || ('Anon-' + Math.random().toString(36).slice(2, 6));
    this._metadata = metadata;
    this._title = title;
    this._isOwner = isOwner;
    this._browserId = browserId;
    this._clientHint = clientHint;
    this._encrypted = encrypted;
    this._isConnecting = true;
    this.explicitDisconnect = false;
    this.retryCount = 0;

    // Ensure cleanup on page unload (refresh, tab close, navigation)
    if (typeof window !== 'undefined') {
      this.boundBeforeUnload = () => this.disconnect();
      window.addEventListener('beforeunload', this.boundBeforeUnload);
    }

    this.openWebSocket();
  }

  disconnect(): void {
    this.explicitDisconnect = true;
    if (this.boundBeforeUnload && typeof window !== 'undefined') {
      window.removeEventListener('beforeunload', this.boundBeforeUnload);
      this.boundBeforeUnload = null;
    }
    if (this.retryTimer) {
      clearTimeout(this.retryTimer);
      this.retryTimer = null;
    }
    if (!this.grpc) return;

    const wasConnected = this._isConnected;
    const grpc = this.grpc;

    // Send LeaveRoom before closing
    if (wasConnected) {
      grpc.send({ leave: { reason: 'user disconnected' } });
    }

    // Reset state BEFORE close so that handleConnectionClosed (triggered by
    // onClose) sees _isConnected=false and becomes a no-op.
    this.resetState();
    grpc.close();

    // Fire onDisconnect synchronously so the caller can clear state immediately.
    if (wasConnected) {
      this.options.onDisconnect?.();
    }
  }

  send(action: Record<string, unknown>): void {
    if (!this._isConnected || !this.grpc) {
      throw new Error('Not connected');
    }
    this.grpc.send({
      ...action,
      clientId: this._clientId,
      timestamp: Date.now(),
    });
  }

  private openWebSocket(): void {
    const resolved = resolveRelayUrl(this._relayUrl);
    const wsSessionId = this._sessionId || '_new';
    const url = `${resolved}/ws/v1/${wsSessionId}/sync`;
    this.grpc = this.options._grpcFactory ? this.options._grpcFactory() : new GRPCWSClient();

    // GRPCWSClient.onMessage receives data already unwrapped from the
    // servicekit envelope ({type:"data", data:...} → just the data).
    this.grpc.onMessage = (data: any) => {
      this.handleEvent(data);
    };

    this.grpc.onClose = () => {
      this.handleConnectionClosed();
    };

    this.grpc.onError = (err: string) => {
      this.options.onError?.(new Error(err));
    };

    // connect() is Promise-based — send JoinRoom once WS is open.
    // Messages use standard protobuf JSON format (field names at top level
    // for oneof, camelCase for field names) so the Go relay can parse them
    // with protojson.Unmarshal.
    this.grpc.connect(url).then(() => {
      this.grpc?.send({
        join: {
          sessionId: this._sessionId,
          username: this._username,
          metadata: this._metadata,
          clientType: 'browser',
          isOwner: this._isOwner,
          browserId: this._browserId,
          clientHint: this._clientHint,
          protocolVersion: 2,
          encrypted: this._encrypted,
          title: this._title,
        },
      });
    }).catch(() => {
      // Error already dispatched via grpc.onError
    });
  }

  private handleEvent(data: any): void {
    const eventKeys = Object.keys(data).filter(k => k !== 'eventId' && k !== 'fromClientId' && k !== 'serverTimestamp');
    console.log('[COLLAB] Received event:', eventKeys.join(','), 'from:', data.fromClientId);
    this.options.onEvent?.(data);

    // Standard protobuf JSON: oneof fields appear at the top level
    // e.g. { "roomJoined": { "clientId": "c1", ... } }
    if (data.error) {
      // Graceful error from relay (e.g. ROOM_FULL, PROTOCOL_VERSION_TOO_OLD)
      this.options.onErrorEvent?.(data.error.code || '', data.error.message || '');
      this.explicitDisconnect = true; // Don't auto-reconnect on graceful rejection
      this.grpc?.close();
      this.resetState();
      return;
    }

    if (data.roomJoined) {
      this._clientId = data.roomJoined.clientId;
      // Capture relay-generated sessionId (may differ from what we sent)
      if (data.roomJoined.sessionId) {
        this._sessionId = data.roomJoined.sessionId;
      }
      this._maxPeers = data.roomJoined.maxPeers || 0;
      this._roomEncrypted = !!data.roomJoined.encrypted;
      this._title = data.roomJoined.title || '';
      this._isConnected = true;
      this._isConnecting = false;
      this.retryCount = 0;
      this.options.onConnect?.(this._clientId);

      // Add self as a peer (server doesn't include joining client in peers list)
      this.options.onPeerJoined?.({
        clientId: this._clientId,
        username: this._username,
        avatarUrl: '',
        clientType: 'browser',
        isActive: true,
      } as PeerInfo);

      // Add existing peers already in the room
      if (data.roomJoined.peers) {
        for (const peer of data.roomJoined.peers) {
          this.options.onPeerJoined?.(peer);
        }
      }
    } else if (data.peerJoined) {
      this.options.onPeerJoined?.(data.peerJoined.peer);
    } else if (data.peerLeft) {
      this.options.onPeerLeft?.(data.peerLeft.clientId);
    } else if (data.sessionEnded) {
      this.options.onSessionEnded?.(data.sessionEnded.reason || '');
      this.explicitDisconnect = true; // Don't reconnect
      this.grpc?.close();
      this.resetState();
    } else if (data.ownerChanged) {
      const newOwnerId = data.ownerChanged.newOwnerClientId;
      this._isOwner = newOwnerId === this._clientId;
      this.options.onOwnerChanged?.(newOwnerId);
    } else if (data.credentialsChanged) {
      this.options.onCredentialsChanged?.(data.credentialsChanged.reason || '');
    } else if (data.titleChanged) {
      this._title = data.titleChanged.title || '';
      this.options.onTitleChanged?.(this._title);
    }
  }

  private handleConnectionClosed(): void {
    const wasConnected = this._isConnected;
    this._isConnected = false;
    this._isConnecting = false;

    if (wasConnected) {
      this.options.onDisconnect?.();
    }

    // Auto-reconnect disabled for now — reconnecting with stale session
    // params after a server restart creates phantom sessions. The user can
    // re-click Share to reconnect. TODO: add smart reconnect that validates
    // the session is still alive before re-joining.
  }

  private resetState(): void {
    this._isConnected = false;
    this._isConnecting = false;
    this._clientId = '';
    this._isOwner = false;
    this.grpc = null;
  }
}
