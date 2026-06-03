// Board: the whole factory on one kanban. Columns run left to right along the
// task lifecycle — the human gates (Approve, Decide, Accept, Audit) interleaved
// with the in-flight stages (Ready, Running, Verifying, Merged). Gate columns
// are marked "needs you"; click any card to act or steer. "Define" / "Create
// task" seed work; "Settings" carries the autonomy controls + throughput that
// used to live in the Engine room. (SPECS §10.)
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { openModal, closeModal } from "../ui.js";
import { STAGE_ORDER } from "../types.js";
import { DEFAULT_AVATAR } from "../avatar.js";
import type { Plan, Decision, ReviewItem, Task, Agent, Metrics, AgentMetrics, BigTask, Evidence, Attempt, Comment, FabrikaEvent } from "../types.js";

type ColId = "planning" | "approve" | "decide" | "ready" | "running" | "verifying" | "accept" | "audit" | "merged";
const COLUMNS: { id: ColId; label: string; gate?: boolean }[] = [
  { id: "planning", label: "Planning" },
  { id: "approve", label: "Approve", gate: true },
  { id: "decide", label: "Decide", gate: true },
  { id: "ready", label: "Ready" },
  { id: "running", label: "Running" },
  { id: "verifying", label: "Verifying" },
  { id: "accept", label: "Accept", gate: true },
  { id: "audit", label: "Audit", gate: true },
  { id: "merged", label: "Merged" },
];
const IN_FLIGHT = ["claimed", "running"];
// Big-task statuses shown in the Planning column: the request is in (or awaiting)
// planning, or planning errored. Planned/running/done big tasks move on.
const PRE_PLAN = ["draft", "planning", "error"];
const STEERABLE = ["ready", "claimed", "running", "blocked", "failed"];
const BARS = ["var(--accent)", "var(--tan)", "var(--teal)", "var(--green)", "var(--amber)", "var(--red)"];

export function renderBoard(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header board-header" }, [
      el("div", {}, [
        el("h1", {}, ["Board"]),
        el("p", { class: "muted" }, [
          "The whole factory, left to right. ",
          el("span", { class: "gate-dot" }, []),
          " columns need you — click any card to act or steer.",
        ]),
      ]),
      el("div", { class: "header-actions" }, [
        el("button", { onclick: openSettings }, ["Settings"]),
        // Hidden until pushStatus reports unpushed commits (see updatePushButton).
        el("button", {
          id: "push-btn",
          style: "display:none",
          onclick: (e: Event) => pushMain(e.currentTarget as HTMLButtonElement),
        }, ["Push"]),
        el("button", { onclick: openCreateTask }, ["Create task"]),
        el("button", { class: "primary", onclick: openDefine }, ["Define"]),
      ]),
    ]),
    el("div", { id: "board-err", class: "form-error" }, []),
    el("div", { class: "board needs-board", id: "needs-board" }, COLUMNS.map(colSkeleton)),
  );
  refresh();
}

function colSkeleton(c: (typeof COLUMNS)[number]): HTMLElement {
  const head = el("div", { class: "board-col-head" }, [
    c.gate ? el("span", { class: "gate-dot", title: "needs you" }, []) : el("span", {}),
    c.label,
    el("span", { class: "count", "data-count": c.id }, []),
  ]);
  return el("div", { class: "board-col" + (c.gate ? " gate" : ""), "data-col": c.id }, [
    head,
    el("div", { class: "board-col-body", "data-body": c.id }, []),
  ]);
}

// Monotonic generation token: bumped at the start of every refresh() and
// re-checked after the fetches resolve, so only the most recent in-flight
// refresh is allowed to paint. Bursts of WebSocket events fan into several
// overlapping refreshes; without this an earlier-started fetch could resolve
// last and clobber fresher columns with stale data.
let refreshGen = 0;
// Debounce handle: rapid onBoardEvent()-driven refreshes collapse into one
// refetch (a task moving ready→running→verifying→review emits several
// task.updated events within a couple of seconds).
let refreshTimer: ReturnType<typeof setTimeout> | null = null;
const REFRESH_DEBOUNCE_MS = 150;

