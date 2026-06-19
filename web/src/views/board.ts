// Board: the whole factory on one kanban. Columns run left to right along the
// task lifecycle — the human gates (Approve, Decide, Accept, Audit) interleaved
// with the in-flight stages (Ready, Running, Verifying, Merged). Gate columns
// are marked "needs you"; click any card to act or steer. "Define big task" /
// "Create task" seed work; metrics + autonomy controls live in the Factory
// view. (SPECS §10.)
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { showToast } from "./toast.js";
import { undoToastSpec } from "./undo-actions.js";
import type { ToastSpec } from "./undo-actions.js";
import { button, pill, tag, field, formatTokens, formatTokensShort } from "../components.js";
import { openModal, closeModal, promptModal } from "../ui.js";
import { STAGE_ORDER } from "../types.js";
import { DEFAULT_AVATAR } from "../avatar.js";
import type { Plan, Decision, ReviewItem, Task, Agent, BigTask, Evidence, Attempt, Usage, Comment, FabrikaEvent, Release, Heartbeat, Transition } from "../types.js";
import { renderTransitionTimeline } from "./history.js";
import { registerReleaseListener } from "../ws.js";
import { pushStatusLabel } from "../push.js";
import { renderDiff } from "./diff-view.js";
import { attachmentGallery, imageAttach } from "./attachment.js";
import { ciBadge } from "./ci-badge.js";
import { emptyFilter, matchesFilter, countLabel, distinctValues, type CardFilter, type Filterable } from "./board-filter.js";
import { mentionQuery, matchAgents, applyMention } from "../mentions.js";

let filterState: CardFilter = emptyFilter();
let storedPlans: Plan[] = [];
let storedDecisions: Decision[] = [];
let storedReviews: ReviewItem[] = [];
let storedAudits: ReviewItem[] = [];
let storedTasks: Task[] = [];
let storedAgents: Agent[] = [];
let storedBigTasks: BigTask[] = [];

type CardItem = { el: HTMLElement; filterable: Filterable };
const colCards: Record<string, CardItem[]> = {};

// Latest liveness pulse per in-flight task, keyed by task id. Heartbeat events
// update this and repaint the card in place; a fresh board render repaints from
// it so a just-rendered running card already shows its last known pulse.
const liveness = new Map<string, Heartbeat>();

// QUIET_AFTER is the silence (seconds) past which a running card turns amber —
// the agent is alive but hasn't produced output, the early sign of a stall well
// before the engine's idle-timeout kill.
const QUIET_AFTER = 45;

type ColId = "backlog" | "planning" | "approve" | "decide" | "ready" | "running" | "verifying" | "accept" | "audit" | "merged" | "closed";
const COLUMNS: { id: ColId; label: string; gate?: boolean }[] = [
  { id: "backlog", label: "Backlog" },
  { id: "planning", label: "Planning" },
  { id: "approve", label: "Approve", gate: true },
  { id: "decide", label: "Decide", gate: true },
  { id: "ready", label: "Ready" },
  { id: "running", label: "Running" },
  { id: "verifying", label: "Verifying" },
  { id: "accept", label: "Accept", gate: true },
  { id: "audit", label: "Audit", gate: true },
  { id: "merged", label: "Merged" },
  { id: "closed", label: "Closed" },
];
export const IN_FLIGHT = ["claimed", "running"];
// Big-task statuses shown in the Planning column: the request is in (or awaiting)
// planning, or planning errored. Planned/running/done big tasks move on.
const PRE_PLAN = ["draft", "planning", "error"];
const STEERABLE = ["ready", "claimed", "running", "blocked", "failed"];

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
      el("input", {
        id: "board-search",
        class: "board-search",
        type: "text",
        placeholder: "Search cards…",
        oninput: (e: Event) => {
          filterState.search = (e.target as HTMLInputElement).value;
          paint();
        },
      }),
      el("div", { class: "header-actions" }, [
        // Hidden until pushStatus reports unpushed commits (see updatePushButton).
        el("button", {
          id: "push-btn",
          style: "display:none",
          onclick: (e: Event) => pushMain(e.currentTarget as HTMLButtonElement),
        }, ["Push"]),
        button("Create task", { onclick: openCreateTask }),
        button("Define big task", { variant: "primary", onclick: openDefine }),
      ]),
    ]),
    el("div", { id: "board-err", class: "form-error" }, []),
    // Release strip: always visible when deploy is enabled; shows latest release.
    el("div", { id: "release-strip", class: "release-strip", style: "display:none" }, []),
    el("div", { class: "board needs-board", id: "needs-board" }, COLUMNS.map(colSkeleton)),
  );
  setupBoardKeys();
  setupBacklogDrag();
  refresh();
  registerReleaseListener(updateReleaseUI);
}

function colSkeleton(c: (typeof COLUMNS)[number]): HTMLElement {
  const filterBtn = el("button", {
    class: "col-filter-btn",
    title: "Filter column",
    onclick: (e: Event) => {
      e.stopPropagation();
      openColFilter(c.id, filterBtn);
    },
  }, ["\u25BD"]);
  const head = el("div", { class: "board-col-head" }, [
    c.gate ? el("span", { class: "gate-dot", title: "needs you" }, []) : el("span", {}),
    el("span", { class: "board-col-label-group" }, [
      c.label,
      filterBtn,
    ]),
    el("span", { class: "count", "data-count": c.id }, []),
  ]);
  return el("div", { class: "board-col" + (c.gate ? " gate" : ""), "data-col": c.id }, [
    head,
    el("div", { class: "board-col-body", "data-body": c.id }, []),
  ]);
}

function isEditing(): boolean {
  const el = document.activeElement;
  if (!el) return false;
  const tag = el.tagName.toLowerCase();
  if (tag === "input" || tag === "textarea" || tag === "select") return true;
  return (el as HTMLElement).isContentEditable;
}

let boardKeyHandler: ((e: KeyboardEvent) => void) | null = null;

function setupBoardKeys(): void {
  if (boardKeyHandler) {
    document.removeEventListener("keydown", boardKeyHandler);
  }
  boardKeyHandler = (e: KeyboardEvent) => {
    if (!document.getElementById("needs-board")) return;
    if (e.key === "/" && !isEditing()) {
      e.preventDefault();
      const input = document.getElementById("board-search") as HTMLInputElement | null;
      input?.focus();
      return;
    }
    if (e.key === "Escape") {
      const openMenu = document.querySelector(".col-filter-menu");
      if (openMenu) {
        openMenu.remove();
        return;
      }
      const input = document.getElementById("board-search") as HTMLInputElement | null;
      if (input && (document.activeElement === input || filterState.search.trim() !== "")) {
        input.value = "";
        filterState = emptyFilter();
        paint();
        input.blur();
      }
    }
  };
  document.addEventListener("keydown", boardKeyHandler);
}

