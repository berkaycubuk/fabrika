// Tasks view: unified list of all tasks across the factory. Each row carries a
// status glyph + colored pill that track the task through its lifecycle, so the
// stage reads at a glance the same way the board columns do — hollow circle for
// queued work, a dashed spinner while an agent is on it, an amber ring-with-dot
// when a human gate is waiting, a green check once merged. Risk tier rides
// alongside as a .tag chip.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { button, pill, tag } from "../components.js";
import { ciBadge } from "./ci-badge.js";
import { pushStatusLabel } from "../push.js";
import { DEFAULT_AVATAR } from "../avatar.js";
import type { Task, Agent, BigTask } from "../types.js";

// A status glyph shape. Kept abstract from any one status so the same shape can
// stand for several (running and claimed both spin) and the CSS owns the look.
type Glyph = "hollow" | "spinner" | "ring" | "check" | "dot";

interface StatusMeta {
  label: string;
  glyph: Glyph;
  // Colour keyword resolved to a `tg-<tone>` CSS class. The pill picks up its
  // own colour from `status-<status>` to match the board column.
  tone: "indigo" | "accent" | "amber" | "green" | "red" | "muted";
}

// Single source of truth for how a task.status reads on the Tasks page. Glyphs
// mirror the board: hollow indigo while queued (backlog/ready), a stage-coloured
// dashed spinner in flight (running=accent, verifying=amber), an amber
// ring-with-dot when a human gate is waiting (review → Accept), a green check
// once merged. Unknown statuses fall back to a muted dot below.
export const STATUS_META: Record<string, StatusMeta> = {
  backlog:   { label: "backlog",   glyph: "hollow",  tone: "indigo" },
  ready:     { label: "ready",     glyph: "hollow",  tone: "indigo" },
  claimed:   { label: "claimed",   glyph: "spinner", tone: "accent" },
  running:   { label: "running",   glyph: "spinner", tone: "accent" },
  verifying: { label: "verifying", glyph: "spinner", tone: "amber" },
  review:    { label: "in review", glyph: "ring",    tone: "amber" },
  blocked:   { label: "blocked",   glyph: "ring",    tone: "red" },
  failed:    { label: "failed",    glyph: "dot",     tone: "red" },
  merged:    { label: "merged",    glyph: "check",   tone: "green" },
  closed:    { label: "closed",    glyph: "dot",     tone: "muted" },
};

const FALLBACK: StatusMeta = { label: "", glyph: "dot", tone: "muted" };

function metaFor(status: string): StatusMeta {
  return STATUS_META[status] ?? { ...FALLBACK, label: status };
}

// statusGlyph builds the lifecycle-stage glyph for a status. The shape and
// colour are pure CSS (see .tg in style.css); we only pick the classes and label
// the node for assistive tech.
export function statusGlyph(status: string): HTMLElement {
  const m = metaFor(status);
  return el("span", {
    class: `tg tg-${m.glyph} tg-${m.tone}`,
    role: "img",
    title: m.label,
    "aria-label": `status: ${m.label}`,
  });
}

// statusPill builds the colored lifecycle pill. The `status-<status>` class is
// what colours it to match the board column it would sit in.
export function statusPill(status: string): HTMLElement {
  return pill(metaFor(status).label, `status-${status}`);
}

// riskChip renders the risk tier as a .tag chip (low/medium/high), reusing the
// existing risk-* tag colours. Empty tiers produce nothing.
export function riskChip(tier: string): HTMLElement | null {
  if (!tier) return null;
  return tag(tier, `risk-${tier}`);
}

// avatarFor renders the assigned agent's photo as a small avatar, name on hover;
// the generic silhouette stands in until an agent self-photo is set. Mirrors the
// board's agentPhoto so a task reads the same on either surface.
function avatarFor(agents: Agent[], id: string): HTMLElement {
  const a = agents.find((x) => x.id === id);
  const name = a?.name ?? "—";
  return el("img", { class: "card-agent-photo", src: a?.photo || DEFAULT_AVATAR, alt: name, title: name });
}

