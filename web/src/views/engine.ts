// Engine room: observability for the live scheduler (SPECS.md §10), styled as an
// analytics dashboard — an editorial hero beside a contained panel that carries
// throughput stat cards, a segmented view switch, and the agent share-of-work
// table / status board / trust + autonomy controls.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import type { Agent, Task, Metrics, AgentMetrics } from "../types.js";

// Board columns map task statuses to lifecycle stages, left to right.
const COLUMNS: { label: string; statuses: string[] }[] = [
  { label: "Ready", statuses: ["ready"] },
  { label: "Running", statuses: ["claimed", "running"] },
  { label: "Verifying", statuses: ["verifying"] },
  { label: "Review", statuses: ["review"] },
  { label: "Needs fix", statuses: ["blocked", "failed"] },
  { label: "Merged", statuses: ["merged"] },
];

// Warm/cool palette cycled across the share bars (mirrors the reference look).
const BARS = ["var(--accent)", "var(--tan)", "var(--teal)", "var(--green)", "var(--amber)", "var(--red)"];

type Tab = "agents" | "board" | "trust";
let tab: Tab = "agents";

export function renderEngine(root: HTMLElement): void {
  clear(root);

  const tabBtn = (id: Tab, label: string) =>
    el("button", {
      class: tab === id ? "on" : "",
      onclick: () => {
        tab = id;
        document.querySelectorAll("#engine-seg button").forEach((b, i) => {
          b.classList.toggle("on", ["agents", "board", "trust"][i] === tab);
        });
        refresh();
      },
    }, [label]);

  root.append(
    el("div", { class: "dash" }, [
      el("header", { class: "dash-hero" }, [
        el("div", { class: "eyebrow" }, [el("span", { class: "eyebrow-dot" }, []), "Engine room"]),
        el("h1", {}, ["Factory throughput at a glance"]),
        el("p", { class: "lede" }, [
          "See which agents are shipping the most, where work is flowing, and where the line backs up. ",
          "Glance here to calibrate trust — you don't operate it.",
        ]),
      ]),
      el("section", { class: "dash-panel" }, [
        el("div", { class: "panel-head" }, [
          el("div", {}, [
            el("h2", {}, ["Factory"]),
            el("p", { class: "muted" }, ["Throughput and autonomy across all agents."]),
          ]),
          el("div", { class: "panel-tools" }, [
            el("div", { id: "engine-seg", class: "seg" }, [
              tabBtn("agents", "Top agents"),
              tabBtn("board", "Board"),
              tabBtn("trust", "Trust"),
            ]),
          ]),
        ]),
        el("div", { id: "engine-stats", class: "stat-grid" }, []),
        el("div", { id: "engine-tab" }, []),
      ]),
    ]),
  );
  refresh();
}

async function refresh(): Promise<void> {
  if (!document.getElementById("engine-tab")) return;
  try {
    const [metrics, agents, tasks] = await Promise.all([
      api.metrics(),
      api.listAgents(),
      api.listTasks(),
    ]);
    renderStats(metrics);
    if (tab === "agents") renderShare(metrics);
    else if (tab === "board") renderBoard(tasks, agents);
    else renderTrust(metrics);
  } catch (e) {
    const body = document.getElementById("engine-tab");
    if (body) {
      clear(body);
      body.append(el("p", { class: "share-empty" }, [(e as Error).message]));
    }
  }
}

function stat(label: string, value: string, unit?: string): HTMLElement {
  const v = el("div", { class: "stat-value" }, [value]);
  if (unit) v.append(el("span", { class: "unit" }, [` ${unit}`]));
  return el("div", { class: "stat" }, [el("div", { class: "stat-label" }, [label]), v]);
}

function renderStats(m: Metrics): void {
  const grid = document.getElementById("engine-stats");
  if (!grid) return;
  clear(grid);
  grid.append(
    stat("In flight", String(m.wip), m.wipCap > 0 ? `/ ${m.wipCap}` : undefined),
    stat("Ready", String(m.ready)),
    stat("In review", String(m.inReview)),
    stat("Merged", String(m.merged)),
  );
}