function setupBacklogDrag(): void {
  const body = document.querySelector('[data-body="backlog"]');
  if (!body) return;

  let dragging: HTMLElement | null = null;

  body.addEventListener("dragstart", (e: Event) => {
    const target = (e.target as HTMLElement).closest<HTMLElement>("[data-bigtask-id]");
    if (!target) return;
    dragging = target;
    (e as DragEvent).dataTransfer?.setData("text/plain", target.dataset.bigtaskId ?? "");
    target.classList.add("drag-active");
  });

  body.addEventListener("dragover", (e: Event) => {
    e.preventDefault();
    const de = e as DragEvent;
    const over = (de.target as HTMLElement).closest<HTMLElement>("[data-bigtask-id]");
    if (!over || over === dragging || !dragging) return;
    const rect = over.getBoundingClientRect();
    if (de.clientY < rect.top + rect.height / 2) {
      body.insertBefore(dragging, over);
    } else {
      over.after(dragging);
    }
  });

  body.addEventListener("drop", (e: Event) => {
    e.preventDefault();
    if (!dragging) return;
    dragging.classList.remove("drag-active");
    const ids = Array.from(body.querySelectorAll<HTMLElement>("[data-bigtask-id]"))
      .map((c) => c.dataset.bigtaskId!)
      .filter(Boolean);
    dragging = null;
    void api.reorderBigTasks(ids);
  });

  body.addEventListener("dragend", () => {
    if (dragging) {
      dragging.classList.remove("drag-active");
      dragging = null;
    }
  });
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
  void updateReleaseUI();
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
    storedPlans = plans;
    storedDecisions = decisions;
    storedReviews = reviews;
    storedAudits = audits;
    storedTasks = tasks;
    storedAgents = agents;
    storedBigTasks = bigTasks;
    paint();
  } catch (e) {
    if (gen !== refreshGen) return;
    if (errBox) errBox.textContent = (e as Error).message;
  }
}

function fillColumn(id: string, cards: CardItem[]): void {
  const body = document.querySelector(`[data-body="${id}"]`);
  const count = document.querySelector(`[data-count="${id}"]`);
  if (!body) return;
  body.replaceChildren();
  const total = cards.length;
  const filtered = cards.filter((c) => matchesFilter(c.filterable, filterState));
  if (count) count.textContent = countLabel(filtered.length, total);
  colCards[id] = cards;
  const btn = document.querySelector(`[data-col="${id}"] .col-filter-btn`);
  if (btn) {
    const active = filterState.risk.length > 0 || filterState.agent.length > 0 || filterState.status.length > 0;
    btn.classList.toggle("active", active);
  }
  const existingMenu = document.querySelector(`[data-col="${id}"] .col-filter-menu`);
  if (existingMenu) existingMenu.remove();
  if (filtered.length === 0) {
    body.append(el("div", { class: "board-empty" }, ["empty"]));
    return;
  }
  for (const c of filtered) body.append(c.el);
}

function openColFilter(colId: string, btn: HTMLElement): void {
  const existing = document.querySelector(".col-filter-menu");
  if (existing) {
    if (existing.closest(`[data-col="${colId}"]`)) {
      existing.remove();
      return;
    }
    existing.remove();
  }
  const cards = colCards[colId] || [];
  const filterables = cards.map((c) => c.filterable);
  const risks = distinctValues(filterables, "riskTier");
  const agentIds = distinctValues(filterables, "agentId");
  const statuses = distinctValues(filterables, "pushStatus");

  const rows: HTMLElement[] = [];
  const onChange = () => paint();

  if (risks.length > 0) {
    rows.push(el("div", { class: "col-filter-group" }, [
      el("div", { class: "col-filter-label" }, ["Risk"]),
      ...risks.map((r) => {
        const ckd = filterState.risk.includes(r);
        return el("label", { class: "col-filter-checkbox" }, [
          el("input", {
            type: "checkbox",
            checked: ckd,
            onchange: () => {
              if (ckd) filterState.risk = filterState.risk.filter((x) => x !== r);
              else filterState.risk = [...filterState.risk, r];
              onChange();
            },
          }),
          r,
        ]);
      }),
    ]));
  }
  if (agentIds.length > 0) {
    rows.push(el("div", { class: "col-filter-group" }, [
      el("div", { class: "col-filter-label" }, ["Agent"]),
      ...agentIds.map((id) => {
        const ckd = filterState.agent.includes(id);
        const display = storedAgents.find((a) => a.id === id)?.name ?? id;
        return el("label", { class: "col-filter-checkbox" }, [
          el("input", {
            type: "checkbox",
            checked: ckd,
            onchange: () => {
              if (ckd) filterState.agent = filterState.agent.filter((x) => x !== id);
              else filterState.agent = [...filterState.agent, id];
              onChange();
            },
          }),
          display,
        ]);
      }),
    ]));
  }
  if (statuses.length > 0) {
    rows.push(el("div", { class: "col-filter-group" }, [
      el("div", { class: "col-filter-label" }, ["Push Status"]),
      ...statuses.map((s) => {
        const ckd = filterState.status.includes(s);
        return el("label", { class: "col-filter-checkbox" }, [
          el("input", {
            type: "checkbox",
            checked: ckd,
            onchange: () => {
              if (ckd) filterState.status = filterState.status.filter((x) => x !== s);
              else filterState.status = [...filterState.status, s];
              onChange();
            },
          }),
          s,
        ]);
      }),
    ]));
  }
  if (rows.length === 0) return;

  const menu = el("div", { class: "col-filter-menu" }, rows);
  btn.after(menu);

  const clickOut = (e: MouseEvent) => {
    if (!menu.contains(e.target as Node) && e.target !== btn) {
      menu.remove();
      document.removeEventListener("click", clickOut);
    }
  };
  setTimeout(() => document.addEventListener("click", clickOut), 0);
}

function paint(): void {
  const board = document.getElementById("needs-board");
  if (!board) return;
  const errBox = document.getElementById("board-err");
  try {
    const auditIds = new Set(storedAudits.map((a) => a.task.id));
    const byStatus = (s: string) => storedTasks.filter((t) => t.status === s);

    // Drop liveness for tasks no longer in flight so a stale pulse can't linger
    // (or briefly reappear if the id is reused on retry).
    const inFlight = new Set(storedTasks.filter((t) => IN_FLIGHT.includes(t.status)).map((t) => t.id));
    for (const id of liveness.keys()) if (!inFlight.has(id)) liveness.delete(id);

    const backlogItems = storedBigTasks.filter((b) => b.status === "backlog").map((b) => {
      const item = bigTaskCard(b, storedAgents);
      item.el.draggable = true;
      return item;
    });
    fillColumn("backlog", backlogItems);
    fillColumn("planning", storedBigTasks.filter((b) => PRE_PLAN.includes(b.status)).map((b) => bigTaskCard(b, storedAgents)));
    fillColumn("approve", storedPlans.filter((p) => p.status === "proposed").map((p) => planCard(p, storedAgents)));
    fillColumn("decide", storedDecisions.map(decideCard));
    fillColumn("ready", byStatus("ready").map((t) => taskCard(t, storedAgents)));
    fillColumn("running", storedTasks.filter((t) => IN_FLIGHT.includes(t.status)).map((t) => taskCard(t, storedAgents)));
    fillColumn("verifying", byStatus("verifying").map((t) => taskCard(t, storedAgents)));
    fillColumn("accept", storedReviews.map((r) => reviewCard(r, storedAgents)));
    fillColumn("audit", storedAudits.map(auditCard));
    fillColumn("merged", byStatus("merged").filter((t) => !auditIds.has(t.id)).map((t) => taskCard(t, storedAgents)));
    fillColumn("closed", byStatus("closed").map((t) => taskCard(t, storedAgents)));
  } catch (e) {
    if (errBox) errBox.textContent = (e as Error).message;
  }
}

