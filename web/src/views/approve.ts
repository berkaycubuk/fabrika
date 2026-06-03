// Approve screen: review a proposed plan (task list + dependency shape + open
// decisions) before any work starts. Approve to release tasks to the scheduler,
// or reject to send the big task back. (SPECS §10 "Approve".)
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import type { Plan, Task, Decision } from "../types.js";

export function renderApprove(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Approve"]),
      el("p", { class: "muted" }, [
        "Proposed plans from the planner. Approve to start the work, or kick it back.",
      ]),
    ]),
    el("div", { id: "plan-list", class: "card-list" }, ["Loading…"]),
  );
  refresh();
}

async function refresh(): Promise<void> {
  const list = document.getElementById("plan-list");
  if (!list) return;
  try {
    const plans = (await api.listPlans()).filter((p) => p.status === "proposed");
    clear(list);
    if (plans.length === 0) {
      list.append(el("p", { class: "muted" }, ["No plans waiting for approval."]));
      return;
    }
    for (const p of plans) list.append(planCard(p));
  } catch (e) {
    list.textContent = (e as Error).message;
  }
}

function planCard(p: Plan): HTMLElement {
  const titleOf = (id: string) => p.tasks.find((t) => t.id === id)?.title ?? id.slice(0, 6);
  const heading = p.bigTask?.title ?? "Plan";

  const children: (Node | string)[] = [
    el("div", { class: "card-title" }, [
      heading,
      el("span", { class: "pill status-review" }, [`${p.tasks.length} tasks`]),
    ]),
  ];
  if (p.bigTask?.intent) children.push(el("p", { class: "card-spec" }, [p.bigTask.intent]));

  children.push(el("div", { class: "plan-tasks" }, p.tasks.map((t) => planTask(t, titleOf))));

  if (p.openDecisions.length > 0) {
    children.push(
      el("div", { class: "plan-decisions" }, [
        el("div", { class: "section-h sm" }, ["Open questions"]),
        ...p.openDecisions.map((d) => planDecision(d)),
      ]),
    );
  }

  const actions = el("div", { class: "card-actions" }, [
    el("button", { class: "primary", onclick: () => act(() => api.approvePlan(p.id)) }, ["Approve plan"]),
    el("button", { class: "danger", onclick: () => act(() => api.rejectPlan(p.id)) }, ["Reject"]),
  ]);

  return el("div", { class: "card plan-card" }, [el("div", { class: "card-main" }, children), actions]);
}

function planTask(t: Task, titleOf: (id: string) => string): HTMLElement {
  const meta: (Node | string)[] = [el("span", { class: `tag risk-${t.riskTier}` }, [t.riskTier])];
  for (const tag of t.tags ?? []) meta.push(el("span", { class: "tag" }, [tag]));
  for (const dep of t.dependsOn ?? []) meta.push(el("span", { class: "tag dep" }, [`after: ${titleOf(dep)}`]));
  for (const c of t.acceptance?.verifyCmds ?? []) meta.push(el("code", { class: "verify-cmd" }, [c]));

  return el("div", { class: "plan-task" }, [
    el("div", { class: "plan-task-title" }, [t.title]),
    t.spec ? el("p", { class: "card-spec sm" }, [t.spec]) : el("span", {}),
    el("div", { class: "card-meta" }, meta),
  ]);
}

function planDecision(d: Decision): HTMLElement {
  return el("div", { class: "plan-decision" }, [
    el("span", { class: "q" }, [d.question]),
    d.options.length ? el("span", { class: "muted" }, [` (${d.options.join(" / ")})`]) : el("span", {}),
    el("span", { class: "muted hint" }, [" — answer it under Decide"]),
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

export function onPlanEvent(): void {
  if (document.getElementById("plan-list")) refresh();
}
