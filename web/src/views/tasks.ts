// Tasks view: unified list of all tasks across the factory. Each row carries a
// status glyph + colored pill on the left that track the task through its
// lifecycle, the same way the board columns read; on the right a live cluster
// answers "what's it doing right now?" — a live pulse while an agent works, an
// amber "needs you" at a human gate, or a relative timestamp otherwise — beside
// a small agent avatar chip. Filter tabs narrow rows client-side.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { button, pill, tag } from "../components.js";
import { pulseEl, IN_FLIGHT, openTaskDetail } from "./board.js";
import type { Task, Agent } from "../types.js";

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

// Tab definitions: each is a predicate over a task. "all" is everything still in
// motion (anything not yet at a terminal state), "needs" mirrors the board's
// gate columns (work parked for a human), "running" the in-flight set, "done"
// the terminal merged/closed states.
type TabId = "all" | "needs" | "running" | "done";
const TABS: { id: TabId; label: string; match: (t: Task) => boolean }[] = [
  { id: "all", label: "All active", match: (t) => !DONE_STATUSES.includes(t.status) },
  { id: "needs", label: "Needs you", match: needsYou },
  { id: "running", label: "Running", match: (t) => IN_FLIGHT.includes(t.status) },
  { id: "done", label: "Done", match: (t) => DONE_STATUSES.includes(t.status) },
];

// Terminal states: a task here has left the pipeline (merged after auto/manual
// review, or closed without merging). Everything else counts as active.
const DONE_STATUSES = ["merged", "closed"];

// Statuses where the task sits at a human gate (accept / blocked / failed), plus
// auto-merged work sampled for audit. Amber "needs you" — same contract as the
// board's gate columns.
const GATE_STATUSES = ["review", "blocked", "failed"];
function needsYou(t: Task): boolean {
  return GATE_STATUSES.includes(t.status) || (t.status === "merged" && t.auditFlagged);
}

let storedTasks: Task[] = [];
let storedAgents: Agent[] = [];
let activeTab: TabId = "all";
let search = "";

export function renderTasks(root: HTMLElement): void {
  clear(root);
  activeTab = "all";
  search = "";
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
        oninput: (e: Event) => {
          search = (e.target as HTMLInputElement).value;
          paint();
        },
      }),
      el("div", { class: "header-actions" }, [
        button("Create task", { onclick: () => {} }),
        button("Define big task", { variant: "primary", onclick: () => {} }),
      ]),
    ]),
    el("div", { class: "task-toolbar" }, [
      el("div", { class: "task-tabs", id: "task-tabs" }, TABS.map(tabButton)),
      el("div", { class: "task-view-controls" }, VIEW_CONTROLS.map(viewControl)),
    ]),
    el("div", { id: "tasks-err", class: "form-error" }, []),
    el("div", { class: "task-list", id: "task-list" }, []),
  );
  void refresh();
}

function tabButton(t: (typeof TABS)[number]): HTMLElement {
  return el("button", {
    class: "task-tab" + (t.id === activeTab ? " active" : ""),
    "data-tab": t.id,
    onclick: () => {
      activeTab = t.id;
      document.querySelectorAll(".task-tab").forEach((b) =>
        b.classList.toggle("active", (b as HTMLElement).dataset.tab === activeTab));
      paint();
    },
  }, [t.label, el("span", { class: "task-tab-count", "data-tabcount": t.id }, [])]);
}

// View controls: filter / sort / layout. These mirror the right cluster of the
// Figma toolbar — present as the surface for upcoming view options; for now they
// are inert icon buttons that anchor the toolbar's right edge.
const VIEW_CONTROLS: { id: string; label: string; paths: string[] }[] = [
  { id: "filter", label: "Filter", paths: ["M2.5 3.5h11l-4.3 5.1v3.6l-2.4 1.3V8.6z"] },
  { id: "sort", label: "Sort", paths: ["M3 4.5h10", "M3 8h6.5", "M3 11.5h3.5"] },
  { id: "layout", label: "Layout", paths: ["M2.5 3.5h11v9h-11z", "M2.5 6.8h11", "M2.5 9.9h11"] },
];

function viewControl(c: (typeof VIEW_CONTROLS)[number]): HTMLElement {
  return el("button", {
    type: "button",
    class: "view-ctl",
    "data-ctl": c.id,
    title: c.label,
    "aria-label": c.label,
  }, [svgIcon(c.paths)]);
}