// ── Cards (compact; click opens an action / steer panel) ───────────────────

function card(title: string, meta: (Node | string)[], onClick: () => void): HTMLElement {
  return el("div", { class: "needs-card", onclick: onClick }, [
    el("div", { class: "needs-card-title" }, [title]),
    meta.length ? el("div", { class: "needs-card-meta" }, meta) : el("span", {}),
  ]);
}

// onHeartbeat records a liveness pulse and repaints the matching running card in
// place (no board refetch). Exported for the WS event loop in main.ts.
export function onHeartbeat(hb: Heartbeat): void {
  liveness.set(hb.taskId, hb);
  const node = document.querySelector<HTMLElement>(`[data-pulse="${cssEscape(hb.taskId)}"]`);
  if (node) paintPulse(node, hb);
}

// pulseEl builds the live activity line for an in-flight task card, seeded from
// the last known pulse so it isn't blank until the next heartbeat arrives.
// Exported so the Tasks list reuses the exact same liveness rendering (and the
// shared heartbeat repaint, which targets [data-pulse] nodes regardless of view).
export function pulseEl(t: Task): HTMLElement {
  const node = el("div", { class: "pulse", "data-pulse": t.id });
  paintPulse(node, liveness.get(t.id));
  return node;
}

// paintPulse renders a pulse node from a heartbeat: green "working" while output
// flows, amber "quiet" once the agent has been silent past QUIET_AFTER. With no
// pulse yet (run just started, or pre-heartbeat), it reads "starting…".
function paintPulse(node: HTMLElement, hb?: Heartbeat): void {
  if (!hb) {
    node.className = "pulse";
    node.textContent = "● starting…";
    node.title = "";
    return;
  }
  const quiet = hb.idleSeconds >= QUIET_AFTER;
  node.className = "pulse" + (quiet ? " quiet" : "");
  const ago = hb.idleSeconds < 4 ? "just now" : `${fmtDur(hb.idleSeconds)} ago`;
  node.textContent = quiet
    ? `● quiet · last output ${ago}`
    : `● working · ${fmtDur(hb.runningSeconds)} elapsed`;
  // The most recent output line as a hover, so a curious operator can confirm
  // what the agent is actually doing without opening the task.
  node.title = hb.lastLine ? `${hb.lastLine}\n(last output ${ago})` : `last output ${ago}`;
}

// fmtDur renders a whole-second duration compactly: 45s, 3m, 1h2m.
function fmtDur(sec: number): string {
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  return `${h}h${m % 60}m`;
}

