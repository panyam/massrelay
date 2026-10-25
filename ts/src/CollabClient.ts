import { GRPCWSClient } from '@panyam/servicekit-client';
import { fromJson, type JsonValue } from '@bufbuild/protobuf';
import { base64Encode } from '@bufbuild/protobuf/wire';
import type { CollabActionJson, PeerInfoJson } from './gen/massrelay/v1/models/collab_pb.js';
import type { CollabEvent, PeerInfo } from './gen/massrelay/v1/models/collab_pb.js';
import { CollabEventSchema } from './gen/massrelay/v1/models/collab_pb.js';
import { resolveRelayUrl } from './url-params.js';

export interface CollabClientOptions {
  /** Every event from the relay, before the client handles it. */
  onEvent?: (event: CollabEvent) => void;
  /** Called for this client and each peer already in the room on join, then
   * for each peer that joins later. peer.identity is set when the relay
   * validated a token for that peer's connection, and absent for anonymous
   * peers and for this client itself. */
  onPeerJoined?: (peer: PeerInfoJson) => void;
  /** A peer left the room, including this client's own old id after a
   * reconnect rejoins under a new one. */
  onPeerLeft?: (clientId: string) => void;
  /** A socket-level error, or getToken throwing. Relay-sent errors go to
   * onErrorEvent instead. */
  onError?: (error: Error) => void;
  /** The relay accepted the join (RoomJoined), with this client's id. Fires
   * again after each reconnect, with a new id. */
  onConnect?: (clientId: string) => void;
  /** Called once for every connection that reached onConnect, however it
   * ends: disconnect(), the relay closing, or the owner ending the session
   * (before onSessionEnded). A reconnect that rejoins calls onConnect again,
   * with a new clientId. */
  onDisconnect?: () => void;
  /** The session is over for this client. reason is the relay's (e.g.
   * "owner_disconnected"), or one of the client's own: "session_gone" when a
   * reconnect found the room no longer exists (the relay restarted, or
   * everyone left), and "reconnect_failed" when maxRetries attempts couldn't
   * reach the relay or rejoin. "token_expired" is the relay ending the
   * connection at the token's exp; with getToken set the client reconnects
   * on a fresh token instead of reporting it. */
  onSessionEnded?: (reason: string) => void;
  /** Ownership moved to newOwnerClientId (another tab of the owner's
   * browser); isOwner reflects it already. */
  onOwnerChanged?: (newOwnerClientId: string) => void;
  /** The owner changed or removed the room's password ("password_removed").
   * The client stays connected; an encrypted app must re-derive its key. */
  onCredentialsChanged?: (reason: string) => void;
  /** The owner retitled the session; title reflects it already. */
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
   * was refused, and the client closes without calling onDisconnect. On a
   * rejoin, ROOM_NOT_FOUND is followed by onSessionEnded("session_gone"). One that
   * arrives after it (PEER_NOT_FOUND, ALREADY_JOINED, RATE_LIMITED) refuses
   * that one action, and the connection stays open. */
  onErrorEvent?: (code: string, message: string) => void;
  /** Rejoin on its own when the connection drops: the socket closing without
   * disconnect(), or the relay evicting this client as a slow consumer.
   * Before each attempt it checks GET /api/v1/rooms/{sessionId}, rejoining the
   * same sessionId on a 200 and ending with onSessionEnded("session_gone") on
   * a 404, so a relay restart never recreates the room as a phantom. Only a
   * client that has joined once reconnects. Default true. */
  reconnect?: boolean;
  /** Reconnect attempts before onSessionEnded("reconnect_failed"). Default 5.
   * The count resets each time a rejoin succeeds. */
  maxRetries?: number;
  /** Delay before the first reconnect attempt, doubling per attempt up to
   * 30s. Default 1000. */
  retryDelayMs?: number;
  /** Called before each reconnect attempt waits, with the 1-based attempt. */
  onReconnecting?: (attempt: number) => void;
  /** JWT for a relay running with auth. Sent as the `token` query parameter,
   * since browsers can't set headers on a WebSocket upgrade. */
  token?: string;
  /** Returns a JWT for each connection, the first and every reconnect, so a
   * rejoin after the original token expired presents a fresh one. Takes
   * precedence over token. If it throws, that attempt counts as failed. */
  getToken?: () => string | Promise<string>;
  /** Factory for creating GRPCWSClient instances. Defaults to `() => new GRPCWSClient()`.
   *  Override in tests with `GRPCWSClient.createMock()`. */
  _grpcFactory?: () => GRPCWSClient;
  /** fetch used for the reconnect room check. Defaults to globalThis.fetch. */
  _fetch?: typeof fetch;
}

