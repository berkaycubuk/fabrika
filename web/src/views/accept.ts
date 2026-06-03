// Accept screen: the review queue. Each item is a task that has run through the
// gate, shown with its stage results + branch diff. Merge green work or kick it
// back. (SPECS.md §10 "Accept", §9 merge gate.)
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { STAGE_ORDER, type ReviewItem, type Evidence } from "../types.js";

export function renderAccept(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Accept"]),
      el("p", { class: "muted" }, [
        "Work that ran through the gate and needs your judgment. Merge what's right; kick back the rest.",
      ]),
    ]),
    el("div", { id: "review-list", class: "card-list" }, ["Loading…"]),
  );
  refresh();
}

async function refresh(): Promise<void> {
  const list = document.getElementById("review-list");
  if (!list) return;
  try {
    const items = await api.listReviews();
    clear(list);
    if (items.length === 0) {
      list.append(el("p", { class: "muted" }, ["Nothing waiting on you. 🎉"]));
      return;
    }
    for (const it of items) list.append(reviewCard(it));
  } catch (e) {
    list.textContent = (e as Error).message;
  }
}

function reviewCard(it: ReviewItem): HTMLElement {
  const { task, attempt } = it;
  const green = task.status === "review";

  const stages = attempt ? stageRow(attempt.evidence) : el("span", { class: "muted" }, ["no evidence"]);
  const diff = attempt?.evidence?.diff?.trim();
  const blockedReason =
    task.status === "blocked" && attempt ? firstLine(attempt.log) : "";

  const actions = el("div", { class: "card-actions" }, [
    el("button", {
      class: "primary",
      disabled: !green,
      title: green ? "" : "Only green runs can be merged",
      onclick: green ? () => act(() => api.acceptTask(task.id)) : undefined,
    }, ["Merge"]),
    el("button", {
      class: "danger",
      onclick: () => {
        const reason = prompt("Reason for kicking this back? (optional)") ?? "";
        act(() => api.rejectTask(task.id, reason));
      },
    }, ["Kick back"]),
  ]);

  return el("div", { class: "card review-card" }, [
    el("div", { class: "card-main" }, [
      el("div", { class: "card-title" }, [
        task.title,
        el("span", { class: `pill status-${task.status}` }, [task.status]),
        task.branch ? el("code", { class: "branch" }, [task.branch]) : el("span", {}),
      ]),
      blockedReason ? el("p", { class: "blocked-q" }, [blockedReason]) : el("span", {}),
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
    const chip = el("span", { class: cls, title: r.output ? r.output.slice(-4000) : "" }, [`${mark} ${s}`]);
    return chip;
  });
  if (chips.length === 0) return el("div", { class: "stage-row" }, [el("span", { class: "muted" }, ["no gate stages"])]);
  return el("div", { class: "stage-row" }, chips);
}

function diffBlock(diff: string): HTMLElement {
  const pre = el("pre", { class: "diff" });
  // Lightweight diff coloring by line prefix.
  for (const line of diff.split("\n")) {
    let cls = "";
    if (line.startsWith("+") && !line.startsWith("+++")) cls = "add";
    else if (line.startsWith("-") && !line.startsWith("---")) cls = "del";
    else if (line.startsWith("@@")) cls = "hunk";
    pre.append(el("span", { class: `dl ${cls}` }, [line + "\n"]));
  }
  const wrap = el("details", { class: "diff-wrap" }, [
    el("summary", {}, ["Diff"]),
    pre,
  ]);
  return wrap;
}

function firstLine(s: string): string {
  return (s || "").split("\n")[0];
}

async function act(fn: () => Promise<unknown>): Promise<void> {
  try {
    await fn();
    refresh();
  } catch (e) {
    alert((e as Error).message);
  }
}

export function onReviewEvent(): void {
  if (document.getElementById("review-list")) refresh();
}