// renderShare ranks agents by share of shipped work — the dashboard's headline
// table. Falls back to current load when nothing has merged yet.
function renderShare(m: Metrics): void {
  const body = document.getElementById("engine-tab");
  if (!body) return;
  clear(body);

  if (m.agents.length === 0) {
    body.append(el("p", { class: "share-empty" }, ["No agents registered."]));
    return;
  }

  const totalMerged = m.agents.reduce((s, a) => s + a.merged, 0);
  const byLoad = totalMerged === 0;
  const weight = (a: AgentMetrics) => (byLoad ? a.running : a.merged);
  const total = m.agents.reduce((s, a) => s + weight(a), 0);
  const ranked = [...m.agents].sort((x, y) => weight(y) - weight(x));

  const table = el("table", { class: "share-table" }, [
    el("thead", {}, [
      el("tr", {}, [
        el("th", {}, ["Agent"]),
        el("th", {}, [byLoad ? "Share of load" : "Share of work"]),
        el("th", { class: "num" }, [byLoad ? "In flight" : "Shipped"]),
      ]),
    ]),
  ]);

  const tbody = el("tbody", {});
  ranked.forEach((a, i) => {
    const w = weight(a);
    const share = total > 0 ? w / total : 0;
    const pct = Math.round(share * 100);
    const dotClass = a.running > 0 ? "pill busy" : "pill idle";
    tbody.append(
      el("tr", {}, [
        el("td", { class: "who" }, [
          el("span", { class: dotClass, title: a.enabled ? "enabled" : "disabled" }, [
            a.running > 0 ? "working" : "idle",
          ]),
          " " + a.name,
        ]),
        el("td", {}, [
          el("div", { class: "share-cell" }, [
            el("div", { class: "share-track" }, [
              el("div", {
                class: "share-fill",
                style: `width:${Math.max(share * 100, w > 0 ? 4 : 0)}%;background:${BARS[i % BARS.length]}`,
              }, []),
            ]),
            el("span", { class: "share-pct" }, [`${pct}%`]),
          ]),
        ]),
        el("td", { class: "num" }, [String(byLoad ? a.running : a.merged)]),
      ]),
    );
  });
  table.append(tbody);
  body.append(table);

  if (byLoad) {
    body.append(el("p", { class: "share-empty" }, ["No work shipped yet — ranking by current load."]));
  }
}

// renderTrust shows the Phase 3 trust numbers and the autonomy controls. The
// headline pairing is touches-per-unit (drive down) vs change-failure-rate (keep
// flat) as auto-merge share widens (SPECS §13, §14).
function renderTrust(m: Metrics): void {
  const body = document.getElementById("engine-tab");
  if (!body) return;
  clear(body);
  const pct = (n: number) => `${Math.round(n * 100)}%`;

  body.append(
    el("div", { class: "stat-grid" }, [
      stat("Touches / unit", m.merged > 0 ? m.touchesPerUnit.toFixed(2) : "—"),
      stat("Change-fail rate", m.merged > 0 ? pct(m.changeFailRate) : "—"),
      stat("Auto-merged", m.merged > 0 ? String(m.autoMerged) : "0",
        m.merged > 0 ? `· ${pct(m.autoMergeShare)}` : undefined),
      stat("Audit queue", String(m.auditQueue)),
    ]),
    autonomyControls(m),
  );
}

function autonomyControls(m: Metrics): HTMLElement {
  const wip = el("input", {
    type: "number", min: "0", value: String(m.wipCap || 0), title: "0 = unlimited",
  }) as HTMLInputElement;
  const setWip = el("form", {
    class: "wip-cap",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      try {
        await api.putSettings({ wip_cap: String(parseInt(wip.value, 10) || 0) });
        refresh();
      } catch (err) {
        alert((err as Error).message);
      }
    },
  }, [el("label", {}, ["WIP cap"]), wip, el("button", { class: "primary", type: "submit" }, ["Set"])]);

  const rate = el("input", {
    type: "number", min: "0", max: "1", step: "0.05",
    value: String(m.auditRate ?? 0), title: "Fraction of auto-merges to sample for audit (0–1)",
  }) as HTMLInputElement;

  const mutation = el("input", {
    type: "checkbox", title: "Run mutation testing on green branches before auto-merge",
  }) as HTMLInputElement;
  mutation.checked = m.mutationTesting;
  mutation.onchange = async () => {
    try {
      await api.putSettings({ mutation_testing: mutation.checked ? "on" : "off" });
      refresh();
    } catch (e) {
      alert((e as Error).message);
    }
  };

  const setRate = el("form", {
    class: "wip-cap",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      try {
        await api.putSettings({ audit_rate: String(parseFloat(rate.value) || 0) });
        refresh();
      } catch (err) {
        alert((err as Error).message);
      }
    },
  }, [
    el("label", {}, ["Audit rate"]),
    rate,
    el("button", { class: "primary", type: "submit" }, ["Set"]),
    el("label", { class: "checkbox" }, [mutation, "mutation testing"]),
  ]);

  return el("div", { class: "metrics-bar", style: "margin-top:20px" }, [setWip, setRate]);
}

