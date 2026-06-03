// Define screen: state one big task as plain intent + constraints and submit.
// A planner agent decomposes it into a proposed plan you approve (SPECS §10).
// Below the form, every big task is listed with its live planning status so
// failures (e.g. a repo with no commits, no planner enabled) are visible
// instead of silently disappearing.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import type { BigTask } from "../types.js";

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
    el("div", { class: "section-h" }, ["Big tasks"]),
    el("div", { id: "bigtask-list", class: "card-list" }, ["Loading…"]),
  );
  refresh();
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
        ok.textContent = "Submitted. Watch its status below; an approved plan appears under Approve.";
        refresh();
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

async function refresh(): Promise<void> {
  const list = document.getElementById("bigtask-list");
  if (!list) return;
  try {
    const bts = await api.listBigTasks();
    clear(list);
    if (bts.length === 0) {
      list.append(el("p", { class: "muted" }, ["No big tasks yet. Define one above."]));
      return;
    }
    for (const bt of bts) list.append(bigTaskCard(bt));
  } catch (e) {
    list.textContent = (e as Error).message;
  }
}

function bigTaskCard(bt: BigTask): HTMLElement {
  const children: (Node | string)[] = [
    el("div", { class: "card-title" }, [
      bt.title,
      el("span", { class: `pill status-${bt.status}` }, [bt.status]),
    ]),
  ];
  if (bt.intent) children.push(el("p", { class: "card-spec" }, [bt.intent]));
  if (bt.status === "error" && bt.error) {
    children.push(el("div", { class: "form-error bigtask-error" }, [bt.error]));
  }
  if (bt.status === "planning") {
    children.push(el("p", { class: "muted sm" }, ["Planner is working…"]));
  }
  return el("div", { class: "card" }, [el("div", { class: "card-main" }, children)]);
}

// onBigTaskEvent refreshes the list when a bigtask.* event arrives (status
// transitions: planning -> planned, or -> error).
export function onBigTaskEvent(): void {
  if (document.getElementById("bigtask-list")) refresh();
}