async function refresh(): Promise<void> {
  const board = document.getElementById("needs-board");
  if (!board) return;
  const errBox = document.getElementById("board-err");
  const gen = ++refreshGen;
  // Independent of the column fetches: a push-status hiccup (git error) should
  // never blank the board, and vice versa. Fire-and-forget with its own catch.
  void updatePushButton();
  try {
    const [plans, decisions, reviews, audits, tasks, agents, bigTasks] = await Promise.all([
      api.listPlans(),
      api.listDecisions(),
      api.listReviews(),
      api.listAudits(),
      api.listTasks(),
      api.listAgents(),
      api.listBigTasks(),
    ]);
    // A newer refresh started while these fetches were in flight — discard
    // our (now stale) results so we don't paint over fresher columns.
    if (gen !== refreshGen) return;
    if (errBox) errBox.textContent = "";
    const auditIds = new Set(audits.map((a) => a.task.id));
    const byStatus = (s: string) => tasks.filter((t) => t.status === s);

    // Pre-plan big tasks: show the submitted request while the planner works
    // (or stalls). Once planned, the proposed Plan takes over in Approve; once
    // running/done its tasks carry it forward — so only draft/planning/error
    // land here.
    fillColumn("planning", bigTasks.filter((b) => PRE_PLAN.includes(b.status)).map((b) => bigTaskCard(b, agents)));
    fillColumn("approve", plans.filter((p) => p.status === "proposed").map(planCard));
    fillColumn("decide", decisions.map(decideCard));
    fillColumn("ready", byStatus("ready").map((t) => taskCard(t, agents)));
    fillColumn("running", tasks.filter((t) => IN_FLIGHT.includes(t.status)).map((t) => taskCard(t, agents)));
    fillColumn("verifying", byStatus("verifying").map((t) => taskCard(t, agents)));
    fillColumn("accept", reviews.map(reviewCard));
    fillColumn("audit", audits.map(auditCard));
    fillColumn("merged", byStatus("merged").filter((t) => !auditIds.has(t.id)).map((t) => taskCard(t, agents)));
  } catch (e) {
    if (gen !== refreshGen) return;
    if (errBox) errBox.textContent = (e as Error).message;
  }
}

function fillColumn(id: string, cards: HTMLElement[]): void {
  const body = document.querySelector(`[data-body="${id}"]`);
  const count = document.querySelector(`[data-count="${id}"]`);
  if (!body) return;
  body.replaceChildren();
  if (count) count.textContent = cards.length ? String(cards.length) : "";
  if (cards.length === 0) {
    body.append(el("div", { class: "board-empty" }, ["—"]));
    return;
  }
  for (const c of cards) body.append(c);
}

// ── Cards (compact; click opens an action / steer panel) ───────────────────

function card(title: string, meta: (Node | string)[], onClick: () => void): HTMLElement {
  return el("div", { class: "needs-card", onclick: onClick }, [
    el("div", { class: "needs-card-title" }, [title]),
    meta.length ? el("div", { class: "needs-card-meta" }, meta) : el("span", {}),
  ]);
}

function planCard(p: Plan): HTMLElement {
  const meta: (Node | string)[] = [el("span", { class: "tag" }, [`${p.tasks.length} tasks`])];
  if (p.openDecisions.length) meta.push(el("span", { class: "tag dep" }, [`${p.openDecisions.length} open Q`]));
  return card(p.bigTask?.title ?? "Plan", meta, () => openPlanDetail(p));
}

// bigTaskCard surfaces a submitted big task while it's being planned (or after
// planning errored), so a Define submission is visible immediately instead of
// silently churning in the background. The status pill carries the live state;
// errored cards read red and open to the failure reason. When a planner agent is
// assigned, its name appears on the card as well.
function bigTaskCard(b: BigTask, agents: Agent[]): HTMLElement {
  const meta: (Node | string)[] = [];
  const label = b.status === "planning" ? "planning…" : b.status;
  meta.push(el("span", { class: `pill status-${b.status}` }, [label]));
  if (b.status === "planning" && b.plannerAgentId) {
    meta.push(el("span", { class: "tag agent" }, [agentName(agents, b.plannerAgentId)]));
  }
  return card(b.title, meta, () => openBigTaskDetail(b, agents));
}

