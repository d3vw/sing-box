// The outbound tab's nodes. By default the server's routing rules decide
// and nothing is overridden; a member may save a few Shadowsocks nodes and
// send their traffic out through the one picked. On a switch the server
// closes open connections so they reconnect through the new outbound.

import type { Profile, Profiles } from "./types";
import { post } from "./state.svelte";

export const nodes = $state<Profiles>({ active: "", profiles: [] });

/** The latency test per node: busy, a round trip in ms, or an error. */
export const latency = $state<Record<string, { busy?: boolean; ms?: number; err?: string }>>({});

export function setNodes(j: Partial<Profiles>) {
  nodes.active = j.active || "";
  nodes.profiles = j.profiles || [];
}

export function findNode(id: string): Profile | null {
  return nodes.profiles.find((p) => p.id === id) || null;
}

/** Calls an endpoint under /outbound/profiles with o as its form. */
export function nodeAPI(action: string, o: Record<string, string | number | boolean | undefined>) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(o)) {
    if (v === false || v == null) continue;
    p.set(k, v === true ? "true" : String(v));
  }
  return post("/outbound/profiles/" + action, p);
}

/** Parses an ss:// link (SIP002 or all base64) with an optional #name; plugin parameters are ignored. */
export function parseSS(s: string) {
  s = s.trim();
  if (!/^ss:\/\//i.test(s)) return null;
  s = s.slice(5);
  let name = "";
  let i = s.indexOf("#");
  if (i >= 0) {
    try { name = decodeURIComponent(s.slice(i + 1)); } catch { name = s.slice(i + 1); }
    s = s.slice(0, i);
  }
  i = s.indexOf("?");
  if (i >= 0) s = s.slice(0, i);
  s = s.replace(/\/$/, "");
  const b64 = (x: string) => {
    x = x.replace(/-/g, "+").replace(/_/g, "/");
    while (x.length % 4) x += "=";
    try {
      return new TextDecoder("utf-8", { fatal: true }).decode(Uint8Array.from(atob(x), (c) => c.charCodeAt(0)));
    } catch {
      return null;
    }
  };
  let at = s.lastIndexOf("@");
  let user: string | null, host: string;
  if (at < 0) {
    const d = b64(s);
    if (!d) return null;
    at = d.lastIndexOf("@");
    if (at < 0) return null;
    user = d.slice(0, at);
    host = d.slice(at + 1);
  } else {
    user = s.slice(0, at);
    host = s.slice(at + 1);
    let plain: string | null = null;
    try { plain = decodeURIComponent(user); } catch {}
    user = plain && plain.indexOf(":") > 0 ? plain : b64(user);
  }
  if (!user) return null;
  const c = user.indexOf(":");
  const m = host.match(/^\[?([^\]]+?)\]?:(\d+)$/);
  if (c < 1 || !m) return null;
  return { name, server: m[1], server_port: m[2], method: user.slice(0, c), password: user.slice(c + 1) };
}