/**
 * Framework-agnostic WebSocket client for the collab relay.
 * Uses @panyam/servicekit-client GRPCWSClient for envelope protocol
 * and auto ping/pong. Adds reconnect with exponential backoff on top (see
 * CollabClientOptions.reconnect).
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
  private hasJoined: boolean = false;
  private joinAsOwner: boolean = false;
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
    this.joinAsOwner = isOwner;
    this._browserId = browserId;
    this._clientHint = clientHint;
    this._encrypted = encrypted;
    this._isConnecting = true;
    this.explicitDisconnect = false;
    this.hasJoined = false;
    this.retryCount = 0;

    // Ensure cleanup on page unload (refresh, tab close, navigation)
    if (typeof window !== 'undefined') {
      this.boundBeforeUnload = () => this.disconnect();
      window.addEventListener('beforeunload', this.boundBeforeUnload);
    }

    this.openWithToken();
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
    this._isConnecting = false;
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

  /** Opens the socket with options.token, or with getToken()'s result when
   * set. A getToken failure counts as a failed reconnect attempt. */
  private openWithToken(): void {
    const getToken = this.options.getToken;
    if (!getToken) {
      this.openWebSocket(this.options.token);
      return;
    }
    Promise.resolve().then(getToken).then(
      (token) => {
        if (!this.explicitDisconnect) this.openWebSocket(token);
      },
      (err) => {
        this.options.onError?.(err instanceof Error ? err : new Error(String(err)));
        if (this.explicitDisconnect) return;
        if (this.hasJoined) this.scheduleReconnect();
        else this._isConnecting = false;
      },
    );
  }

  private openWebSocket(token: string | undefined): void {
    const resolved = resolveRelayUrl(this._relayUrl);
    const wsSessionId = this._sessionId || '_new';
    let url = `${resolved}/ws/v1/${encodeURIComponent(wsSessionId)}/sync`;
    if (token) url += `?token=${encodeURIComponent(token)}`;
    const grpc = this.options._grpcFactory ? this.options._grpcFactory() : new GRPCWSClient();
    this.grpc = grpc;

    // GRPCWSClient.onMessage receives data already unwrapped from the
    // servicekit envelope ({type:"data", data:...} → just the data).
    // Convert raw JSON to canonical protobuf-es Message type at the boundary.
    this.grpc.onMessage = (data: unknown) => {
      const event = fromJson(CollabEventSchema, data as JsonValue);
      this.handleEvent(event);
    };

    // A socket this client already replaced or closed can still report its
    // close late; only the current one counts.
    this.grpc.onClose = () => {
      if (this.grpc === grpc) this.handleConnectionClosed();
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
          isOwner: this.joinAsOwner,
          browserId: this._browserId,
          clientHint: this._clientHint,
          protocolVersion: 2,
          encrypted: this._encrypted,
          title: this._title,
          // A rejoin must not recreate a room the relay lost between the
          // room check and this join. Older relays ignore the field.
          mustExist: this.hasJoined,
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
        if (!this._isConnected) {
          this.closeFromRelay();
          if (err.code === 'ROOM_NOT_FOUND' && this.hasJoined) this.options.onSessionEnded?.('session_gone');
        }
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
        this.hasJoined = true;
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

      case 'sessionEnded': {
        const reason = event.event.value.reason;
        // An expired token only comes back with a fresh one; without
        // getToken the rejoin would be refused until retries ran out.
        const reconnectable = reason === 'slow_consumer' || (reason === 'token_expired' && !!this.options.getToken);
        if (reconnectable && this.reconnectEnabled()) {
          this.dropAndReconnect();
          break;
        }
        this.closeFromRelay();
        this.options.onSessionEnded?.(reason);
        break;
      }

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
    this.resetState();
    if (wasConnected) {
      this.options.onDisconnect?.();
    }
    if (!this.explicitDisconnect && this.hasJoined && this.reconnectEnabled()) {
      this.scheduleReconnect();
    }
  }

  private reconnectEnabled(): boolean {
    return this.options.reconnect ?? true;
  }

  /** Closes a connection the relay evicted and starts reconnecting. */
  private dropAndReconnect(): void {
    const grpc = this.grpc;
    const wasConnected = this._isConnected;
    this.resetState();
    grpc?.close();
    if (wasConnected) this.options.onDisconnect?.();
    this.scheduleReconnect();
  }

  private scheduleReconnect(): void {
    if (this.retryCount >= this.maxRetries) {
      this.endSession('reconnect_failed');
      return;
    }
    const attempt = ++this.retryCount;
    this._isConnecting = true;
    this.options.onReconnecting?.(attempt);
    const delay = Math.min((this.options.retryDelayMs ?? 1000) * 2 ** (attempt - 1), 30000);
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      void this.rejoinIfRoomExists();
    }, delay);
  }

  /** Rejoins only a room the relay still has, so a restarted relay doesn't
   * get the room recreated empty by whoever reconnects first. */
  private async rejoinIfRoomExists(): Promise<void> {
    const base = resolveRelayUrl(this._relayUrl).replace(/^ws/i, 'http');
    const fetchFn = this.options._fetch ?? globalThis.fetch;
    let status = 0;
    try {
      status = (await fetchFn(`${base}/api/v1/rooms/${encodeURIComponent(this._sessionId)}`)).status;
    } catch {
      // relay unreachable; retry
    }
    if (this.explicitDisconnect) return;
    if (status === 404) {
      this.endSession('session_gone');
    } else if (status === 200) {
      this.openWithToken();
    } else {
      this.scheduleReconnect();
    }
  }

  private endSession(reason: string): void {
    this.explicitDisconnect = true;
    this._isConnecting = false;
    this.options.onSessionEnded?.(reason);
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
