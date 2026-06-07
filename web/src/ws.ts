// Live event stream over WebSocket (SPECS.md §11, §10). Auto-reconnects.
import type { FabrikaEvent } from "./types.js";

export type ReleaseListener = () => void;
const releaseListeners: ReleaseListener[] = [];

// registerReleaseListener registers a callback invoked on release.updated events.
export function registerReleaseListener(fn: ReleaseListener): void {
  releaseListeners.push(fn);
}

// notifyReleaseListeners is called by the event loop when a release.updated event arrives.
export function notifyReleaseListeners(): void {
  for (const fn of releaseListeners) fn();
}

export type IncidentListener = () => void;
const incidentListeners: IncidentListener[] = [];

// registerIncidentListener registers a callback invoked on incident.* events.
export function registerIncidentListener(fn: IncidentListener): void {
  incidentListeners.push(fn);
}

// notifyIncidentListeners is called by the event loop when an incident.* event arrives.
export function notifyIncidentListeners(): void {
  for (const fn of incidentListeners) fn();
}

type Listener = (e: FabrikaEvent) => void;

interface Handlers {
  // Called every time the socket opens, including the first connect. Lets the
  // UI show "live" as soon as the link is up rather than waiting for the first
  // event to arrive (a quiet system may emit nothing for a long while).
  onConnect?: () => void;
  // Called whenever the socket re-opens after a prior close — not on the very
  // first connect. The app uses this to force a full reconcile, since any
  // events emitted while the socket was down (server restart, sleep, blip) are
  // gone forever and the board would otherwise stay stale until a manual reload.
  onReconnect?: () => void;
  // Called when the socket closes and we begin backing off to reconnect.
  onDisconnect?: () => void;
}

export function connectEvents(onEvent: Listener, handlers: Handlers = {}): void {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const url = `${proto}://${location.host}/api/events`;

  let backoff = 500;
  // Tracks whether we've ever lost the socket, so the first open is silent and
  // every subsequent open is treated as a reconnect.
  let everClosed = false;
  const open = () => {
    const ws = new WebSocket(url);
    ws.onopen = () => {
      backoff = 500;
      handlers.onConnect?.();
      if (everClosed) handlers.onReconnect?.();
    };
    ws.onmessage = (msg) => {
      try {
        const e = JSON.parse(msg.data) as FabrikaEvent;
        if (e.type === "release.updated") notifyReleaseListeners();
        if (e.type.startsWith("incident.")) notifyIncidentListeners();
        onEvent(e);
      } catch {
        /* ignore malformed */
      }
    };
    ws.onclose = () => {
      everClosed = true;
      handlers.onDisconnect?.();
      setTimeout(open, backoff);
      backoff = Math.min(backoff * 2, 5000);
    };
    ws.onerror = () => ws.close();
  };
  open();
}