// cssEscape quotes a task id for a CSS attribute selector. Ids are generated and
// safe, but escape defensively rather than trust them in a selector.
function cssEscape(s: string): string {
  const cssAny = CSS as unknown as { escape?: (v: string) => string };
  return cssAny.escape ? cssAny.escape(s) : s.replace(/["\\]/g, "\\$&");
}

function planCard(p: Plan, agents: Agent[]): CardItem {
  const meta: (Node | string)[] = [];
  if (p.bigTask?.plannerAgentId) meta.push(agentPhoto(agents, p.bigTask.plannerAgentId));
  meta.push(tag(`${p.tasks.length} tasks`));
  const openQ = p.openDecisions.filter((d) => d.status === "open").length;
  if (openQ) meta.push(tag(`${openQ} open Q`, "dep"));
  const title = p.bigTask?.title ?? "Plan";
  return { el: card(title, meta, () => openPlanDetail(p)), filterable: { title } };
}

// bigTaskCard surfaces a submitted big task while it's being planned (or after
// planning errored), so a Define submission is visible immediately instead of
// silently churning in the background. The status pill carries the live state;
// errored cards read red and open to the failure reason. When a planner agent is
// assigned, its photo appears on the card as well.
function bigTaskCard(b: BigTask, agents: Agent[]): CardItem {
  const meta: (Node | string)[] = [];
  const label = b.status === "planning" ? "planning…" : b.status;
  meta.push(pill(label, `status-${b.status}`));
  if (b.status === "planning" && b.plannerAgentId) {
    meta.push(agentPhoto(agents, b.plannerAgentId));
  }
  const cardEl = card(b.title, meta, () => openBigTaskDetail(b, agents));
  cardEl.dataset.bigtaskId = b.id;
  return { el: cardEl, filterable: { title: b.title } };
}

export function openBigTaskDetail(b: BigTask, agents: Agent[]): void {
  const children: (Node | string)[] = [
    b.intent ? el("p", { class: "card-spec" }, [b.intent]) : el("span", {}),
  ];
  if (b.attachments?.length) children.push(attachmentGallery(b.attachments));
  if (b.status === "planning") {
    const who = b.plannerAgentId ? ` ${agentName(agents, b.plannerAgentId)} is` : "A planner agent is";
    children.push(el("p", { class: "muted sm" }, [`${who} decomposing this into a plan — it'll land in Approve when ready.`]));
  } else if (b.status === "draft") {
    children.push(el("p", { class: "muted sm" }, ["Queued for planning."]));
  } else if (b.status === "error") {
    children.push(el("p", { class: "form-error bigtask-error" }, [b.error || "Planning failed."]));
  }
  if (b.status === "draft" || b.status === "error") {
    children.push(actionRow([
      b.status === "error"
        ? button("Retry planning", { variant: "primary", onclick: () => act(() => api.replanBigTask(b.id)) })
        : el("span", {}),
      button("Delete request", { variant: "danger", onclick: () => {
        if (!confirm(`Delete "${b.title}"? This removes the plan request and its proposed tasks.`)) return;
        act(() => api.deleteBigTask(b.id));
      }}),
    ]));
  }
  if (b.status === "backlog") {
    children.push(actionRow([
      button("Move to planning", { variant: "primary", onclick: () => act(() => api.promoteBigTask(b.id)) }),
      button("Delete", { variant: "danger", onclick: () => {
        if (!confirm(`Delete "${b.title}"?`)) return;
        act(() => api.deleteBigTask(b.id));
      }}),
    ]));
  }
  if (b.status === "planning") {
    children.push(actionRow([
      button("Stop planning", { variant: "danger", onclick: () => {
        if (!confirm(`Stop planning "${b.title}"?`)) return;
        act(() => api.stopPlanning(b.id));
      }}),
    ]));
  }
  children.push(bigTaskCommentsSection(b.id));
  const side = buildSidebar([
    asideField("Status", [pill(b.status, `status-${b.status}`)]),
    b.plannerAgentId ? asideField("Planner", [tag(agentName(agents, b.plannerAgentId), "agent")]) : null,
    (b.constraints?.length)
      ? asideField("Constraints", b.constraints.map((c) => el("code", { class: "verify-cmd" }, [c])))
      : null,
  ]);
  openModal(b.title, el("div", { class: "detail" }, children), { wide: true, sidebar: side });
  loadBigTaskComments(b.id);
}

function decideCard(d: Decision): CardItem {
  return { el: card(d.question, [tag(d.taskId ? "task" : "plan")], () => openDecideDetail(d)), filterable: { title: d.question } };
}

function reviewCard(it: ReviewItem, agents: Agent[]): CardItem {
  const t = it.task;
  return {
    el: card(
      t.title,
      [tag(t.status, `status-${t.status}`), tag(t.riskTier, `risk-${t.riskTier}`)],
      () => openReviewDetail(it, agents),
    ),
    filterable: { title: t.title, riskTier: t.riskTier },
  };
}

function auditCard(it: ReviewItem): CardItem {
  const t = it.task;
  return {
    el: card(
      t.title,
      [tag("auto-merged"), tag(t.riskTier, `risk-${t.riskTier}`)],
      () => openAuditDetail(it),
    ),
    filterable: { title: t.title, riskTier: t.riskTier },
  };
}

// Cards stay quiet: avatar + risk, plus priority only when it deviates from
// the medium default. Reporter, topic tags, deps etc. live in the detail
// sidebar — repeating them here turns every card into equal-weight noise.
function taskCard(t: Task, agents: Agent[]): CardItem {
  const meta: (Node | string)[] = [];
  if (t.agentId) meta.push(agentPhoto(agents, t.agentId));
  meta.push(tag(t.riskTier, `risk-${t.riskTier}`));
  if (t.priority && t.priority !== "medium") meta.push(tag(t.priority, `priority-${t.priority}`));
  const badge = ciBadge(t);
  if (badge) meta.push(badge);
  const pl = pushStatusLabel(t);
  if (pl) meta.push(tag(pl, `push-${pl}`));
  const node = card(t.title, meta, () => openTaskDetail(t, agents));
  // In-flight cards carry a live pulse so a walk-away operator can see the agent
  // is actually working (and spot one that's gone quiet) without opening it.
  if (IN_FLIGHT.includes(t.status)) node.append(pulseEl(t));
  return {
    el: node,
    filterable: { title: t.title, riskTier: t.riskTier, agentId: t.agentId, pushStatus: pushStatusLabel(t) ?? undefined },
  };
}

// ── Action / detail panels ─────────────────────────────────────────────────

export function openPlanDetail(p: Plan): void {
  const titleOf = (id: string) => p.tasks.find((t) => t.id === id)?.title ?? id.slice(0, 6);
  const openQ = p.openDecisions.filter((d) => d.status === "open").length;
  const body = el("div", { class: "detail" }, [
    p.bigTask?.intent ? el("p", { class: "card-spec" }, [p.bigTask.intent]) : el("span", {}),
    el("div", { class: "plan-tasks" }, p.tasks.map((t) => planTaskRow(t, titleOf))),
    p.openDecisions.length
      ? el("div", { class: "plan-decisions" }, [
          el("div", { class: "section-h sm" }, ["Questions"]),
          ...p.openDecisions.map((d) =>
            el("div", { class: "plan-decision" }, [
              el("span", { class: "q" }, [d.question]),
              d.status === "answered"
                ? tag(`→ ${d.answer}`)
                : el("span", { class: "muted hint" }, [" — answer it in Decide"]),
            ]),
          ),
        ])
      : el("span", {}),
    actionRow([
      button("Approve plan", { variant: "primary", onclick: () => act(() => api.approvePlan(p.id)) }),
      button("Request changes", { variant: "danger", onclick: () => {
        const textarea = el("textarea", {
          class: "prompt-input comment-input",
          rows: "4",
          placeholder: "What should the planner change?",
        }) as HTMLTextAreaElement;
        const err = el("div", { class: "form-error" }, []);
        const attach = imageAttach(textarea, err);
        const submit = () => {
          const feedback = textarea.value.trim();
          if (!feedback) return;
          const attachments = attach.urls();
          closeModal();
          act(() => api.revisePlan(p.id, feedback, attachments));
        };
        textarea.addEventListener("keydown", (e: KeyboardEvent) => {
          if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); submit(); }
        });
        const body = el("div", {}, [
          textarea,
          attach.previews,
          err,
          el("div", { class: "form-actions" }, [
            ...attach.controls,
            button("Cancel", { onclick: closeModal }),
            button("Send", { variant: "primary", onclick: submit }),
          ]),
        ]);
        openModal("Request changes", body);
        textarea.focus();
      }}),
      button("Reject", { variant: "danger", onclick: () => act(() => api.rejectPlan(p.id)) }),
    ]),
  ]);
  const side = buildSidebar([
    asideField("Status", [tag(p.status, `status-${p.status}`)]),
    asideField("Tasks", [tag(`${p.tasks.length} tasks`)]),
    openQ ? asideField("Open questions", [tag(`${openQ} open`, "dep")]) : null,
  ]);
  openModal(p.bigTask?.title ?? "Plan", body, { wide: true, sidebar: side });
}

function planTaskRow(t: Task, titleOf: (id: string) => string): HTMLElement {
  const meta: (Node | string)[] = [tag(t.riskTier, `risk-${t.riskTier}`)];
  for (const lbl of t.tags ?? []) meta.push(tag(lbl));
  for (const dep of t.dependsOn ?? []) meta.push(tag(`after: ${titleOf(dep)}`, "dep"));
  for (const c of t.acceptance?.verifyCmds ?? []) meta.push(el("code", { class: "verify-cmd" }, [c]));
  return el("div", { class: "plan-task" }, [
    el("div", { class: "plan-task-title" }, [t.title]),
    t.spec ? el("p", { class: "card-spec sm" }, [t.spec]) : el("span", {}),
    el("div", { class: "card-meta" }, meta),
  ]);
}

export function openDecideDetail(d: Decision): void {
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
    el("div", { class: "decision-answer" }, [free, button("Answer", { variant: "primary", onclick: () => answer(free.value) })]),
    el("label", { class: "checkbox" }, [promote, "Save as a convention (steer future runs)"]),
  ]);
  const side = buildSidebar([
    asideField("Status", [tag(d.status, `status-${d.status}`)]),
    asideField("Type", [tag(d.taskId ? "task" : "plan")]),
  ]);
  openModal(d.question, body, { sidebar: side });
}

