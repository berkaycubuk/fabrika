// Tasks view: one flat, scannable list of every task in the factory. Each row
// carries a right-side cluster that answers "what is this task doing right now?"
// — a live pulse while an agent works, a "needs you" flag at a human gate, or a
// relative timestamp otherwise — plus a small agent avatar chip. Filter tabs
// narrow the list client-side without a refetch.
import { el, clear } from "../dom.js";
import { button, tag } from "../components.js";
import { api } from "../api.js";
import { pulseEl, IN_FLIGHT, openTaskDetail } from "./board.js";
import type { Task, Agent } from "../types.js";

// Tab definitions: each is a predicate over a task. "needs" mirrors the board's
// gate columns (work parked for a human), "running" the in-flight set, "done"
// the merged terminal state.
type TabId = "all" | "needs" | "running" | "done";
const TABS: { id: TabId; label: string; match: (t: Task) => boolean }[] = [
  { id: "all", label: "All", match: () => true },
  { id: "needs", label: "Needs you", match: needsYou },
  { id: "running", label: "Running", match: (t) => IN_FLIGHT.includes(t.status) },
  { id: "done", label: "Done", match: (t) => t.status === "merged" },
];

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
    el("div", { class: "task-tabs", id: "task-tabs" }, TABS.map(tabButton)),
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
  list.replaceChildren();
  if (rows.length === 0) {
    list.append(el("div", { class: "board-empty" }, ["No tasks"]));
    return;
  }
  for (const t of rows) list.append(taskRow(t));
}

function taskRow(t: Task): HTMLElement {
  return el("div", { class: "task-row", onclick: () => openTaskDetail(t, storedAgents) }, [
    el("div", { class: "task-row-main" }, [
      el("div", { class: "task-row-title" }, [t.title]),
      el("div", { class: "task-row-sub" }, [
        tag(t.status, `status-${t.status}`),
        tag(t.riskTier, `risk-${t.riskTier}`),
      ]),
    ]),
    el("div", { class: "task-row-right" }, [meta(t), avatarChip(t)]),
  ]);
}

// meta is the right-cluster status line: a live pulse while the agent works, an
// amber "needs you" at a gate, otherwise when the task was created.
function meta(t: Task): HTMLElement {
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
