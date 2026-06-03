// Live event stream over WebSocket (SPECS.md §11, §10). Auto-reconnects.
import type { FabrikaEvent } from "./types.js";

type Listener = (e: FabrikaEvent) => void;

export function connectEvents(onEvent: Listener): void {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const url = `${proto}://${location.host}/api/events`;

  let backoff = 500;
  const open = () => {
    const ws = new WebSocket(url);
    ws.onopen = () => {
      backoff = 500;
    };
    ws.onmessage = (msg) => {
      try {
        onEvent(JSON.parse(msg.data) as FabrikaEvent);
      } catch {
        /* ignore malformed */
      }
    };
    ws.onclose = () => {
      setTimeout(open, backoff);
      backoff = Math.min(backoff * 2, 5000);
    };
    ws.onerror = () => ws.close();
  };
  open();
}
