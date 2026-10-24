import { GRPCWSClient } from '@panyam/servicekit-client';
import { fromJson, type JsonValue } from '@bufbuild/protobuf';
import { base64Encode } from '@bufbuild/protobuf/wire';
import type { CollabActionJson, PeerInfoJson } from './gen/massrelay/v1/models/collab_pb.js';
import type { CollabEvent, PeerInfo } from './gen/massrelay/v1/models/collab_pb.js';
import { CollabEventSchema } from './gen/massrelay/v1/models/collab_pb.js';
import { resolveRelayUrl } from './url-params.js';

export interface CollabClientOptions {
  onEvent?: (event: CollabEvent) => void;
  /** Called for this client and each peer already in the room on join, then
   * for each peer that joins later. peer.identity is set when the relay
   * validated a token for that peer's connection, and absent for anonymous
   * peers and for this client itself. */
  onPeerJoined?: (peer: PeerInfoJson) => void;
  onPeerLeft?: (clientId: string) => void;
  onError?: (error: Error) => void;
  onConnect?: (clientId: string) => void;
  /** Called once for every connection that reached onConnect, however it
   * ends: disconnect(), the relay closing, or the owner ending the session
   * (before onSessionEnded). */
  onDisconnect?: () => void;
  onSessionEnded?: (reason: string) => void;
  onOwnerChanged?: (newOwnerClientId: string) => void;
  onCredentialsChanged?: (reason: string) => void;
  onTitleChanged?: (title: string) => void;
  /** A generic app-defined message another peer sent to the room, or to this
   * client among the peers it addressed. fromClientId is the sender, as stamped
   * by the relay; it is empty for a message the relay's embedding server
   * originated. toClientId is this client's id when the message was addressed
   * to it, and empty when it went to the whole room, so a receiver can refuse a
   * room-wide message that should only ever arrive addressed. Messages from one
   * sender arrive in the order it sent them. */
  onAppMessage?: (kind: string, payload: Uint8Array, fromClientId: string, toClientId: string) => void;
  /** Called when the relay returns an ErrorEvent. One that arrives before the
   * join (ROOM_FULL, ROOM_FORBIDDEN, PROTOCOL_VERSION_TOO_OLD) means the join
   * was refused, and the client closes without calling onDisconnect. One that
   * arrives after it (PEER_NOT_FOUND, ALREADY_JOINED, RATE_LIMITED) refuses
   * that one action, and the connection stays open. */
  onErrorEvent?: (code: string, message: string) => void;
  maxRetries?: number;
  /** JWT for a relay running with auth. Sent as the `token` query parameter,
   * since browsers can't set headers on a WebSocket upgrade. */
  token?: string;
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
  private peers = new Map<string, PeerInfoJson>();
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

  /** The peer with this client id while it is in the room, including this
   * client itself, or undefined. identity is what the relay validated from the
   * peer's token, so a page can check `getPeer(fromClientId)?.identity?.subject`
   * before acting on an AppMessage. The relay delivers a peer's peerJoined
   * before anything it sends, so the sender is always here when onAppMessage
   * runs. It is gone after peerLeft, so a handler that awaits before checking
   * should read it first. Cleared when the connection closes. */
  getPeer(clientId: string): PeerInfoJson | undefined {
    return this.peers.get(clientId);
  }

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
      const leaveAction: CollabActionJson = { leave: { reason: 'user disconnected' } };
      grpc.send(leaveAction);
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

  send(action: CollabActionJson): void {
    if (!this._isConnected || !this.grpc) {
      throw new Error('Not connected');
    }
    this.grpc.send({
      ...action,
      clientId: this._clientId,
      timestamp: String(Date.now()),
    });
  }

  /** Send a generic app-defined message. kind is an app-defined discriminator;
   * payload is opaque bytes. By default it fans out to all other peers in the
   * room; with opts.to it goes to that client or those clients alone, each
   * seeing only its own id as the addressee. The relay answers with one
   * ErrorEvent PEER_NOT_FOUND (see onErrorEvent) naming any that are not in the
   * room, and still delivers to the rest. */
  sendAppMessage(kind: string, payload: Uint8Array, opts?: { to?: string | string[] }): void {
    // The JSON action encodes bytes as base64; keep the public API Uint8Array.
    const appMessage: { kind: string; payload: string; toClientIds?: string[] } = { kind, payload: base64Encode(payload) };
    const to = typeof opts?.to === 'string' ? [opts.to] : (opts?.to ?? []);
    if (to.length > 0) appMessage.toClientIds = to;
    this.send({ appMessage });
  }