function openBigTaskDetail(b: BigTask, agents: Agent[]): void {
  const children: (Node | string)[] = [
    el("div", { class: "card-meta" }, [el("span", { class: `pill status-${b.status}` }, [b.status])]),
    b.intent ? el("p", { class: "card-spec" }, [b.intent]) : el("span", {}),
  ];
  for (const c of b.constraints ?? []) children.push(el("code", { class: "verify-cmd" }, [c]));
  if (b.status === "planning") {
    const who = b.plannerAgentId ? ` ${agentName(agents, b.plannerAgentId)} is` : "A planner agent is";
    children.push(el("p", { class: "muted sm" }, [`${who} decomposing this into a plan — it'll land in Approve when ready.`]));
  } else if (b.status === "draft") {
    children.push(el("p", { class: "muted sm" }, ["Queued for planning."]));
  } else if (b.status === "error") {
    children.push(el("p", { class: "form-error bigtask-error" }, [b.error || "Planning failed."]));
  }
  openModal(b.title, el("div", { class: "detail" }, children), { wide: true });
}

function decideCard(d: Decision): HTMLElement {
  return card(d.question, [el("span", { class: "tag" }, [d.taskId ? "task" : "plan"])], () => openDecideDetail(d));
}

function reviewCard(it: ReviewItem): HTMLElement {
  const t = it.task;
  return card(
    t.title,
    [el("span", { class: `tag status-${t.status}` }, [t.status]), el("span", { class: `tag risk-${t.riskTier}` }, [t.riskTier])],
    () => openReviewDetail(it),
  );
}

function auditCard(it: ReviewItem): HTMLElement {
  const t = it.task;
  return card(
    t.title,
    [el("span", { class: "tag" }, ["auto-merged"]), el("span", { class: `tag risk-${t.riskTier}` }, [t.riskTier])],
    () => openAuditDetail(it),
  );
}

function taskCard(t: Task, agents: Agent[]): HTMLElement {
  const meta: (Node | string)[] = [];
  if (t.agentId) meta.push(agentPhoto(agents, t.agentId));
  meta.push(el("span", { class: `tag risk-${t.riskTier}` }, [t.riskTier]));
  meta.push(el("span", { class: `tag priority-${t.priority}` }, [`priority: ${t.priority}`]));
  for (const tag of t.tags ?? []) meta.push(el("span", { class: "tag" }, [tag]));
  return card(t.title, meta, () => openTaskDetail(t, agents));
}

// ── Action / detail panels ─────────────────────────────────────────────────

function openPlanDetail(p: Plan): void {
  const titleOf = (id: string) => p.tasks.find((t) => t.id === id)?.title ?? id.slice(0, 6);
  const body = el("div", { class: "detail" }, [
    p.bigTask?.intent ? el("p", { class: "card-spec" }, [p.bigTask.intent]) : el("span", {}),
    el("div", { class: "plan-tasks" }, p.tasks.map((t) => planTaskRow(t, titleOf))),
    p.openDecisions.length
      ? el("div", { class: "plan-decisions" }, [
          el("div", { class: "section-h sm" }, ["Open questions"]),
          ...p.openDecisions.map((d) =>
            el("div", { class: "plan-decision" }, [
              el("span", { class: "q" }, [d.question]),
              el("span", { class: "muted hint" }, [" — answer it in Decide"]),
            ]),
          ),
        ])
      : el("span", {}),
    actionRow([
      primaryBtn("Approve plan", () => act(() => api.approvePlan(p.id))),
      dangerBtn("Reject", () => act(() => api.rejectPlan(p.id))),
    ]),
  ]);
  openModal(p.bigTask?.title ?? "Plan", body, { wide: true });
}

