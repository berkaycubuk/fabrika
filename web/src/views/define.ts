// Define screen: state one big task as plain intent + constraints and submit.
// A planner agent decomposes it into a proposed plan you approve (SPECS §10).
import { api } from "../api.js";
import { el, clear } from "../dom.js";

export function renderDefine(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Define"]),
      el("p", { class: "muted" }, [
        "Describe an outcome. A planner agent turns it into a plan you approve — no task-level prompts.",
      ]),
    ]),
    defineForm(),
  );
}

function defineForm(): HTMLElement {
  const title = el("input", {
    placeholder: "Outcome (e.g. Users can log in with email)",
  }) as HTMLInputElement;
  const intent = el("textarea", {
    placeholder: "The why + desired outcome. What does done look like?",
    rows: "5",
  }) as HTMLTextAreaElement;
  const constraints = el("textarea", {
    placeholder: "Constraints, one per line (e.g. PCI-compliant, works on mobile)",
    rows: "3",
  }) as HTMLTextAreaElement;
  const err = el("div", { class: "form-error" });
  const ok = el("div", { class: "form-ok" });

  return el("form", {
    class: "define-form card",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      err.textContent = "";
      ok.textContent = "";
      try {
        await api.createBigTask({
          title: title.value.trim(),
          intent: intent.value.trim(),
          constraints: constraints.value.split("\n").map((s) => s.trim()).filter(Boolean),
        });
        (e.target as HTMLFormElement).reset();
        ok.textContent = "Submitted. If a planner agent is enabled, a plan will appear under Approve.";
      } catch (e2) {
        err.textContent = (e2 as Error).message;
      }
    },
  }, [
    el("div", { class: "field" }, [el("label", {}, ["Outcome"]), title]),
    el("div", { class: "field" }, [el("label", {}, ["Intent"]), intent]),
    el("div", { class: "field" }, [el("label", {}, ["Constraints"]), constraints]),
    err,
    ok,
    el("div", { class: "form-actions" }, [
      el("button", { class: "primary", type: "submit" }, ["Define big task"]),
    ]),
  ]);
}