function renderBoard(tasks: Task[], agents: Agent[]): void {
  const body = document.getElementById("engine-tab");
  if (!body) return;
  clear(body);
  const board = el("div", { class: "board dash-board" }, []);
  body.append(board);

  const agentName = (id: string) => agents.find((a) => a.id === id)?.name ?? "—";
  const titleOf = (id: string) => tasks.find((t) => t.id === id)?.title ?? id.slice(0, 6);

  for (const col of COLUMNS) {
    const items = tasks.filter((t) => col.statuses.includes(t.status));
    const column = el("div", { class: "board-col" }, [
      el("div", { class: "board-col-head" }, [
        col.label,
        el("span", { class: "count" }, [items.length ? String(items.length) : ""]),
      ]),
    ]);
    for (const t of items) {
      column.append(taskNode(t, agents, agentName, titleOf));
    }
    if (items.length === 0) {
      column.append(el("div", { class: "board-empty" }, ["—"]));
    }
    board.append(column);
  }
}

function taskNode(
  t: Task,
  agents: Agent[],
  agentName: (id: string) => string,
  titleOf: (id: string) => string,
): HTMLElement {
  const meta: (Node | string)[] = [];
  if (t.agentId) meta.push(el("span", { class: "tag agent" }, [agentName(t.agentId)]));
  meta.push(el("span", { class: `tag risk-${t.riskTier}` }, [t.riskTier]));
  for (const tag of t.tags ?? []) meta.push(el("span", { class: "tag" }, [tag]));
  for (const dep of t.dependsOn ?? [])
    meta.push(el("span", { class: "tag dep" }, [`after: ${titleOf(dep)}`]));

  const children: (Node | string)[] = [
    el("div", { class: "board-task-title" }, [t.title]),
    el("div", { class: "card-meta" }, meta),
  ];

  // Steering controls for not-yet-terminal tasks: reassign + cancel.
  const steerable = ["ready", "claimed", "running", "blocked", "failed"].includes(t.status);
  if (steerable) {
    children.push(steerRow(t, agents));
  }

  return el("div", { class: `board-task status-${t.status}` }, children);
}

function steerRow(t: Task, agents: Agent[]): HTMLElement {
  const select = el("select", {
    class: "assign-select",
    title: "Pin this task to an agent",
    onchange: async (e: Event) => {
      const id = (e.target as HTMLSelectElement).value;
      try {
        await api.assignTask(t.id, id);
        refresh();
      } catch (err) {
        alert((err as Error).message);
      }
    },
  }) as HTMLSelectElement;
  select.append(el("option", { value: "" }, ["auto-route"]));
  for (const a of agents) {
    const opt = el("option", { value: a.id }, [a.name]) as HTMLOptionElement;
    if (a.id === t.preferredAgentId) opt.selected = true;
    select.append(opt);
  }

  return el("div", { class: "board-task-actions" }, [
    select,
    el("button", {
      class: "link danger",
      title: "Cancel this task",
      onclick: async () => {
        if (!confirm(`Cancel “${t.title}”?`)) return;
        try {
          await api.cancelTask(t.id);
          refresh();
        } catch (err) {
          alert((err as Error).message);
        }
      },
    }, ["cancel"]),
  ]);
}

export function onEngineEvent(): void {
  if (document.getElementById("engine-tab")) refresh();
}
