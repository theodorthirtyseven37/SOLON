/**
 * Solon Relay — Cloudflare Worker + Durable Object
 *
 * Routes:
 *   GET  /api/connect/{instance_id}  → WebSocket upgrade for Solon instances
 *   ANY  /{instance_id}/v1/*         → Proxy to connected Solon instance
 *   ANY  /{instance_id}/api/v1/*     → Proxy to connected Solon instance
 *   GET  /                           → Health check
 */

export interface Env {
  INSTANCE: DurableObjectNamespace;
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    const path = url.pathname;

    // Health check
    if (path === '/' || path === '/health') {
      return Response.json({ status: 'ok', service: 'solon-relay' });
    }

    // WebSocket connect: GET /api/connect/{instance_id}
    if (path.startsWith('/api/connect/')) {
      const instanceId = path.slice('/api/connect/'.length).split('/')[0];
      if (!instanceId || instanceId.length < 12) {
        return Response.json({ error: 'invalid instance ID' }, { status: 400 });
      }
      const stub = env.INSTANCE.get(env.INSTANCE.idFromName(instanceId));
      return stub.fetch(request);
    }

    // Proxy requests: /{instance_id}/v1/* or /{instance_id}/api/v1/*
    const match = path.match(/^\/([a-zA-Z0-9_-]{12,})\/(.*)/);
    if (!match) {
      return Response.json(
        { error: 'not found — format: /{instance_id}/v1/...' },
        { status: 404 }
      );
    }

    const [, instanceId, rest] = match;
    const stub = env.INSTANCE.get(env.INSTANCE.idFromName(instanceId));

    // Rewrite the URL to strip the instance_id prefix
    const proxyUrl = new URL(request.url);
    proxyUrl.pathname = '/' + rest;

    const proxyReq = new Request(proxyUrl.toString(), {
      method: request.method,
      headers: request.headers,
      body: request.body,
    });

    proxyReq.headers.set('X-Relay-Instance', instanceId);

    return stub.fetch(proxyReq);
  },
};

/**
 * Durable Object: one per Solon instance.
 * Holds the WebSocket connection to the Solon binary.
 * Uses Hibernation API so the connection survives DO eviction.
 */
export class SolonInstance implements DurableObject {
  private ctx: DurableObjectState;
  private pending = new Map<string, {
    resolve: (resp: RelayResponse) => void;
    reject: (err: Error) => void;
    stream?: WritableStreamDefaultWriter;
  }>();
  private requestCounter = 0;

  constructor(ctx: DurableObjectState, env: Env) {
    this.ctx = ctx;
  }

  /** Get the active WebSocket (survives hibernation). */
  private getSocket(): WebSocket | null {
    const sockets = this.ctx.getWebSockets();
    return sockets.length > 0 ? sockets[0] : null;
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname.startsWith('/api/connect/')) {
      return this.handleConnect(request);
    }

