// Board: the whole factory on one kanban. Columns run left to right along the
// task lifecycle — the human gates (Approve, Decide, Accept, Audit) interleaved
// with the in-flight stages (Ready, Running, Verifying, Merged). Gate columns
// are marked "needs you"; click any card to act or steer. "Define big task" /
// "Create task" seed work; metrics + autonomy controls live in the Factory
// view. (SPECS §10.)
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { button, pill, tag, field, formatTokens, formatTokensShort } from "../components.js";
import { openModal, closeModal } from "../ui.js";
import { STAGE_ORDER } from "../types.js";
import { DEFAULT_AVATAR } from "../avatar.js";
import type { Plan, Decision, ReviewItem, Task, Agent, BigTask, Evidence, Attempt, Usage, Comment, FabrikaEvent, Release } from "../types.js";
import { registerReleaseListener, registerIncidentListener } from "../ws.js";
import { renderDiff } from "./diff-view.js";
import { attachmentGallery } from "./attachment.js";

type ColId = "planning" | "approve" | "decide" | "ready" | "running" | "verifying" | "accept" | "audit" | "merged" | "closed";
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
  { id: "closed", label: "Closed" },
];
const IN_FLIGHT = ["claimed", "running"];
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
      el("div", { class: "header-actions" }, [
        // Hidden until pushStatus reports unpushed commits (see updatePushButton).
        el("button", {
          id: "push-btn",
          style: "display:none",
          onclick: (e: Event) => pushMain(e.currentTarget as HTMLButtonElement),
        }, ["Push"]),
        // Ship button: hidden when deploy is not enabled; shows unshipped count.
        el("button", {
          id: "ship-btn",
          style: "display:none",
          onclick: () => openShipConfirm(),
        }, ["Ship · 0"]),
        button("Create task", { onclick: openCreateTask }),
        button("Define big task", { variant: "primary", onclick: openDefine }),
      ]),
    ]),
    el("div", { id: "board-err", class: "form-error" }, []),
    // Incident banner: shown when any open incident exists.
    el("div", { id: "incident-banner", class: "incident-banner", style: "display:none" }, []),
    // Release strip: always visible when deploy is enabled; shows latest release.
    el("div", { id: "release-strip", class: "release-strip", style: "display:none" }, []),
    el("div", { class: "board needs-board", id: "needs-board" }, COLUMNS.map(colSkeleton)),
  );
  refresh();
  registerReleaseListener(updateReleaseUI);
  registerIncidentListener(updateIncidentBanner);
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
  void updateReleaseUI();
  void updateIncidentBanner();
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
    fillColumn("approve", plans.filter((p) => p.status === "proposed").map((p) => planCard(p, agents)));
    fillColumn("decide", decisions.map(decideCard));
    fillColumn("ready", byStatus("ready").map((t) => taskCard(t, agents)));
    fillColumn("running", tasks.filter((t) => IN_FLIGHT.includes(t.status)).map((t) => taskCard(t, agents)));
    fillColumn("verifying", byStatus("verifying").map((t) => taskCard(t, agents)));
    fillColumn("accept", reviews.map((r) => reviewCard(r, agents)));
    fillColumn("audit", audits.map(auditCard));
    fillColumn("merged", byStatus("merged").filter((t) => !auditIds.has(t.id)).map((t) => taskCard(t, agents)));
    // Kicked-back tasks land here instead of vanishing: every dead end keeps a
    // UI exit (retry or delete from the card detail).
    fillColumn("closed", byStatus("closed").map((t) => taskCard(t, agents)));
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
    body.append(el("div", { class: "board-empty" }, ["empty"]));
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

function planCard(p: Plan, agents: Agent[]): HTMLElement {
  const meta: (Node | string)[] = [];
  if (p.bigTask?.plannerAgentId) meta.push(agentPhoto(agents, p.bigTask.plannerAgentId));
  meta.push(tag(`${p.tasks.length} tasks`));
  const openQ = p.openDecisions.filter((d) => d.status === "open").length;
  if (openQ) meta.push(tag(`${openQ} open Q`, "dep"));
  return card(p.bigTask?.title ?? "Plan", meta, () => openPlanDetail(p));
}

// bigTaskCard surfaces a submitted big task while it's being planned (or after
// planning errored), so a Define submission is visible immediately instead of
// silently churning in the background. The status pill carries the live state;
// errored cards read red and open to the failure reason. When a planner agent is
// assigned, its photo appears on the card as well.
function bigTaskCard(b: BigTask, agents: Agent[]): HTMLElement {
  const meta: (Node | string)[] = [];
  const label = b.status === "planning" ? "planning…" : b.status;
  meta.push(pill(label, `status-${b.status}`));
  if (b.status === "planning" && b.plannerAgentId) {
    meta.push(agentPhoto(agents, b.plannerAgentId));
  }
  return card(b.title, meta, () => openBigTaskDetail(b, agents));
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
  if (b.status === "planning") {
    children.push(actionRow([
      button("Stop planning", { variant: "danger", onclick: () => {
        if (!confirm(`Stop planning "${b.title}"?`)) return;
        act(() => api.stopPlanning(b.id));
      }}),
    ]));
  }
  const side = buildSidebar([
    asideField("Status", [pill(b.status, `status-${b.status}`)]),
    b.plannerAgentId ? asideField("Planner", [tag(agentName(agents, b.plannerAgentId), "agent")]) : null,
    (b.constraints?.length)
      ? asideField("Constraints", b.constraints.map((c) => el("code", { class: "verify-cmd" }, [c])))
      : null,
  ]);
  openModal(b.title, el("div", { class: "detail" }, children), { wide: true, sidebar: side });
}