// svgIcon builds a 16-grid stroked glyph from one or more path commands. `el`
// goes through createElement (HTML namespace), so SVG nodes are built here with
// the SVG namespace and currentColor strokes so they inherit the button's tone.
const SVG_NS = "http://www.w3.org/2000/svg";
function svgIcon(paths: string[]): SVGElement {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("viewBox", "0 0 16 16");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.4");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  for (const d of paths) {
    const p = document.createElementNS(SVG_NS, "path");
    p.setAttribute("d", d);
    svg.appendChild(p);
  }
  return svg;
}

async function refresh(): Promise<void> {
  const list = document.getElementById("task-list");
  if (!list) return;
  const errBox = document.getElementById("tasks-err");
  try {
    const [tasks, agents] = await Promise.all([api.listTasks(), api.listAgents()]);
    if (!document.getElementById("task-list")) return;
    if (errBox) errBox.textContent = "";
    storedTasks = tasks;
    storedAgents = agents;
    paint();
  } catch (e) {
    if (errBox) errBox.textContent = (e as Error).message;
  }
}

function paint(): void {
  const list = document.getElementById("task-list");
  if (!list) return;
  const q = search.trim().toLowerCase();
  const tabMatch = TABS.find((t) => t.id === activeTab)!.match;
  // Tab counts reflect the search-filtered population so the badges stay honest
  // while typing.
  const searched = storedTasks.filter((t) => !q || t.title.toLowerCase().includes(q));
  for (const t of TABS) {
    const count = document.querySelector(`[data-tabcount="${t.id}"]`);
    if (count) count.textContent = String(searched.filter(t.match).length);
  }
  const rows = searched.filter(tabMatch);
  clear(list);
  if (rows.length === 0) {
    list.append(el("div", { class: "board-empty" }, ["No tasks"]));
    return;
  }
  for (const t of rows) list.append(taskRow(t));
}

function taskRow(t: Task): HTMLElement {
  const meta: (Node | string)[] = [statusPill(t.status)];
  const risk = riskChip(t.riskTier);
  if (risk) meta.push(risk);
  return el("div", { class: "task-row", onclick: () => openTaskDetail(t, storedAgents) }, [
    statusGlyph(t.status),
    el("div", { class: "task-row-main" }, [
      el("div", { class: "task-row-title" }, [t.title]),
    ]),
    el("div", { class: "task-row-meta" }, meta),
    el("div", { class: "task-row-right" }, [liveMeta(t), avatarChip(t)]),
  ]);
}

// liveMeta is the right-cluster status line: a live pulse while the agent works,
// an amber "needs you" at a gate, otherwise when the task was created.
function liveMeta(t: Task): HTMLElement {
  if (IN_FLIGHT.includes(t.status)) return pulseEl(t);
  if (needsYou(t)) {
    return el("div", { class: "task-needs" }, [
      el("span", { class: "gate-dot", title: "needs you" }, []),
      "needs you",
    ]);
  }
  return el("div", { class: "task-time", title: t.createdAt }, [relTime(t.createdAt)]);
}

// avatarChip is a small round chip with the assigned agent's initial; an empty
// dashed ring stands in when the task is unassigned.
function avatarChip(t: Task): HTMLElement {
  if (!t.agentId) return el("span", { class: "task-avatar unassigned", title: "unassigned" }, []);
  const a = storedAgents.find((x) => x.id === t.agentId);
  const name = a?.name ?? "—";
  const initial = name.trim().charAt(0).toUpperCase() || "?";
  return el("span", { class: "task-avatar", title: name }, [initial]);
}

// relTime renders a UTC "YYYY-MM-DD HH:MM:SS" stamp as a compact age (just now,
// 5m, 3h, 2d). SQLite stamps are space-separated and zone-less, so normalise to
// ISO-UTC before parsing rather than trusting the engine's local-time guess.
function relTime(stamp: string): string {
  if (!stamp) return "";
  const ms = Date.parse(stamp.replace(" ", "T") + "Z");
  if (Number.isNaN(ms)) return "";
  const sec = Math.max(0, Math.floor((Date.now() - ms) / 1000));
  if (sec < 45) return "just now";
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

// onTasksEvent repaints the list from a fresh snapshot when the view is mounted;
// heartbeats are handled separately by the board's shared [data-pulse] repaint.
export function onTasksEvent(): void {
  if (!document.getElementById("task-list")) return;
  void refresh();
}
