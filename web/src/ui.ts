// Modal overlay — the board uses it both for the Define / Create-task forms and
// for the per-card action panels (approve, decide, accept, audit). One modal at
// a time; Esc or a backdrop click closes it.
import { el } from "./dom.js";
import { button } from "./components.js";

let overlay: HTMLElement | null = null;

export function openModal(
  title: string,
  body: HTMLElement,
  opts: {
    wide?: boolean;
    subtitle?: string;
    sidebar?: HTMLElement;
    // Custom chrome: when given, `header` replaces the default title bar and
    // `footer` is appended below the body (e.g. the Create-task modal's topbar
    // and Cancel / Plan & create action bar). `className` adds a modifier class
    // to the panel for scoped styling.
    header?: HTMLElement;
    footer?: HTMLElement;
    className?: string;
  } = {},
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
  const head = opts.header ?? el("div", { class: "modal-head" }, [
    el("div", {}, heading),
    el("button", { class: "modal-x", title: "Close", onclick: closeModal }, ["✕"]),
  ]);
  const cls = "modal" + (opts.wide ? " wide" : "") + (opts.sidebar ? " split" : "") +
    (opts.className ? " " + opts.className : "");
  const panelChildren: HTMLElement[] = [head, modalBody];
  if (opts.footer) panelChildren.push(opts.footer);
  const panel = el("div", { class: cls }, panelChildren);
  // Clicking the overlay outside the modal must not close it — only the
  // Cancel button or the top-right ✕ dismiss the modal.
  const o = el("div", { class: "modal-overlay" }, [panel]);
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

export function promptModal(
  opts: { title: string; placeholder?: string; submitLabel?: string; value?: string },
  onSubmit: (text: string) => void,
  onCancel?: () => void,
): void {
  const textarea = el("textarea", {
    class: "prompt-input comment-input",
    rows: "4",
    placeholder: opts.placeholder ?? "",
    value: opts.value ?? "",
  }) as HTMLTextAreaElement;

  const submit = () => {
    const trimmed = textarea.value.trim();
    if (!trimmed) return;
    closeModal();
    onSubmit(trimmed);
  };

  const cancel = () => {
    closeModal();
    onCancel?.();
  };

  textarea.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  });

  const body = el("div", {}, [
    textarea,
    el("div", { class: "form-actions" }, [
      button("Cancel", { onclick: cancel }),
      button(opts.submitLabel ?? "Submit", { variant: "primary", onclick: submit }),
    ]),
  ]);

  openModal(opts.title, body);
  textarea.focus();
}

function onEsc(e: KeyboardEvent): void {
  if (e.key === "Escape") closeModal();
}
