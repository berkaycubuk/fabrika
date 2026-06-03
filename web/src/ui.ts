// Modal overlay — the board uses it both for the Define / Create-task forms and
// for the per-card action panels (approve, decide, accept, audit). One modal at
// a time; Esc or a backdrop click closes it.
import { el } from "./dom.js";

let overlay: HTMLElement | null = null;

export function openModal(
  title: string,
  body: HTMLElement,
  opts: { wide?: boolean; subtitle?: string; sidebar?: HTMLElement } = {},
): void {
  closeModal();
  const heading: HTMLElement[] = [el("h2", {}, [title])];
  if (opts.subtitle) heading.push(el("p", { class: "modal-sub muted" }, [opts.subtitle]));
  // With a sidebar, the body becomes a two-column container (main + aside) and
  // the modal widens to the split variant. Without one, markup is unchanged.
  const modalBody = opts.sidebar
    ? el("div", { class: "modal-body" }, [
        el("div", { class: "modal-main" }, [body]),
        el("div", { class: "modal-aside" }, [opts.sidebar]),
      ])
    : el("div", { class: "modal-body" }, [body]);
  const cls = "modal" + (opts.wide ? " wide" : "") + (opts.sidebar ? " split" : "");
  const panel = el("div", { class: cls }, [
    el("div", { class: "modal-head" }, [
      el("div", {}, heading),
      el("button", { class: "modal-x", title: "Close", onclick: closeModal }, ["✕"]),
    ]),
    modalBody,
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