function planTaskRow(t: Task, titleOf: (id: string) => string): HTMLElement {
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

function openDecideDetail(d: Decision): void {
  const promote = el("input", { type: "checkbox" }) as HTMLInputElement;
  const free = el("input", { placeholder: "Or type an answer…" }) as HTMLInputElement;
  const answer = (text: string) => {
    const a = text.trim();
    if (!a) {
      alert("Pick an option or type an answer.");
      return;
    }
    act(() => api.answerDecision(d.id, a, promote.checked));
  };
  const body = el("div", { class: "detail" }, [
    d.context ? el("p", { class: "card-spec" }, [d.context]) : el("span", {}),
    d.options.length
      ? el("div", { class: "option-row" }, d.options.map((o) => el("button", { class: "option", onclick: () => answer(o) }, [o])))
      : el("span", {}),
    el("div", { class: "decision-answer" }, [free, primaryBtn("Answer", () => answer(free.value))]),
    el("label", { class: "checkbox" }, [promote, "Save as a convention (steer future runs)"]),
  ]);
  openModal(d.question, body);
}

function openReviewDetail(it: ReviewItem): void {
  const { task, attempt } = it;
  const green = task.status === "review";
  const diff = attempt?.evidence?.diff?.trim();
  const blockedReason = task.status === "blocked" && attempt ? firstLine(attempt.log) : "";
  const review = attempt?.evidence?.stages?.review;
  const reviewNote = review && !review.pass ? `Reviewer: ${review.output}` : "";

  const body = el("div", { class: "detail" }, [
    el("div", { class: "card-meta" }, [
      el("span", { class: `tag status-${task.status}` }, [task.status]),
      task.branch ? el("code", { class: "branch" }, [task.branch]) : el("span", {}),
    ]),
    blockedReason ? el("p", { class: "blocked-q" }, [blockedReason]) : el("span", {}),
    reviewNote ? el("p", { class: "blocked-q" }, [reviewNote]) : el("span", {}),
    attempt ? stageRow(attempt.evidence) : el("span", { class: "muted" }, ["no evidence"]),
    !green && attempt ? failureBlock(attempt) : el("span", {}),
    diff ? diffBlock(diff) : el("p", { class: "muted" }, ["(no diff produced)"]),
    actionRow([
      el("button", {
        class: "primary",
        disabled: !green,
        title: green ? "" : "Only green runs can be merged",
        onclick: green ? () => act(() => api.acceptTask(task.id)) : undefined,
      }, ["Merge"]),
      // Failed/escalated runs can be re-queued for a fresh attempt from scratch.
      !green
        ? el("button", { onclick: () => act(() => api.retryTask(task.id)) }, ["Retry"])
        : el("span", {}),
      dangerBtn("Kick back", () => {
        const reason = prompt("Reason for kicking this back? (optional)") ?? "";
        act(() => api.rejectTask(task.id, reason));
      }),
    ]),
  ]);
  openModal(task.title, body, { wide: true });
}

function openAuditDetail(it: ReviewItem): void {
  const { task, attempt } = it;
  const diff = attempt?.evidence?.diff?.trim();
  const body = el("div", { class: "detail" }, [
    el("div", { class: "card-meta" }, [
      el("span", { class: "tag" }, ["auto-merged"]),
      task.branch ? el("code", { class: "branch" }, [task.branch]) : el("span", {}),
    ]),
    attempt ? stageRow(attempt.evidence) : el("span", { class: "muted" }, ["no evidence"]),
    diff ? diffBlock(diff) : el("p", { class: "muted" }, ["(no diff produced)"]),
    actionRow([
      primaryBtn("Looks good", () => act(() => api.ackAudit(task.id))),
      dangerBtn("Revert", () => {
        if (!confirm(`Mark “${task.title}” as a change-failure? Revert it in git separately.`)) return;
        act(() => api.revertTask(task.id));
      }),
    ]),
  ]);
  openModal(task.title, body, { wide: true });
}

// openTaskDetail covers the non-gate lifecycle cards (ready/running/verifying/
// merged): spec + meta, live steering for in-flight work, and lazily-loaded
// gate evidence (stages + diff) from the latest attempt.
function openTaskDetail(t: Task, agents: Agent[]): void {
  const meta: (Node | string)[] = [
    el("span", { class: `tag status-${t.status}` }, [t.status]),
    el("span", { class: `tag risk-${t.riskTier}` }, [t.riskTier]),
  ];
  if (t.agentId) meta.push(el("span", { class: "tag agent" }, [agentName(agents, t.agentId)]));
  if (t.branch) meta.push(el("code", { class: "branch" }, [t.branch]));
  for (const tag of t.tags ?? []) meta.push(el("span", { class: "tag" }, [tag]));

  const children: (Node | string)[] = [
    el("div", { class: "card-meta" }, meta),
    t.spec ? el("p", { class: "card-spec" }, [t.spec]) : el("span", {}),
  ];
  for (const c of t.acceptance?.verifyCmds ?? []) children.push(el("code", { class: "verify-cmd" }, [c]));
  if (STEERABLE.includes(t.status)) children.push(steerRow(t, agents));
  children.push(el("div", { id: `task-evidence-${t.id}`, class: "task-evidence" }, []));
  children.push(commentsSection(t.id));

  openTaskId = t.id;
  openModal(t.title, el("div", { class: "detail" }, children), { wide: true });
  loadEvidence(t.id);
  loadComments(t.id);
}

// The task whose detail modal is currently open (if any). Comment websocket
// events that target this task reload the in-modal list live; cleared so the
// list paints under the agent labels we already have.
let openTaskId: string | null = null;

// commentsSection builds the comments block of the task detail: a heading, the
// (lazily-loaded) list, and a composer that posts a note then reloads the list.
function commentsSection(id: string): HTMLElement {
  const input = el("textarea", { class: "comment-input", placeholder: "Add a comment…", rows: "2" }) as HTMLTextAreaElement;
  const err = el("div", { class: "form-error" }, []);
  const submit = async () => {
    const text = input.value.trim();
    if (!text) return;
    err.textContent = "";
    try {
      await api.addComment(id, text);
      input.value = "";
      loadComments(id);
    } catch (e) {
      err.textContent = (e as Error).message;
    }
  };
  return el("div", { class: "comments" }, [
    el("div", { class: "section-h sm" }, ["Comments"]),
    el("div", { id: `task-comments-${id}`, class: "comment-list" }, []),
    el("div", { class: "comment-compose" }, [
      input,
      err,
      el("div", { class: "form-actions" }, [primaryBtn("Comment", submit)]),
    ]),
  ]);
}

async function loadComments(id: string): Promise<void> {
  try {
    const comments = await api.listComments(id);
    const slot = document.getElementById(`task-comments-${id}`);
    if (!slot) return;
    clear(slot);
    if (comments.length === 0) {
      slot.append(el("div", { class: "comment-empty muted sm" }, ["No comments yet."]));
      return;
    }
    for (const c of comments) slot.append(commentItem(c));
  } catch {
    /* comments are best-effort */
  }
}

// commentItem renders one note oldest-first: a "You" label for human comments,
// the authoring agent's id for agent comments, above the body text.
function commentItem(c: Comment): HTMLElement {
  const who = c.authorType === "user" ? "You" : (c.authorId || c.authorType);
  return el("div", { class: "comment" }, [
    el("div", { class: "comment-author" }, [who]),
    el("div", { class: "comment-body" }, [c.body]),
  ]);
}

async function loadEvidence(id: string): Promise<void> {
  try {
    const { attempts } = await api.getTask(id);
    const slot = document.getElementById(`task-evidence-${id}`);
    if (!slot) return;
    clear(slot);
    const a = attempts && attempts.length ? attempts[attempts.length - 1] : null;
    if (!a) return;
    slot.append(stageRow(a.evidence));
    if (a.result !== "pass") slot.append(failureBlock(a));
    const diff = a.evidence?.diff?.trim();
    if (diff) slot.append(diffBlock(diff));
  } catch {
    /* evidence is best-effort */
  }
}

function steerRow(t: Task, agents: Agent[]): HTMLElement {
  const select = el("select", {
    class: "assign-select",
    title: "Pin this task to an agent",
    onchange: (e: Event) => act(() => api.assignTask(t.id, (e.target as HTMLSelectElement).value)),
  }) as HTMLSelectElement;
  select.append(el("option", { value: "" }, ["auto-route"]));
  for (const a of agents) {
    const opt = el("option", { value: a.id }, [a.name]) as HTMLOptionElement;
    if (a.id === t.preferredAgentId) opt.selected = true;
    select.append(opt);
  }
  return el("div", { class: "card-actions" }, [
    select,
    dangerBtn("Cancel task", () => {
      if (!confirm(`Cancel “${t.title}”?`)) return;
      act(() => api.cancelTask(t.id));
    }),
  ]);
}

// ── Settings (autonomy controls + throughput; was the Engine room) ─────────

function openSettings(): void {
  const body = el("div", { class: "detail" }, [el("p", { class: "muted" }, ["Loading…"])]);
  openModal("Factory settings", body, { wide: true });
  loadSettings(body);
}

async function loadSettings(body: HTMLElement): Promise<void> {
  try {
    const [m, agents] = await Promise.all([api.metrics(), api.listAgents()]);
    clear(body);
    const pct = (n: number) => `${Math.round(n * 100)}%`;
    body.append(
      el("div", { class: "section-h sm" }, ["Throughput"]),
      el("div", { class: "stat-grid" }, [
        stat("In flight", String(m.wip), m.wipCap > 0 ? `/ ${m.wipCap}` : undefined),
        stat("Ready", String(m.ready)),
        stat("In review", String(m.inReview)),
        stat("Merged", String(m.merged)),
      ]),
      el("div", { class: "section-h sm" }, ["Trust + autonomy"]),
      el("div", { class: "stat-grid" }, [
        stat("Touches / unit", m.merged > 0 ? m.touchesPerUnit.toFixed(2) : "—"),
        stat("Change-fail rate", m.merged > 0 ? pct(m.changeFailRate) : "—"),
        stat("Auto-merged", m.merged > 0 ? String(m.autoMerged) : "0", m.merged > 0 ? `· ${pct(m.autoMergeShare)}` : undefined),
        stat("Audit queue", String(m.auditQueue)),
      ]),
      autonomyControls(m),
      el("div", { class: "section-h sm" }, ["Agents by share of work"]),
      shareTable(m),
    );
    void agents;
  } catch (e) {
    clear(body);
    body.append(el("p", { class: "form-error" }, [(e as Error).message]));
  }
}

function autonomyControls(m: Metrics): HTMLElement {
  const wip = el("input", { type: "number", min: "0", value: String(m.wipCap || 0), title: "0 = unlimited" }) as HTMLInputElement;
  const setWip = el("form", {
    class: "wip-cap",
    onsubmit: (e: Event) => {
      e.preventDefault();
      saveSetting({ wip_cap: String(parseInt(wip.value, 10) || 0) });
    },
  }, [el("label", {}, ["WIP cap"]), wip, el("button", { class: "primary", type: "submit" }, ["Set"])]);

  const rate = el("input", {
    type: "number", min: "0", max: "1", step: "0.05",
    value: String(m.auditRate ?? 0), title: "Fraction of auto-merges to sample for audit (0–1)",
  }) as HTMLInputElement;
  const mutation = el("input", { type: "checkbox", title: "Run mutation testing on green branches before auto-merge" }) as HTMLInputElement;
  mutation.checked = m.mutationTesting;
  mutation.onchange = () => saveSetting({ mutation_testing: mutation.checked ? "on" : "off" });

  const setRate = el("form", {
    class: "wip-cap",
    onsubmit: (e: Event) => {
      e.preventDefault();
      saveSetting({ audit_rate: String(parseFloat(rate.value) || 0) });
    },
  }, [
    el("label", {}, ["Audit rate"]),
    rate,
    el("button", { class: "primary", type: "submit" }, ["Set"]),
    el("label", { class: "checkbox" }, [mutation, "mutation testing"]),
  ]);

  return el("div", { class: "metrics-bar", style: "margin-top:16px" }, [setWip, setRate]);
}

async function saveSetting(s: Record<string, string>): Promise<void> {
  try {
    await api.putSettings(s);
    const body = document.querySelector(".modal-body .detail") as HTMLElement | null;
    if (body) loadSettings(body);
    refresh();
  } catch (e) {
    alert((e as Error).message);
  }
}

function shareTable(m: Metrics): HTMLElement {
  if (m.agents.length === 0) return el("p", { class: "share-empty" }, ["No agents registered."]);
  const totalMerged = m.agents.reduce((s, a) => s + a.merged, 0);
  const byLoad = totalMerged === 0;
  // In-flight load counts both running tasks and active planning runs, so the
  // planner doesn't read as idle while it's decomposing a big task.
  const load = (a: AgentMetrics) => a.running + (a.planning ?? 0);
  const weight = (a: AgentMetrics) => (byLoad ? load(a) : a.merged);
  const total = m.agents.reduce((s, a) => s + weight(a), 0);
  const ranked = [...m.agents].sort((x, y) => weight(y) - weight(x));

  const tbody = el("tbody", {});
  ranked.forEach((a, i) => {
    const w = weight(a);
    const share = total > 0 ? w / total : 0;
    const planning = (a.planning ?? 0) > 0;
    const busy = a.running > 0 || planning;
    const pill = busy ? (a.running > 0 ? "working" : "planning") : "idle";
    tbody.append(
      el("tr", {}, [
        el("td", { class: "who" }, [
          el("span", { class: busy ? "pill busy" : "pill idle" }, [pill]),
          " " + a.name,
        ]),
        el("td", {}, [
          el("div", { class: "share-cell" }, [
            el("div", { class: "share-track" }, [
              el("div", { class: "share-fill", style: `width:${Math.max(share * 100, w > 0 ? 4 : 0)}%;background:${BARS[i % BARS.length]}` }, []),
            ]),
            el("span", { class: "share-pct" }, [`${Math.round(share * 100)}%`]),
          ]),
        ]),
        el("td", { class: "num" }, [String(byLoad ? load(a) : a.merged)]),
      ]),
    );
  });
  return el("table", { class: "share-table" }, [
    el("thead", {}, [
      el("tr", {}, [
        el("th", {}, ["Agent"]),
        el("th", {}, [byLoad ? "Share of load" : "Share of work"]),
        el("th", { class: "num" }, [byLoad ? "In flight" : "Shipped"]),
      ]),
    ]),
    tbody,
  ]);
}

function stat(label: string, value: string, unit?: string): HTMLElement {
  const v = el("div", { class: "stat-value" }, [value]);
  if (unit) v.append(el("span", { class: "unit" }, [` ${unit}`]));
  return el("div", { class: "stat" }, [el("div", { class: "stat-label" }, [label]), v]);
}

// ── Define / Create-task forms ─────────────────────────────────────────────

function openDefine(): void {
  const title = el("input", { placeholder: "Outcome (e.g. Users can log in with email)" }) as HTMLInputElement;
  const intent = el("textarea", { placeholder: "The why + desired outcome. What does done look like?", rows: "5" }) as HTMLTextAreaElement;
  const constraints = el("textarea", { placeholder: "Constraints, one per line (e.g. PCI-compliant, works on mobile)", rows: "3" }) as HTMLTextAreaElement;
  const err = el("div", { class: "form-error" });

  const form = el("form", {
    class: "modal-form",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      err.textContent = "";
      try {
        await api.createBigTask({ title: title.value.trim(), intent: intent.value.trim(), constraints: lines(constraints.value) });
        closeModal();
        refresh();
      } catch (e2) {
        err.textContent = (e2 as Error).message;
      }
    },
  }, [
    field("Outcome", title),
    field("Intent", intent),
    field("Constraints", constraints),
    el("p", { class: "muted sm" }, ["A planner agent turns this into a plan that lands in Approve."]),
    err,
    el("div", { class: "form-actions" }, [el("button", { class: "primary", type: "submit" }, ["Define big task"])]),
  ]);
  openModal("Define a big task", form);
}