// metaColumn is the right-aligned secondary cluster: priority (only when it
// deviates from the medium default), a CI-failure badge, and a push-status tag.
// Empty when none apply, so a quiet task carries no noise. Mirrors the board
// card's meta so the same signals read the same way here.
function metaColumn(t: Task): HTMLElement | null {
  const items: (Node | string)[] = [];
  if (t.priority && t.priority !== "medium") items.push(tag(t.priority, `priority-${t.priority}`));
  const badge = ciBadge(t);
  if (badge) items.push(badge);
  const pl = pushStatusLabel(t);
  if (pl) items.push(tag(pl, `push-${pl}`));
  if (items.length === 0) return null;
  return el("div", { class: "task-row-metacol" }, items);
}

// taskRow renders one task left to right: lifecycle glyph, muted short id,
// semibold title with an optional parent big-task breadcrumb, then the right
// cluster — status pill, optional risk tag, right-aligned meta column, agent
// avatar. Big-task titles are resolved from the map built in loadTasks.
function taskRow(t: Task, agents: Agent[], bigTaskTitles: Map<string, string>): HTMLElement {
  const main: (Node | string)[] = [el("span", { class: "task-row-title" }, [t.title])];
  const parent = t.bigTaskId ? bigTaskTitles.get(t.bigTaskId) : undefined;
  if (parent) main.push(el("span", { class: "task-row-crumb" }, [`› ${parent}`]));

  const right: (Node | string)[] = [statusPill(t.status)];
  const risk = riskChip(t.riskTier);
  if (risk) right.push(risk);
  const meta = metaColumn(t);
  if (meta) right.push(meta);
  if (t.agentId) right.push(avatarFor(agents, t.agentId));

  return el("div", { class: "task-row", "data-title": t.title.toLowerCase() }, [
    statusGlyph(t.status),
    el("span", { class: "task-row-id muted" }, [t.id.slice(0, 6)]),
    el("div", { class: "task-row-main" }, main),
    el("div", { class: "task-row-right" }, right),
  ]);
}

export function renderTasks(root: HTMLElement): void {
  clear(root);
  const list = el("div", { class: "task-list", id: "task-list" }, []);
  root.append(
    el("div", { class: "view-header board-header" }, [
      el("div", {}, [
        el("h1", {}, ["Tasks"]),
        el("p", { class: "muted" }, ["Sessions don't scale. Tasks do."]),
      ]),
      el("input", {
        id: "tasks-search",
        class: "board-search",
        type: "text",
        placeholder: "Search tasks…",
        oninput: (e: Event) => filterTasks((e.target as HTMLInputElement).value),
      }),
      el("div", { class: "header-actions" }, [
        button("Create task", { onclick: () => {} }),
        button("Define big task", { variant: "primary", onclick: () => {} }),
      ]),
    ]),
    el("div", { id: "tasks-err", class: "form-error" }, []),
    list,
  );
  void loadTasks();
}

async function loadTasks(): Promise<void> {
  const list = document.getElementById("task-list");
  const err = document.getElementById("tasks-err");
  if (!list) return;
  try {
    const [tasks, agents, bigTasks] = await Promise.all([
      api.listTasks(),
      api.listAgents(),
      api.listBigTasks(),
    ]);
    if (err) err.textContent = "";
    clear(list);
    if (tasks.length === 0) {
      list.append(el("div", { class: "board-empty" }, ["No tasks yet."]));
      return;
    }
    const bigTaskTitles = new Map<string, string>(bigTasks.map((b: BigTask) => [b.id, b.title]));
    for (const t of tasks) list.append(taskRow(t, agents, bigTaskTitles));
  } catch (e) {
    if (err) err.textContent = (e as Error).message;
  }
}

function filterTasks(query: string): void {
  const q = query.trim().toLowerCase();
  const rows = document.querySelectorAll<HTMLElement>("#task-list .task-row");
  for (const row of rows) {
    const hit = !q || (row.dataset.title ?? "").includes(q);
    row.style.display = hit ? "" : "none";
  }
}