export function openReviewDetail(it: ReviewItem, agents: Agent[] = []): void {
  const { task, attempt } = it;
  const green = task.status === "review";
  const diff = attempt?.evidence?.diff?.trim();
  const blockedReason = task.status === "blocked" && attempt ? firstLine(attempt.log) : "";
  const review = attempt?.evidence?.stages?.review;
  const reviewNote = review && !review.pass ? `Reviewer: ${review.output}` : "";
  const hasAdvisoryFailure = attempt?.evidence?.stages
    ? Object.values(attempt.evidence.stages).some(s => !s.pass && !s.skipped)
    : false;

  const body = el("div", { class: "detail" }, [
    blockedReason ? el("p", { class: "blocked-q" }, [blockedReason]) : el("span", {}),
    reviewNote ? el("p", { class: "blocked-q" }, [reviewNote]) : el("span", {}),
    attempt ? stageRow(attempt.evidence) : el("span", { class: "muted" }, ["no evidence"]),
    attempt ? usageLine(attempt.usage) : el("span", {}),
    !green && attempt ? failureBlock(attempt) : el("span", {}),
    evidenceArtifacts(attempt?.evidence),
    diff ? diffBlock(diff) : el("p", { class: "muted" }, ["(no diff produced)"]),
    el("div", { class: "action-bar" }, [
      gateSummaryEl(attempt?.evidence),
      actionRow([
        green && !hasAdvisoryFailure
          ? button("Merge", { variant: "primary", onclick: () => act(() => api.acceptTask(task.id), () => undoToastSpec("accept", task)) })
          : green && hasAdvisoryFailure
            ? button("Merge anyway", { onclick: () => {
                if (!confirm(`Gates failed on "${task.title}". Merge its work into the base branch anyway?`)) return;
                act(() => api.acceptTask(task.id, true), () => undoToastSpec("accept", task));
              }})
            : button("Retry", { variant: "primary", onclick: () => act(() => api.retryTask(task.id)) }),
        !green && task.branch
          ? button("Merge anyway", { variant: "danger", onclick: () => {
              if (!confirm(`Gates failed on "${task.title}". Merge its work into the base branch anyway?`)) return;
              act(() => api.acceptTask(task.id, true), () => undoToastSpec("accept", task));
            }})
          : el("span", {}),
        green
          ? button("Request changes", { onclick: () => {
              const guidance = requireCommentInput("Describe the changes you want before sending the task back.");
              if (guidance === null) return;
              act(() => api.requestChangesTask(task.id, guidance));
            }})
          : el("span", {}),
        button("Kick back", { variant: "danger", onclick: () => {
          promptModal({
            title: "Kick back",
            placeholder: "A reason is required to kick a task back.",
            submitLabel: "Kick back",
          }, (reason) => act(() => api.rejectTask(task.id, reason), () => undoToastSpec("reject", task)));
        }}),
      ]),
    ]),
    historySection(task.id),
    commentsSection(task.id),
  ]);
  const side = taskDetailSidebar(task, agents);
  openTaskId = task.id;
  openModal(task.title, body, { wide: true, sidebar: side });
  loadHistory(task.id);
  loadComments(task.id);
}

export function openAuditDetail(it: ReviewItem): void {
  const { task, attempt } = it;
  const diff = attempt?.evidence?.diff?.trim();
  const body = el("div", { class: "detail" }, [
    attempt ? stageRow(attempt.evidence) : el("span", { class: "muted" }, ["no evidence"]),
    attempt ? usageLine(attempt.usage) : el("span", {}),
    evidenceArtifacts(attempt?.evidence),
    diff ? diffBlock(diff) : el("p", { class: "muted" }, ["(no diff produced)"]),
    actionRow([
      button("Looks good", { variant: "primary", onclick: () => act(() => api.ackAudit(task.id)) }),
      button("Revert", { variant: "danger", onclick: () => {
        if (!confirm(`Mark "${task.title}" as a change-failure? Revert it in git separately.`)) return;
        act(() => api.revertTask(task.id));
      }}),
    ]),
    historySection(task.id),
    commentsSection(task.id),
  ]);
  const side = buildSidebar([
    asideField("Status", [tag("auto-merged")]),
    asideField("Risk", [tag(task.riskTier, `risk-${task.riskTier}`)]),
    task.branch ? asideField("Branch", [el("code", { class: "branch" }, [task.branch])]) : null,
  ]);
  openTaskId = task.id;
  openModal(task.title, body, { wide: true, sidebar: side });
  loadHistory(task.id);
  loadComments(task.id);
}

export function taskDetailSidebar(t: Task, agents: Agent[], onOpenDep?: (depId: string) => void): HTMLElement {
  const releaseField = (): HTMLElement | null => {
    if (t.status !== "merged" && !t.releaseId) return null;
    if (t.releaseId) {
      return asideField("Release", [el("span", { class: "release-field" }, [`#${t.releaseId.slice(0, 6)}`])]);
    }
    // merged but no releaseId = unshipped
    return asideField("Release", [el("span", { class: "release-field release-unshipped" }, ["unshipped"])]);
  };
  return buildSidebar([
    asideField("Task ID", [el("code", { class: "branch" }, [t.id])]),
    asideField("Status", [tag(t.status, `status-${t.status}`)]),
    asideField("Risk", [tag(t.riskTier, `risk-${t.riskTier}`)]),
    t.priority ? asideField("Priority", [tag(t.priority, `priority-${t.priority}`)]) : null,
    t.reporter ? asideField("Reporter", [tag(t.reporter, `reporter-${t.reporter}`)]) : null,
    asideField("Assignee", [
      t.agentId
        ? tag(agentName(agents, t.agentId), "agent")
        : el("span", { class: "muted" }, ["unassigned"]),
    ]),
    t.branch ? asideField("Branch", [el("code", { class: "branch" }, [t.branch])]) : null,
    releaseField(),
    asideField("Tags", (t.tags ?? []).length
      ? (t.tags ?? []).map((lbl) => tag(lbl))
      : [el("span", { class: "muted" }, ["none"])]),
    (t.dependsOn ?? []).length
      ? asideField("Depends on", (t.dependsOn ?? []).map((dep) =>
          el("span", { class: "tag dep clickable", style: "cursor:pointer", role: "button", onclick: onOpenDep ? () => onOpenDep(dep) : undefined }, [dep.slice(0, 6)])))
      : null,
    (t.touchPaths ?? []).length
      ? asideField("Touches", (t.touchPaths ?? []).map((p) => el("code", { class: "verify-cmd" }, [p])))
      : null,
  ]);
}

// openTaskDetail covers the non-gate lifecycle cards (ready/running/verifying/
// merged/closed): spec + meta, live steering for in-flight work, retry/delete
// for kicked-back work, and lazily-loaded gate evidence (stages + diff) from
// the latest attempt.
export function openTaskDetail(t: Task, agents: Agent[]): void {
  const onOpenDep = (depId: string) => {
    api.getTask(depId).then((result) => {
      openTaskDetail(result.task, agents);
    }).catch((e) => {
      alert((e as Error).message);
    });
  };
  const side = taskDetailSidebar(t, agents, onOpenDep);

  const children: (Node | string)[] = [];
  if (t.spec) children.push(el("p", { class: "card-spec" }, [t.spec]));
  else children.push(el("span", {}));
  if (t.attachments?.length) children.push(attachmentGallery(t.attachments));
  for (const c of t.acceptance?.verifyCmds ?? []) children.push(el("code", { class: "verify-cmd" }, [c]));
  if (STEERABLE.includes(t.status)) children.push(steerRow(t, agents));
  if (t.status === "closed") {
    children.push(actionRow([
      button("Retry", { variant: "primary", onclick: () => act(() => api.retryTask(t.id)) }),
      button("Delete", { variant: "danger", onclick: () => {
        if (!confirm(`Permanently delete "${t.title}"? Its attempt history and comments are removed too.`)) return;
        act(() => api.deleteTask(t.id));
      }}),
    ]));
  }
  children.push(el("div", { id: `task-evidence-${t.id}`, class: "task-evidence" }, []));
  children.push(historySection(t.id));
  children.push(commentsSection(t.id));

  openTaskId = t.id;
  openModal(t.title, el("div", { class: "detail" }, children), { wide: true, sidebar: side });
  loadEvidence(t.id);
  loadHistory(t.id);
  loadComments(t.id);
}

