// Modal overlay — the board uses it both for the Define / Create-task forms and
// for the per-card action panels (approve, decide, accept, audit). One modal at
// a time; Esc or a backdrop click closes it.
import { el } from "./dom.js";

let overlay: HTMLElement | null = null;

export function openModal(title: string, body: HTMLElement, opts: { wide?: boolean } = {}): void {
  closeModal();
  const panel = el("div", { class: "modal" + (opts.wide ? " wide" : "") }, [
    el("div", { class: "modal-head" }, [
      el("h2", {}, [title]),
      el("button", { class: "modal-x", title: "Close", onclick: closeModal }, ["✕"]),
    ]),
    el("div", { class: "modal-body" }, [body]),
  ]);
  const o = el("div", {
    class: "modal-overlay",
    onclick: (e: Event) => {
      if (e.target === o) closeModal();
    },
  }, [panel]);
  document.body.append(o);
  overlay = o;
  document.addEventListener("keydown", onEsc);
}

export function closeModal(): void {
  if (!overlay) return;
  overlay.remove();
  overlay = null;
  document.removeEventListener("keydown", onEsc);
}

function onEsc(e: KeyboardEvent): void {
  if (e.key === "Escape") closeModal();
}