function decideCard(d: Decision): HTMLElement {
  return card(d.question, [tag(d.taskId ? "task" : "plan")], () => openDecideDetail(d));
}

function reviewCard(it: ReviewItem, agents: Agent[]): HTMLElement {
  const t = it.task;
  return card(
    t.title,
    [tag(t.status, `status-${t.status}`), tag(t.riskTier, `risk-${t.riskTier}`)],
    () => openReviewDetail(it, agents),
  );
}

function auditCard(it: ReviewItem): HTMLElement {
  const t = it.task;
  return card(
    t.title,
    [tag("auto-merged"), tag(t.riskTier, `risk-${t.riskTier}`)],
    () => openAuditDetail(it),
  );
}

// Cards stay quiet: avatar + risk, plus priority only when it deviates from
// the medium default. Reporter, topic tags, deps etc. live in the detail
// sidebar — repeating them here turns every card into equal-weight noise.
function taskCard(t: Task, agents: Agent[]): HTMLElement {
  const meta: (Node | string)[] = [];
  if (t.agentId) meta.push(agentPhoto(agents, t.agentId));
  meta.push(tag(t.riskTier, `risk-${t.riskTier}`));
  if (t.priority && t.priority !== "medium") meta.push(tag(t.priority, `priority-${t.priority}`));
  return card(t.title, meta, () => openTaskDetail(t, agents));
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
        const feedback = prompt("What should the planner change?")?.trim();
        if (feedback) act(() => api.revisePlan(p.id, feedback));
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
          ? button("Merge", { variant: "primary", onclick: () => act(() => api.acceptTask(task.id)) })
          : green && hasAdvisoryFailure
            ? button("Merge anyway", { onclick: () => {
                if (!confirm(`Gates failed on "${task.title}". Merge its work into the base branch anyway?`)) return;
                act(() => api.acceptTask(task.id, true));
              }})
            : button("Retry", { variant: "primary", onclick: () => act(() => api.retryTask(task.id)) }),
        !green && task.branch
          ? button("Merge anyway", { variant: "danger", onclick: () => {
              if (!confirm(`Gates failed on "${task.title}". Merge its work into the base branch anyway?`)) return;
              act(() => api.acceptTask(task.id, true));
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
          const reason = requireCommentInput("A reason is required to kick a task back.");
          if (reason === null) return;
          act(() => api.rejectTask(task.id, reason));
        }}),
      ]),
    ]),
    commentsSection(task.id),
  ]);
  const side = taskDetailSidebar(task, agents);
  openTaskId = task.id;
  openModal(task.title, body, { wide: true, sidebar: side });
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
    commentsSection(task.id),
  ]);
  const side = buildSidebar([
    asideField("Status", [tag("auto-merged")]),
    asideField("Risk", [tag(task.riskTier, `risk-${task.riskTier}`)]),
    task.branch ? asideField("Branch", [el("code", { class: "branch" }, [task.branch])]) : null,
  ]);
  openTaskId = task.id;
  openModal(task.title, body, { wide: true, sidebar: side });
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
  children.push(commentsSection(t.id));

  openTaskId = t.id;
  openModal(t.title, el("div", { class: "detail" }, children), { wide: true, sidebar: side });
  loadEvidence(t.id);
  loadComments(t.id);
}

// The task whose detail modal is currently open (if any). Comment websocket
// events that target this task reload the in-modal list live; cleared so the
// list paints under the agent labels we already have.
let openTaskId: string | null = null;