// The task whose detail modal is currently open (if any). Comment websocket
// events that target this task reload the in-modal list live; cleared so the
// list paints under the agent labels we already have.
let openTaskId: string | null = null;



// evidenceArtifacts renders agent-attached proof files (screenshots, recordings,
// logs) from the attempt's evidence, or nothing when the run produced none.
function evidenceArtifacts(ev: Evidence | undefined): HTMLElement {
  if (!ev?.artifacts?.length) return el("span", {});
  return el("div", { class: "evidence-artifacts" }, [
    el("div", { class: "section-h sm" }, ["Evidence"]),
    attachmentGallery(ev.artifacts),
  ]);
}

// requireCommentInput reads the open modal's comment composer for actions that
// piggyback on it (Kick back, Request changes). When empty it surfaces `msg`
// inline next to the composer and returns null so the caller can abort.
function requireCommentInput(msg: string): string | null {
  const input = document.querySelector<HTMLTextAreaElement>(".comment-input");
  const text = input?.value.trim() ?? "";
  if (!text) {
    input?.focus();
    const compose = input?.closest(".comment-compose");
    const errEl = compose?.querySelector<HTMLElement>(".form-error");
    if (errEl) errEl.textContent = msg;
    return null;
  }
  return text;
}

// commentsSection builds the comments block of the task detail: a heading, the
// (lazily-loaded) list, and a composer that posts a note then reloads the list.
// Images attach via the file picker or by pasting into the textarea; they upload
// immediately and submit as attachment URLs alongside the text. Typing @name
// opens an agent mention dropdown; selecting an agent directs the comment to it.
function commentsSection(id: string): HTMLElement {
  const input = el("textarea", { class: "comment-input", placeholder: "Add a comment… (paste or attach images)", rows: "2" }) as HTMLTextAreaElement;
  const err = el("div", { class: "form-error" }, []);
  const attach = imageAttach(input, err);

  let directedAgentId: string | null = null;
  let dropdownAgents: Agent[] = [];
  let selectedIdx = -1;

  const dropdown = el("div", { class: "mention-dropdown" }, []);
  dropdown.style.display = "none";

  const hideDropdown = () => {
    dropdown.style.display = "none";
    dropdownAgents = [];
    selectedIdx = -1;
  };

  const setActive = (idx: number) => {
    selectedIdx = idx;
    const items = dropdown.querySelectorAll<HTMLElement>(".mention-item");
    items.forEach((item, i) => item.classList.toggle("active", i === idx));
  };

  const selectAgent = (a: Agent) => {
    const caret = input.selectionStart ?? input.value.length;
    const result = applyMention(input.value, caret, a.name);
    input.value = result.text;
    input.setSelectionRange(result.caret, result.caret);
    directedAgentId = a.id;
    hideDropdown();
    input.focus();
  };

  const showDropdown = (query: string) => {
    const matches = matchAgents(storedAgents, query);
    dropdownAgents = matches;
    if (matches.length === 0) {
      hideDropdown();
      return;
    }
    clear(dropdown);
    matches.forEach((a, i) => {
      dropdown.append(el("div", {
        class: "mention-item" + (i === 0 ? " active" : ""),
        onclick: () => selectAgent(a),
        onmouseenter: () => setActive(i),
      }, [a.name]));
    });
    dropdown.style.display = "";
    selectedIdx = 0;
  };

  input.addEventListener("input", () => {
    const caret = input.selectionStart ?? input.value.length;
    const q = mentionQuery(input.value, caret);
    if (q === null) {
      hideDropdown();
      return;
    }
    showDropdown(q);
  });

  input.addEventListener("keydown", (e: KeyboardEvent) => {
    if (dropdown.style.display === "none") return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive(Math.min(selectedIdx + 1, dropdownAgents.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive(Math.max(selectedIdx - 1, 0));
    } else if (e.key === "Enter") {
      if (selectedIdx >= 0 && dropdownAgents[selectedIdx]) {
        e.preventDefault();
        selectAgent(dropdownAgents[selectedIdx]);
      }
    } else if (e.key === "Escape") {
      hideDropdown();
    }
  });

  const submit = async () => {
    const text = input.value.trim();
    const attachments = attach.urls();
    if (!text && attachments.length === 0) return;
    err.textContent = "";
    try {
      await api.addComment(id, text, attachments, directedAgentId ?? undefined);
      input.value = "";
      attach.reset();
      directedAgentId = null;
      hideDropdown();
      loadComments(id);
    } catch (e) {
      err.textContent = (e as Error).message;
    }
  };

  return el("div", { class: "comments" }, [
    el("div", { class: "section-h sm" }, ["Comments"]),
    el("div", { id: `task-comments-${id}`, class: "comment-list" }, []),
    el("div", { class: "comment-compose" }, [
      el("div", { class: "mention-wrapper" }, [input, dropdown]),
      attach.previews,
      err,
      el("div", { class: "form-actions" }, [...attach.controls, button("Comment", { variant: "primary", onclick: submit })]),
    ]),
  ]);
}

async function loadComments(id: string): Promise<void> {
  try {
    const comments = await api.listComments(id);
    const slot = document.getElementById(`task-comments-${id}`);
    if (!slot) return;
    clear(slot);
    const regularComments = comments.filter((c) => c.authorType !== "system");
    if (regularComments.length === 0) {
      slot.append(el("div", { class: "comment-empty muted sm" }, ["No comments yet."]));
      return;
    }
    for (const c of regularComments) slot.append(commentItem(c));
  } catch {
    /* comments are best-effort */
  }
}

function historySection(id: string): HTMLElement {
  return el("div", { class: "history" }, [
    el("div", { class: "section-h sm" }, ["History"]),
    el("div", { id: `task-history-${id}`, class: "history-slot" }, []),
  ]);
}

async function loadHistory(id: string): Promise<void> {
  try {
    const transitions = await api.listTaskHistory(id);
    const slot = document.getElementById(`task-history-${id}`);
    if (!slot) return;
    slot.replaceChildren(renderTransitionTimeline(transitions));
  } catch {
    /* history is best-effort */
  }
}

// commentItem renders one note oldest-first: a "You" label for human comments,
// "System" for system status-change comments, the authoring agent's id for
// agent comments, above the body text and any image attachments (thumbnails
// linking to the full-size upload).
export function commentItem(c: Comment): HTMLElement {
  let who: string;
  const cls = "comment";
  if (c.authorType === "user") {
    who = "You";
  } else {
    who = c.authorId || c.authorType;
  }
  const children: (Node | string)[] = [el("div", { class: "comment-author" }, [who])];
  if (c.createdAt) {
    const d = new Date(c.createdAt);
    if (!isNaN(d.getTime())) {
      children.push(el("div", { class: "comment-time" }, [d.toLocaleString()]));
    }
  }
  if (c.body) children.push(el("div", { class: "comment-body" }, [c.body]));
  if (c.attachments?.length) children.push(attachmentGallery(c.attachments));
  return el("div", { class: cls }, children);
}

function bigTaskCommentsSection(id: string): HTMLElement {
  const input = el("textarea", { class: "comment-input", placeholder: "Add a comment… (paste or attach images)", rows: "2" }) as HTMLTextAreaElement;
  const err = el("div", { class: "form-error" }, []);
  const attach = imageAttach(input, err);

  const submit = async () => {
    const text = input.value.trim();
    const attachments = attach.urls();
    if (!text && attachments.length === 0) return;
    err.textContent = "";
    try {
      await api.addBigTaskComment(id, text, attachments);
      input.value = "";
      attach.reset();
      loadBigTaskComments(id);
    } catch (e) {
      err.textContent = (e as Error).message;
    }
  };

  return el("div", { class: "comments" }, [
    el("div", { class: "section-h sm" }, ["Comments"]),
    el("div", { id: `bigtask-comments-${id}`, class: "comment-list" }, []),
    el("div", { class: "comment-compose" }, [
      input,
      attach.previews,
      err,
      el("div", { class: "form-actions" }, [...attach.controls, button("Comment", { variant: "primary", onclick: submit })]),
    ]),
  ]);
}

async function loadBigTaskComments(id: string): Promise<void> {
  try {
    const comments = await api.listBigTaskComments(id);
    const slot = document.getElementById(`bigtask-comments-${id}`);
    if (!slot) return;
    clear(slot);
    if (comments.length === 0) {
      slot.append(el("div", { class: "comment-empty muted sm" }, ["No comments yet."]));
      return;
    }
    const systemComments = comments.filter((c) => c.authorType === "system");
    const regularComments = comments.filter((c) => c.authorType !== "system");
    if (systemComments.length > 0) {
      const timeline = systemComments.map((c) => c.body).join(" → ");
      slot.append(el("div", { class: "transition-timeline" }, [timeline]));
    }
    for (const c of regularComments) slot.append(commentItem(c));
  } catch {
    /* comments are best-effort */
  }
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
    slot.append(usageLine(a.usage));
    if (a.result !== "pass") slot.append(failureBlock(a));
    if (a.evidence?.artifacts?.length) slot.append(evidenceArtifacts(a.evidence));
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
    button("Cancel task", { variant: "danger", onclick: () => {
      if (!confirm(`Cancel "${t.title}"?`)) return;
      act(() => api.cancelTask(t.id));
    }}),
  ]);
}

