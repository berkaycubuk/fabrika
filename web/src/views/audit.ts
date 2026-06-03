// Audit screen: the post-merge audit queue. A random sample of auto-merged work
// (governed by the audit rate) lands here for an after-the-fact eyeball, so trust
// in autonomy stays calibrated without re-inserting a human in the common path.
// "Looks good" clears it; "Revert" records a change-failure. (SPECS §13, §14.)
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { STAGE_ORDER, type ReviewItem, type Evidence } from "../types.js";

export function renderAudit(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Audit"]),
      el("p", { class: "muted" }, [
        "Auto-merged work, sampled for a spot-check. Already on main — confirm it was right, or flag it as a change-failure.",
      ]),
    ]),
    el("div", { id: "audit-list", class: "card-list" }, ["Loading…"]),
  );
  refresh();
}

async function refresh(): Promise<void> {
  const list = document.getElementById("audit-list");
  if (!list) return;
  try {
    const items = await api.listAudits();
    clear(list);
    if (items.length === 0) {
      list.append(el("p", { class: "muted" }, ["No auto-merges to audit. 🎉"]));
      return;
    }
    for (const it of items) list.append(auditCard(it));
  } catch (e) {
    list.textContent = (e as Error).message;
  }
}

function auditCard(it: ReviewItem): HTMLElement {
  const { task, attempt } = it;
  const stages = attempt ? stageRow(attempt.evidence) : el("span", { class: "muted" }, ["no evidence"]);
  const diff = attempt?.evidence?.diff?.trim();

  const actions = el("div", { class: "card-actions" }, [
    el("button", {
      class: "primary",
      title: "Acknowledge — this auto-merge looks correct",
      onclick: () => act(() => api.ackAudit(task.id)),
    }, ["Looks good"]),
    el("button", {
      class: "danger",
      title: "Record this as a change-failure (revert the merge in git yourself)",
      onclick: () => {
        if (!confirm(`Mark “${task.title}” as a change-failure? Revert it in git separately.`)) return;
        act(() => api.revertTask(task.id));
      },
    }, ["Revert"]),
  ]);

  return el("div", { class: "card review-card" }, [
    el("div", { class: "card-main" }, [
      el("div", { class: "card-title" }, [
        task.title,
        el("span", { class: "pill status-merged" }, ["auto-merged"]),
        task.branch ? el("code", { class: "branch" }, [task.branch]) : el("span", {}),
      ]),
      stages,
      diff ? diffBlock(diff) : el("p", { class: "muted" }, ["(no diff produced)"]),
    ]),
    actions,
  ]);
}

function stageRow(ev: Evidence): HTMLElement {
  const stages = ev?.stages ?? {};
  const chips = STAGE_ORDER.filter((s) => stages[s]).map((s) => {
    const r = stages[s];
    const cls = r.skipped ? "stage skip" : r.pass ? "stage pass" : "stage fail";
    const mark = r.skipped ? "–" : r.pass ? "✓" : "✗";
    return el("span", { class: cls, title: r.output ? r.output.slice(-4000) : "" }, [`${mark} ${s}`]);
  });
  if (chips.length === 0) return el("div", { class: "stage-row" }, [el("span", { class: "muted" }, ["no gate stages"])]);
  return el("div", { class: "stage-row" }, chips);
}

function diffBlock(diff: string): HTMLElement {
  const pre = el("pre", { class: "diff" });
  for (const line of diff.split("\n")) {
    let cls = "";
    if (line.startsWith("+") && !line.startsWith("+++")) cls = "add";
    else if (line.startsWith("-") && !line.startsWith("---")) cls = "del";
    else if (line.startsWith("@@")) cls = "hunk";
    pre.append(el("span", { class: `dl ${cls}` }, [line + "\n"]));
  }
  return el("details", { class: "diff-wrap" }, [el("summary", {}, ["Diff"]), pre]);
}

async function act(fn: () => Promise<unknown>): Promise<void> {
  try {
    await fn();
    refresh();
  } catch (e) {
    alert((e as Error).message);
  }
}

export function onAuditEvent(): void {
  if (document.getElementById("audit-list")) refresh();
}