    return this.handleProxy(request);
  }

  private handleConnect(request: Request): Response {
    if (request.headers.get('Upgrade') !== 'websocket') {
      return Response.json({ error: 'expected websocket' }, { status: 426 });
    }

    // Close any existing connection
    for (const ws of this.ctx.getWebSockets()) {
      try { ws.close(1000, 'replaced'); } catch {}
    }

    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);

    this.ctx.acceptWebSocket(server);

    return new Response(null, { status: 101, webSocket: client });
  }

  private async handleProxy(request: Request): Promise<Response> {
    const solon = this.getSocket();
    if (!solon) {
      return Response.json(
        { error: { message: 'Solon instance is offline', type: 'unavailable' } },
        { status: 502 }
      );
    }

    const id = `req_${++this.requestCounter}`;
    const body = request.body ? await request.text() : '';

    const headers: Record<string, string> = {};
    for (const key of ['authorization', 'content-type', 'accept']) {
      const val = request.headers.get(key);
      if (val) headers[key] = val;
    }

    const isStream = headers['accept']?.includes('text/event-stream') ||
                     body.includes('"stream":true') || body.includes('"stream": true');

    const msg: RelayRequest = {
      type: 'request',
      id,
      method: request.method,
      path: new URL(request.url).pathname,
      headers,
      body,
    };

    try {
      solon.send(JSON.stringify(msg));
    } catch {
      return Response.json(
        { error: { message: 'Failed to reach Solon instance', type: 'unavailable' } },
        { status: 502 }
      );
    }

    if (isStream) {
      return this.waitForStreamingResponse(id);
    }

    return this.waitForResponse(id);
  }

  private waitForResponse(id: string): Promise<Response> {
    return new Promise<Response>((outerResolve) => {
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        outerResolve(Response.json(
          { error: { message: 'Solon instance timed out', type: 'timeout' } },
          { status: 504 }
        ));
      }, 30_000);

      this.pending.set(id, {
        resolve: (resp) => {
          clearTimeout(timeout);
          this.pending.delete(id);
          const responseHeaders = new Headers(resp.headers || {});
          outerResolve(new Response(resp.body || null, {
            status: resp.status,
            headers: responseHeaders,
          }));
        },
        reject: (err) => {
          clearTimeout(timeout);
          this.pending.delete(id);
          outerResolve(Response.json(
            { error: { message: err.message, type: 'error' } },
            { status: 500 }
          ));
        },
      });
    });
  }

  private waitForStreamingResponse(id: string): Promise<Response> {
    const { readable, writable } = new TransformStream();
    const writer = writable.getWriter();

    return new Promise<Response>((outerResolve) => {
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        writer.close().catch(() => {});
        outerResolve(Response.json(
          { error: { message: 'Solon instance timed out', type: 'timeout' } },
          { status: 504 }
        ));
      }, 120_000);

      this.pending.set(id, {
        resolve: (resp) => {
          clearTimeout(timeout);
          const responseHeaders = new Headers(resp.headers || {});
          outerResolve(new Response(readable, {
            status: resp.status,
            headers: responseHeaders,
          }));
        },
        reject: (err) => {
          clearTimeout(timeout);
          this.pending.delete(id);
          writer.close().catch(() => {});
          outerResolve(Response.json(
            { error: { message: err.message, type: 'error' } },
            { status: 500 }
          ));
        },
        stream: writer,
      });
    });
  }

  async webSocketMessage(_ws: WebSocket, data: string | ArrayBuffer) {
    if (typeof data !== 'string') return;

    let msg: RelayMessage;
    try {
      msg = JSON.parse(data);
    } catch {
      return;
    }

    switch (msg.type) {
      case 'init':
        // Solon instance connected — send back the URL
        _ws.send(JSON.stringify({
          type: 'init_ok',
          url: `https://relay.getsolon.dev/${(msg as any).instance_id}`,
        }));
        break;

      case 'pong':
        break;

      case 'response': {
        const pending = this.pending.get(msg.id!);
        if (pending) {
          pending.resolve(msg as unknown as RelayResponse);
        }
        break;
      }

      case 'response_start': {
        const pending = this.pending.get(msg.id!);
        if (pending) {
          pending.resolve(msg as unknown as RelayResponse);
        }
        break;
      }

      case 'response_chunk': {
        const pending = this.pending.get(msg.id!);
        if (pending?.stream) {
          const encoder = new TextEncoder();
          await pending.stream.write(encoder.encode((msg as any).data));
        }
        break;
      }

      case 'response_end': {
        const pending = this.pending.get(msg.id!);
        if (pending?.stream) {
          await pending.stream.close().catch(() => {});
        }
        this.pending.delete(msg.id!);
        break;
      }
    }
  }

  async webSocketClose(_ws: WebSocket) {
    // Reject all pending requests
    for (const [id, p] of this.pending) {
      p.reject(new Error('Solon instance disconnected'));
      this.pending.delete(id);
    }
  }

  async webSocketError(_ws: WebSocket) {
    // handled by webSocketClose
  }
}

// Protocol types

interface RelayRequest {
  type: 'request';
  id: string;
  method: string;
  path: string;
  headers: Record<string, string>;
  body: string;
}

interface RelayResponse {
  type: 'response' | 'response_start';
  id?: string;
  status: number;
  headers?: Record<string, string>;
  body?: string;
}

interface RelayMessage {
  type: string;
  id?: string;
  [key: string]: unknown;
}