function openCreateTask(): void {
  const title = el("input", { placeholder: "Title (e.g. Add /healthz endpoint)" }) as HTMLInputElement;
  const spec = el("textarea", { placeholder: "What to build, where, and any constraints…", rows: "4" }) as HTMLTextAreaElement;
  const verify = el("textarea", { placeholder: "Verify commands, one per line (e.g. go test ./...)", rows: "3" }) as HTMLTextAreaElement;
  const tags = el("input", { placeholder: "Tags, comma-separated (go, api)" }) as HTMLInputElement;
  const priority = el("select", {}, [
    el("option", { value: "low" }, ["Low"]),
    el("option", { value: "medium" }, ["Medium"]),
    el("option", { value: "high" }, ["High"]),
  ]) as HTMLSelectElement;
  priority.value = "medium";
  const err = el("div", { class: "form-error" });

  const form = el("form", {
    class: "modal-form",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      err.textContent = "";
      try {
        await api.createTask({
          title: title.value.trim(),
          spec: spec.value.trim(),
          acceptance: { verifyCmds: lines(verify.value), heldOut: [], properties: [], lockedGlobs: [] },
          tags: tags.value.split(",").map((s) => s.trim()).filter(Boolean),
          priority: priority.value,
        });
        closeModal();
        refresh();
      } catch (e2) {
        err.textContent = (e2 as Error).message;
      }
    },
  }, [
    field("Title", title),
    field("Spec", spec),
    field("Verify commands", verify),
    field("Tags", tags),
    field("Priority", priority),
    el("p", { class: "muted sm" }, ["Verify commands are the machine-checkable acceptance contract."]),
    err,
    el("div", { class: "form-actions" }, [el("button", { class: "primary", type: "submit" }, ["Create task"])]),
  ]);
  openModal("Create a task", form);
}