  private openWebSocket(): void {
    const resolved = resolveRelayUrl(this._relayUrl);
    const wsSessionId = this._sessionId || '_new';
    let url = `${resolved}/ws/v1/${encodeURIComponent(wsSessionId)}/sync`;
    if (this.options.token) url += `?token=${encodeURIComponent(this.options.token)}`;
    this.grpc = this.options._grpcFactory ? this.options._grpcFactory() : new GRPCWSClient();

    // GRPCWSClient.onMessage receives data already unwrapped from the
    // servicekit envelope ({type:"data", data:...} → just the data).
    // Convert raw JSON to canonical protobuf-es Message type at the boundary.
    this.grpc.onMessage = (data: unknown) => {
      const event = fromJson(CollabEventSchema, data as JsonValue);
      this.handleEvent(event);
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
      const joinAction: CollabActionJson = {
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
      };
      this.grpc?.send(joinAction);
    }).catch(() => {
      // Error already dispatched via grpc.onError
    });
  }

  private handleEvent(event: CollabEvent): void {
    this.options.onEvent?.(event);

    switch (event.event.case) {
      case 'error': {
        // Before the join, an error is the relay refusing it (ROOM_FULL,
        // ROOM_FORBIDDEN, PROTOCOL_VERSION_TOO_OLD), so close. After the join
        // it refuses one action (PEER_NOT_FOUND, ALREADY_JOINED) and the relay
        // keeps the stream open, so we do too.
        const err = event.event.value;
        this.options.onErrorEvent?.(err.code, err.message);
        if (!this._isConnected) this.closeFromRelay();
        break;
      }

      case 'roomJoined': {
        const rj = event.event.value;
        const room = rj.room;
        this._clientId = rj.clientId;
        // Capture relay-generated sessionId (may differ from what we sent)
        if (room?.sessionId) {
          this._sessionId = room.sessionId;
        }
        this._maxPeers = rj.maxPeers;
        this._roomEncrypted = !!room?.encrypted;
        this._title = room?.title || '';
        this._isConnected = true;
        this._isConnecting = false;
        this.retryCount = 0;
        this.options.onConnect?.(this._clientId);

        // Add self as a peer (server doesn't include joining client in peers list).
        // RoomJoined carries no PeerInfo for the joiner, so self has no identity.
        this.peers.clear();
        this.addPeer({
          clientId: this._clientId,
          username: this._username,
          avatarUrl: '',
          clientType: 'browser',
          isActive: true,
          metadata: this._metadata,
        });

        // Add existing peers already in the room (map keyed by clientId)
        if (room?.peers) {
          for (const peer of Object.values(room.peers)) {
            this.addPeer(peerToJson(peer));
          }
        }
        break;
      }

      case 'peerJoined': {
        const peer = event.event.value.peer;
        if (peer) {
          this.addPeer(peerToJson(peer));
        } else {
          this.options.onPeerJoined?.({});
        }
        break;
      }

      case 'peerLeft':
        this.peers.delete(event.event.value.clientId);
        this.options.onPeerLeft?.(event.event.value.clientId);
        break;

      case 'sessionEnded':
        this.closeFromRelay();
        this.options.onSessionEnded?.(event.event.value.reason);
        break;

      case 'ownerChanged': {
        const newOwnerId = event.event.value.newOwnerClientId;
        this._isOwner = newOwnerId === this._clientId;
        for (const [id, p] of this.peers) {
          this.peers.set(id, { ...p, isOwner: id === newOwnerId });
        }
        this.options.onOwnerChanged?.(newOwnerId);
        break;
      }

      case 'credentialsChanged':
        this.options.onCredentialsChanged?.(event.event.value.reason);
        break;

      case 'titleChanged':
        this._title = event.event.value.title;
        this.options.onTitleChanged?.(this._title);
        break;

      case 'appMessage':
        this.options.onAppMessage?.(event.event.value.kind, event.event.value.payload, event.fromClientId, event.event.value.toClientIds[0] ?? '');
        break;
    }
  }

  private handleConnectionClosed(): void {
    const wasConnected = this._isConnected;
    this._isConnected = false;
    this._isConnecting = false;
    this.peers.clear();

    if (wasConnected) {
      this.options.onDisconnect?.();
    }

    // Auto-reconnect disabled for now — reconnecting with stale session
    // params after a server restart creates phantom sessions. The user can
    // re-click Share to reconnect. TODO: add smart reconnect that validates
    // the session is still alive before re-joining.
  }

  /** Closes a connection the relay ended. Fires onDisconnect if it had
   * joined, so every onConnect is paired with exactly one onDisconnect. State
   * is reset before close() so a synchronous onClose finds nothing to do. */
  private closeFromRelay(): void {
    const grpc = this.grpc;
    const wasConnected = this._isConnected;
    this.explicitDisconnect = true;
    this.resetState();
    grpc?.close();
    if (wasConnected) this.options.onDisconnect?.();
  }

  private resetState(): void {
    this._isConnected = false;
    this._isConnecting = false;
    this._clientId = '';
    this._isOwner = false;
    this.peers.clear();
    this.grpc = null;
  }

  private addPeer(peer: PeerInfoJson): void {
    if (peer.clientId) this.peers.set(peer.clientId, peer);
    this.options.onPeerJoined?.(peer);
  }
}

function peerToJson(peer: PeerInfo): PeerInfoJson {
  const json: PeerInfoJson = {
    clientId: peer.clientId,
    username: peer.username,
    avatarUrl: peer.avatarUrl,
    clientType: peer.clientType,
    isActive: peer.isActive,
    isOwner: peer.isOwner,
    metadata: peer.metadata,
  };
  if (peer.identity) {
    json.identity = { subject: peer.identity.subject, clientId: peer.identity.clientId };
  }
  return json;
}
