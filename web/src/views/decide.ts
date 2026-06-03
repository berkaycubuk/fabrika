// Decide screen: the decision queue. Each item is a question the planner or an
// implementer agent couldn't resolve — answer with a tap (or free text), with an
// optional "save as convention" so the answer steers future runs. Answering a
// task-level question resumes its task. (SPECS §10 "Decide".)
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import type { Decision } from "../types.js";

export function renderDecide(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Decide"]),
      el("p", { class: "muted" }, [
        "Questions agents can't resolve. Answer to unblock work; save as a convention to steer future runs.",
      ]),
    ]),
    el("div", { id: "decision-list", class: "card-list" }, ["Loading…"]),
  );
  refresh();
}

async function refresh(): Promise<void> {
  const list = document.getElementById("decision-list");
  if (!list) return;
  try {
    const decisions = await api.listDecisions();
    clear(list);
    if (decisions.length === 0) {
      list.append(el("p", { class: "muted" }, ["No decisions waiting on you. 🎉"]));
      return;
    }
    for (const d of decisions) list.append(decisionCard(d));
  } catch (e) {
    list.textContent = (e as Error).message;
  }
}

function decisionCard(d: Decision): HTMLElement {
  const promote = el("input", { type: "checkbox" }) as HTMLInputElement;
  const free = el("input", { placeholder: "Or type an answer…" }) as HTMLInputElement;
  const scope = d.taskId ? "task" : "plan";

  const answer = (text: string) => {
    const a = text.trim();
    if (!a) {
      alert("Pick an option or type an answer.");
      return;
    }
    act(() => api.answerDecision(d.id, a, promote.checked));
  };

  const optionBtns = (d.options ?? []).map((o) =>
    el("button", { class: "option", onclick: () => answer(o) }, [o]),
  );

  return el("div", { class: "card decision-card" }, [
    el("div", { class: "card-main" }, [
      el("div", { class: "card-title" }, [
        d.question,
        el("span", { class: "pill status-blocked" }, [scope]),
      ]),
      d.context ? el("p", { class: "card-spec" }, [d.context]) : el("span", {}),
      optionBtns.length ? el("div", { class: "option-row" }, optionBtns) : el("span", {}),
      el("div", { class: "decision-answer" }, [
        free,
        el("button", { class: "primary", onclick: () => answer(free.value) }, ["Answer"]),
      ]),
      el("label", { class: "checkbox" }, [promote, "Save as a convention (steer future runs)"]),
    ]),
  ]);
}

async function act(fn: () => Promise<unknown>): Promise<void> {
  try {
    await fn();
    refresh();
  } catch (e) {
    alert((e as Error).message);
  }
}

export function onDecisionEvent(): void {
  if (document.getElementById("decision-list")) refresh();
}