// ── Shared bits ────────────────────────────────────────────────────────────

function agentName(agents: Agent[], id: string): string {
  return agents.find((a) => a.id === id)?.name ?? "—";
}

// Agent avatar for task cards: photo stands in for the name, name shows on hover.
function agentPhoto(agents: Agent[], id: string): HTMLElement {
  const a = agents.find((x) => x.id === id);
  const name = a?.name ?? "—";
  return el("img", { class: "card-agent-photo", src: a?.photo || DEFAULT_AVATAR, alt: name, title: name });
}

function field(label: string, control: HTMLElement): HTMLElement {
  return el("div", { class: "field" }, [el("label", {}, [label]), control]);
}

function actionRow(buttons: HTMLElement[]): HTMLElement {
  return el("div", { class: "card-actions" }, buttons);
}

function primaryBtn(label: string, onclick: () => void): HTMLElement {
  return el("button", { class: "primary", onclick }, [label]);
}

function dangerBtn(label: string, onclick: () => void): HTMLElement {
  return el("button", { class: "danger", onclick }, [label]);
}

function lines(s: string): string[] {
  return s.split("\n").map((x) => x.trim()).filter(Boolean);
}

function firstLine(s: string): string {
  return (s || "").split("\n")[0];
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

// failureBlock surfaces *why* a run failed, in the panel body rather than a
// chip tooltip: the first failing gate stage and its captured output — with an
// explicit note when a command exited non-zero but printed nothing (e.g. a
// `grep -q` acceptance check) — plus the agent's own log, which carries crashes
// (a missing model, a dead provider) that abort before the gate ever runs.
function failureBlock(attempt: Attempt): HTMLElement {
  const stages = attempt.evidence?.stages ?? {};
  const failed = STAGE_ORDER.find((s) => stages[s] && !stages[s].skipped && !stages[s].pass);
  const children: HTMLElement[] = [];
  let stageOut = "";
  if (failed) {
    const r = stages[failed];
    stageOut = (r.output ?? "").trim();
    children.push(el("p", { class: "fail-head" }, [`✗ ${failed} failed (exit ${r.exitCode})`]));
    children.push(
      stageOut
        ? el("pre", { class: "fail-out" }, [r.output.slice(-8000)])
        : el("p", { class: "muted" }, [`(command exited ${r.exitCode} with no output — check the agent log below)`]),
    );
  }
  const log = (attempt.log ?? "").trim();
  if (log) {
    // Expand the log by default when the failing stage gave us nothing useful —
    // it's the only place the real cause lives in that case.
    children.push(el("details", { class: "log-wrap", open: !stageOut }, [
      el("summary", {}, ["Agent log"]),
      el("pre", { class: "fail-out" }, [log.slice(-8000)]),
    ]));
  }
  if (children.length === 0) return el("span", {});
  return el("div", { class: "failure" }, children);
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

// updatePushButton shows the Push button only when the integration branch has
// commits its remote lacks, labelled with the count ("Push 3"). Hidden when
// up-to-date, when no remote is configured, or when the status check fails.
async function updatePushButton(): Promise<void> {
  const btn = document.getElementById("push-btn") as HTMLButtonElement | null;
  if (!btn || btn.disabled) return; // don't fight a push in flight
  try {
    const s = await api.pushStatus();
    btn.style.display = s.canPush ? "" : "none";
    if (s.canPush) btn.textContent = `Push ${s.ahead}`;
  } catch {
    btn.style.display = "none";
  }
}

// pushMain ships the integration branch to its remote. It disables the button
// and shows "Pushing…" for the duration (network-bound), then reports git's
// summary — or the error — via an alert. Afterwards the status re-check hides
// the button (a successful push means nothing is left to ship).
async function pushMain(btn: HTMLButtonElement): Promise<void> {
  const label = btn.textContent ?? "Push";
  btn.disabled = true;
  btn.textContent = "Pushing…";
  try {
    const res = await api.push();
    alert(res.detail || "Pushed.");
  } catch (e) {
    alert((e as Error).message);
  } finally {
    btn.disabled = false;
    btn.textContent = label;
    void updatePushButton();
  }
}

async function act(fn: () => Promise<unknown>): Promise<void> {
  try {
    await fn();
    closeModal();
    refresh();
  } catch (e) {
    alert((e as Error).message);
  }
}

// onBoardEvent refreshes the columns when any plan/decision/task/bigtask event
// arrives, keeping the board live without a reload. Rapid bursts are coalesced
// behind a short debounce so a single task churning through stages triggers one
// refetch rather than half a dozen overlapping ones. (renderBoard() and act()
// still call refresh() directly for an immediate, un-debounced paint.)
export function onBoardEvent(e?: FabrikaEvent): void {
  if (!document.getElementById("needs-board")) return;
  // A new comment on the currently-open task: reload just that modal's list so
  // it updates live (the debounced board refresh below won't touch the modal).
  if (e?.type === "task.comment.added" && openTaskId) {
    const c = e.payload as Comment | null;
    if (c && c.taskId === openTaskId) loadComments(openTaskId);
  }
  if (refreshTimer !== null) clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => {
    refreshTimer = null;
    refresh();
  }, REFRESH_DEBOUNCE_MS);
}
