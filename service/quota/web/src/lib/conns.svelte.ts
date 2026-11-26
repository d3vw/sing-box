// Connections: while the page is open it follows the active count over
// /connections, and asks for every connection only on the connections tab.
// It disconnects while the browser tab is in the background and reconnects
// on return. Only the user's own connections count, not the portal's (this
// page, the network quality test and this WebSocket).

import { ui } from "./state.svelte";
import type { ConnSnapshot } from "./types";

export const conns = $state({
  snap: null as ConnSnapshot | null,
  /** When snap arrived, by this device's clock. */
  gotAt: 0,
  online: false,
});

let ws: WebSocket | null = null;
let retry = 0;
let timer = 0;
let started = false;

function connect() {
  if (ws || document.hidden) return;
  try {
    ws = new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/connections");
  } catch {
    again();
    return;
  }
  ws.onopen = () => {
    retry = 0;
    conns.online = true;
    want();
  };
  ws.onmessage = (e) => {
    try {
      conns.snap = JSON.parse(e.data);
      conns.gotAt = Date.now();
    } catch {}
  };
  ws.onclose = ws.onerror = () => {
    drop();
    again();
  };
}

function drop() {
  if (!ws) return;
  ws.onopen = ws.onclose = ws.onerror = ws.onmessage = null;
  try { ws.close(); } catch {}
  ws = null;
  conns.online = false;
}

// Reconnects backing off 1, 2, 4… seconds, up to 30.
function again() {
  clearTimeout(timer);
  if (document.hidden) return;
  timer = setTimeout(connect, Math.min(30000, 1000 * Math.pow(2, retry++)));
}

/** Tells the server whether to send every connection or just the counts. */
export function want() {
  if (ws && ws.readyState === 1) ws.send(JSON.stringify({ detail: ui.view === "cx" }));
}

export function startConns() {
  if (started) return;
  started = true;
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) {
      clearTimeout(timer);
      drop();
    } else {
      retry = 0;
      connect();
    }
  });
  connect();
}

/** Seconds since at by the server's clock, so a wrong local clock never goes negative. */
export function age(at: number) {
  const now = conns.snap ? conns.snap.now + (Date.now() - conns.gotAt) : Date.now();
  return Math.max(0, Math.floor((now - at) / 1000));
}

export function fmtBytes(b: number) {
  if (b < 1024) return b + " B";
  const u = ["KB", "MB", "GB", "TB"];
  let i = -1;
  do {
    b /= 1024;
    i++;
  } while (b >= 1024 && i < u.length - 1);
  return (b < 10 ? b.toFixed(1) : Math.round(b)) + " " + u[i];
}