// imageAttach bundles the shared image-attach UI used by the comment composer
// and the create/define forms: a hidden file picker, an "Attach image" button,
// pending thumbnails with remove buttons, and paste-to-attach wiring on a
// textarea. Files upload immediately; urls() is what submit should send, and
// reset() clears the pending set after a successful post.
function imageAttach(pasteTarget: HTMLTextAreaElement, err: HTMLElement) {
  const pending: string[] = []; // uploaded image URLs awaiting submit
  const previews = el("div", { class: "attachments" }, []);

  const render = () => {
    clear(previews);
    pending.forEach((url, i) => {
      previews.append(el("div", { class: "attach-pending" }, [
        el("img", { src: url, class: "attach-thumb", alt: "attachment" }),
        el("button", { type: "button", class: "attach-remove", title: "Remove image", onclick: () => {
          pending.splice(i, 1);
          render();
        } }, ["×"]),
      ]));
    });
  };

  const upload = async (files: File[]) => {
    err.textContent = "";
    for (const f of files) {
      if (!f.type.startsWith("image/")) continue;
      try {
        pending.push(await api.uploadImage(f));
      } catch (e) {
        err.textContent = (e as Error).message;
      }
    }
    render();
  };

  const picker = el("input", {
    type: "file",
    accept: "image/png,image/jpeg,image/gif,image/webp",
    multiple: true,
    class: "attach-file",
    onchange: () => {
      upload(Array.from(picker.files ?? []));
      picker.value = "";
    },
  }) as HTMLInputElement;

  pasteTarget.addEventListener("paste", (e: ClipboardEvent) => {
    const files = Array.from(e.clipboardData?.files ?? []).filter((f) => f.type.startsWith("image/"));
    if (files.length === 0) return;
    e.preventDefault();
    upload(files);
  });

  const attachBtn = el("button", {
    type: "button",
    title: "Attach images (or paste one into the text field)",
    onclick: () => picker.click(),
  }, ["Attach image"]);

  return {
    previews,
    controls: [picker, attachBtn],
    urls: () => [...pending],
    reset: () => {
      pending.length = 0;
      render();
    },
  };
}



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
// immediately and submit as attachment URLs alongside the text.
function commentsSection(id: string): HTMLElement {
  const input = el("textarea", { class: "comment-input", placeholder: "Add a comment… (paste or attach images)", rows: "2" }) as HTMLTextAreaElement;
  const err = el("div", { class: "form-error" }, []);
  const attach = imageAttach(input, err);

  const submit = async () => {
    const text = input.value.trim();
    const attachments = attach.urls();
    if (!text && attachments.length === 0) return;
    err.textContent = "";
    try {
      await api.addComment(id, text, attachments);
      input.value = "";
      attach.reset();
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
  if (c.body) children.push(el("div", { class: "comment-body" }, [c.body]));
  if (c.attachments?.length) children.push(attachmentGallery(c.attachments));
  return el("div", { class: cls }, children);
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
    el("div", { class: "form-actions" }, [button("Define big task", { variant: "primary", type: "submit" })]),
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

// updateIncidentBanner fetches open incidents and shows a warning strip if any exist.
// Best-effort — a fetch failure never breaks the board.
async function updateIncidentBanner(): Promise<void> {
  const banner = document.getElementById("incident-banner");
  if (!banner) return;
  try {
    const incidents = await api.listIncidents("open");
    if (!incidents || incidents.length === 0) {
      banner.style.display = "none";
      return;
    }
    banner.style.display = "";
    clear(banner);
    const n = incidents.length;
    banner.append(
      el("span", { class: "incident-banner-icon" }, ["⚠"]),
      el("span", { class: "incident-banner-msg" }, [
        `${n} open incident${n !== 1 ? "s" : ""}`,
      ]),
      el("a", {
        href: "#incidents",
        class: "incident-banner-link",
        onclick: (e: Event) => { e.preventDefault(); location.hash = "incidents"; },
      }, ["view →"]),
    );
  } catch {
    /* best-effort — never break the board */
  }
}

// updateReleaseUI refreshes the Ship button count and the release strip with
// the latest release state. Both controls are hidden when deploy is not wired up
// (no releases in the store and unshipped returns an empty list on a fresh repo).
async function updateReleaseUI(): Promise<void> {
  const strip = document.getElementById("release-strip");
  const shipBtn = document.getElementById("ship-btn") as HTMLButtonElement | null;
  if (!strip && !shipBtn) return;
  try {
    const [releases, unshippedTasks] = await Promise.all([
      api.listReleases(),
      api.unshipped(),
    ]);
    const n = unshippedTasks.length;
    const latest = releases.length ? releases[releases.length - 1] : null;
    const inFlight = latest && (latest.status === "deploying" || latest.status === "baking");

    // Ship button — show only when there is at least one release or unshipped task
    // (proxy for deploy being configured). Disabled when in-flight or count is 0.
    if (shipBtn) {
      const deployActive = releases.length > 0 || n > 0;
      shipBtn.style.display = deployActive ? "" : "none";
      shipBtn.textContent = `Ship · ${n}`;
      shipBtn.disabled = n === 0 || !!inFlight;
    }

    // Release strip
    if (strip) {
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
    }
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

function openShipConfirm(): void {
  api.unshipped().then((tasks) => {
    const n = tasks.length;
    if (n === 0) return;
    const taskList = el("ul", { class: "ship-task-list" },
      tasks.map((t) => el("li", {}, [t.title]))
    );
    const body = el("div", { class: "detail" }, [
      el("p", {}, [`Ship ${n} merged task${n !== 1 ? "s" : ""} as a new release?`]),
      taskList,
      actionRow([
        button("Confirm ship", { variant: "primary", onclick: () => {
          closeModal();
          api.ship().then(() => updateReleaseUI()).catch((e: unknown) => {
            alert((e as Error).message);
          });
        }}),
        button("Cancel", { onclick: () => closeModal() }),
      ]),
    ]);
    openModal("Ship release", body, {});
  }).catch((e: unknown) => {
    alert((e as Error).message);
  });
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