// ── Define / Create-task forms ─────────────────────────────────────────────

function openDefine(): void {
  const title = el("input", { placeholder: "Outcome (e.g. Users can log in with email)" }) as HTMLInputElement;
  const intent = el("textarea", { placeholder: "The why + desired outcome. What does done look like? Paste images for context.", rows: "5" }) as HTMLTextAreaElement;
  const constraints = el("textarea", { placeholder: "Constraints, one per line (e.g. PCI-compliant, works on mobile)", rows: "3" }) as HTMLTextAreaElement;
  const err = el("div", { class: "form-error" });
  const attach = imageAttach(intent, err);

  const form = el("form", {
    class: "modal-form",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      err.textContent = "";
      try {
        await api.createBigTask({
          title: title.value.trim(),
          intent: intent.value.trim(),
          constraints: lines(constraints.value),
          attachments: attach.urls(),
        });
        closeModal();
        refresh();
      } catch (e2) {
        err.textContent = (e2 as Error).message;
      }
    },
  }, [
    field("Outcome", title),
    field("Intent", intent),
    field("Images", el("div", { class: "attach-field" }, [attach.previews, ...attach.controls])),
    field("Constraints", constraints),
    err,
    el("div", { class: "form-actions" }, [
      button("Define big task", { variant: "primary", type: "submit" }),
      button("Save to backlog", { type: "button", onclick: async () => {
        if (!title.value.trim() || !intent.value.trim()) {
          err.textContent = "Title and intent are required.";
          return;
        }
        err.textContent = "";
        try {
          await api.createBigTask({
            title: title.value.trim(),
            intent: intent.value.trim(),
            constraints: lines(constraints.value),
            attachments: attach.urls(),
            status: "backlog",
          });
          closeModal();
          refresh();
        } catch (e2) {
          err.textContent = (e2 as Error).message;
        }
      }}),
    ]),
  ]);
  openModal("Define a big task", form, {
    subtitle: "Describe an outcome in plain intent — a planner agent decomposes it into tasks for your approval.",
  });
}

function openCreateTask(): void {
  const title = el("input", { placeholder: "Title (e.g. Add /healthz endpoint)" }) as HTMLInputElement;
  const spec = el("textarea", { placeholder: "What to build, where, and any constraints… Paste images for context.", rows: "4" }) as HTMLTextAreaElement;
  const verify = el("textarea", { placeholder: "Verify commands, one per line (e.g. go test ./...)", rows: "3" }) as HTMLTextAreaElement;
  const tags = el("input", { placeholder: "Tags, comma-separated (go, api)" }) as HTMLInputElement;
  const priority = el("select", {}, [
    el("option", { value: "low" }, ["Low"]),
    el("option", { value: "medium" }, ["Medium"]),
    el("option", { value: "high" }, ["High"]),
  ]) as HTMLSelectElement;
  priority.value = "medium";
  const err = el("div", { class: "form-error" });
  const attach = imageAttach(spec, err);

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
          attachments: attach.urls(),
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
    field("Images", el("div", { class: "attach-field" }, [attach.previews, ...attach.controls])),
    field("Verify commands", verify),
    field("Tags", tags),
    field("Priority", priority),
    el("p", { class: "muted sm" }, ["Verify commands are the machine-checkable acceptance contract."]),
    err,
    el("div", { class: "form-actions" }, [button("Create task", { variant: "primary", type: "submit" })]),
  ]);
  openModal("Create a task", form, {
    subtitle: "One well-scoped change, routed straight to an implementer agent — no planner involved.",
  });
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

function asideField(label: string, value: (Node | string)[]): HTMLElement {
  return el("div", { class: "modal-aside-field" }, [
    el("div", { class: "aside-label" }, [label]),
    el("div", { class: "aside-value" }, value),
  ]);
}

function buildSidebar(fields: (HTMLElement | null)[]): HTMLElement {
  return el("div", {}, fields.filter(Boolean) as HTMLElement[]);
}

