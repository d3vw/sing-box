// Toasts: at most three at a time, newest on top, each gone after a while.

export interface Toast {
  id: number;
  tone?: "ok" | "err";
  title: string;
  body?: string;
  action?: { label: string; run: () => void };
}

let next = 0;

export const toasts = $state<Toast[]>([]);

export function dismiss(id: number) {
  const i = toasts.findIndex((x) => x.id === id);
  if (i >= 0) toasts.splice(i, 1);
}

export function toast(o: Omit<Toast, "id">) {
  const id = ++next;
  toasts.unshift({ ...o, id });
  if (toasts.length > 3) toasts.length = 3;
  setTimeout(() => dismiss(id), o.tone === "err" ? 7000 : 4000);
}
