import { el } from "../dom.js";

export interface ToastUndo { label?: string; onUndo: () => void | Promise<unknown>; }
export interface ToastOptions { message: string; undo?: ToastUndo; durationMs?: number; }

export function showToast(opts: ToastOptions): void {
  let container = document.querySelector<HTMLDivElement>(".toast-container");
  if (!container) {
    container = el("div", { class: "toast-container" }) as HTMLDivElement;
    document.body.appendChild(container);
  }

  const children: (Node | string)[] = [
    el("span", { class: "toast-message" }, [opts.message]),
  ];

  const toast = el("div", { class: "toast" }, children);

  const durationMs = opts.durationMs ?? 5000;
  let timer: ReturnType<typeof setTimeout> | null = setTimeout(() => {
    toast.remove();
  }, durationMs);

  if (opts.undo) {
    const undo = opts.undo;
    const label = undo.label ?? "Undo";
    const btn = el("button", {
      class: "toast-undo",
      type: "button",
    }, [label]);
    btn.addEventListener("click", () => {
      undo.onUndo();
      if (timer !== null) {
        clearTimeout(timer);
        timer = null;
      }
      toast.remove();
    });
    toast.appendChild(btn);
  }

  container.appendChild(toast);
}