function actionRow(buttons: HTMLElement[]): HTMLElement {
  return el("div", { class: "card-actions" }, buttons);
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
    const isFail = !r.skipped && !r.pass;
    const cls = r.skipped ? "stage skip" : r.pass ? "stage pass" : "stage fail" + (isFail ? " clickable" : "");
    const mark = r.skipped ? "–" : r.pass ? "✓" : "✗";
    const attrs: Record<string, string | number | boolean | EventListener | undefined> = {
      class: cls,
      title: isFail ? "" : (r.output ? r.output.slice(-4000) : ""),
    };
    if (isFail) {
      attrs.role = "button";
      attrs.tabindex = 0;
      attrs.onclick = (e: Event) => {
        const chip = e.currentTarget as HTMLElement;
        const next = chip.nextElementSibling as HTMLElement | null;
        if (next && next.classList.contains("stage-evidence")) {
          next.style.display = next.style.display === "none" ? "" : "none";
        } else {
          const block = el("div", { class: "stage-evidence" }, [
            el("pre", {}, [r.output ?? "(no output)"]),
          ]);
          chip.insertAdjacentElement("afterend", block);
        }
      };
    }
    return el("span", attrs, [`${mark} ${s}`]);
  });
  if (chips.length === 0) return el("div", { class: "stage-row" }, [el("span", { class: "muted" }, ["no gate stages"])]);
  return el("div", { class: "stage-row" }, chips);
}

// usageLine surfaces an attempt's self-reported token usage as a small muted
// line, or nothing when usage is absent or zero (older payloads / runs that
// captured none).
function usageLine(usage: Usage | undefined): HTMLElement {
  if (!usage || !usage.totalTokens) return el("span", {});
  return el("div", { class: "muted sm token-usage" }, [
    `tokens: ${formatTokens(usage.totalTokens)} (in ${formatTokensShort(usage.inputTokens)} / out ${formatTokensShort(usage.outputTokens)})`,
  ]);
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

function gateSummaryEl(ev: Evidence | undefined): HTMLElement {
  const stages = ev?.stages ?? {};
  const entries = Object.entries(stages);
  const failedNames = entries
    .filter(([, r]) => !r.pass && !r.skipped)
    .map(([name]) => `✗ ${name}`);
  const passedCount = entries.filter(([, r]) => r.pass && !r.skipped).length;
  if (failedNames.length === 0 && passedCount === 0) {
    return el("div", { class: "gate-summary muted" }, ["no gate stages"]);
  }
  const parts = [...failedNames, `${passedCount} passed`];
  return el("div", { class: "gate-summary" }, [parts.join(" · ")]);
}

function diffBlock(diff: string): HTMLElement {
  return renderDiff(diff);
}

// updateReleaseUI refreshes the release strip with the latest release state.
// The strip is hidden when deploy is not wired up (no releases in the store).
async function updateReleaseUI(): Promise<void> {
  const strip = document.getElementById("release-strip");
  if (!strip) return;
  try {
    const releases = await api.listReleases();
    const latest = releases.length ? releases[releases.length - 1] : null;

    if (!latest) {
      strip.style.display = "none";
      return;
    }
    strip.style.display = "";
    clear(strip);
    const statusClass = releaseStatusClass(latest.status);
    const label = releaseStripLabel(latest);
    strip.append(
      el("span", { class: `release-strip-id ${statusClass}` }, [`#${latest.id.slice(0, 6)}`]),
      el("span", { class: `release-strip-status ${statusClass}` }, [label]),
      el("button", { class: "release-strip-detail", onclick: () => openReleaseDetail(latest) }, ["details"]),
    );
  } catch {
    /* release UI is best-effort — never break the board */
  }
}

function releaseStatusClass(status: string): string {
  switch (status) {
    case "live": return "release-live";
    case "baking": return "release-baking";
    case "deploying": return "release-deploying";
    case "failed": return "release-failed";
    case "rolled_back": return "release-rolled-back";
    default: return "release-pending";
  }
}

function releaseStripLabel(rel: Release): string {
  switch (rel.status) {
    case "deploying": return "deploying…";
    case "baking": return "baking";
    case "live": return "live";
    case "failed": return `failed: ${rel.error || "unknown error"}`;
    case "rolled_back": return "rolled back";
    default: return rel.status;
  }
}

function openReleaseDetail(rel: Release): void {
  const canRollback = rel.status === "baking" || rel.status === "live";
  const children: (Node | string)[] = [
    asideField("SHA", [el("code", { class: "branch" }, [rel.sha.slice(0, 12)])]),
    rel.prevSha ? asideField("Previous SHA", [el("code", { class: "branch" }, [rel.prevSha.slice(0, 12)])]) : el("span", {}),
    asideField("Status", [tag(rel.status, `release-tag-${rel.status}`)]),
    rel.deployedAt ? asideField("Deployed at", [el("span", {}, [rel.deployedAt])]) : el("span", {}),
    rel.liveAt ? asideField("Live at", [el("span", {}, [rel.liveAt])]) : el("span", {}),
    rel.error ? el("p", { class: "form-error" }, [rel.error]) : el("span", {}),
    rel.deployLog ? el("details", { class: "log-wrap" }, [
      el("summary", {}, ["Deploy log"]),
      el("pre", { class: "fail-out" }, [rel.deployLog]),
    ]) : el("span", {}),
    rel.healthLog ? el("details", { class: "log-wrap" }, [
      el("summary", {}, ["Health log"]),
      el("pre", { class: "fail-out" }, [rel.healthLog]),
    ]) : el("span", {}),
    canRollback ? actionRow([
      button("Rollback", { variant: "danger", onclick: () => {
        if (!confirm(`Rollback release #${rel.id.slice(0, 6)}? This will re-deploy the previous SHA.`)) return;
        api.rollbackRelease(rel.id).then(() => {
          closeModal();
          void updateReleaseUI();
        }).catch((e: unknown) => {
          alert((e as Error).message);
        });
      }}),
    ]) : el("span", {}),
  ];
  openModal(`Release #${rel.id.slice(0, 6)}`, el("div", { class: "detail" }, children), {});
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
// and shows "Pushing…" for the duration (network-bound), then shows a success
// modal with git's summary — or an alert on error. Afterwards the status
// re-check hides the button (a successful push means nothing is left to ship).
async function pushMain(btn: HTMLButtonElement): Promise<void> {
  const label = btn.textContent ?? "Push";
  btn.disabled = true;
  btn.textContent = "Pushing…";
  try {
    const res = await api.push();
    openModal("Push succeeded", el("p", {}, [res.detail || "Pushed."]));
  } catch (e) {
    alert((e as Error).message);
  } finally {
    btn.disabled = false;
    btn.textContent = label;
    void updatePushButton();
  }
}

async function act(fn: () => Promise<unknown>, spec?: () => ToastSpec): Promise<void> {
  try {
    await fn();
    if (spec) {
      const s = spec();
      showToast({ message: s.message, undo: { label: s.label, onUndo: s.onUndo } });
    }
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
  if (e?.type === "task.transition.added" && openTaskId) {
    const t = e.payload as Transition | null;
    if (t && t.taskId === openTaskId) loadHistory(openTaskId);
  }
  if (refreshTimer !== null) clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => {
    refreshTimer = null;
    refresh();
  }, REFRESH_DEBOUNCE_MS);
}
