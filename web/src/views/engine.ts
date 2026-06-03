// Engine room: observability for the live scheduler (SPECS.md §10). A metrics
// bar, per-agent activity, and a status board (the task DAG by lifecycle stage)
// — plus the two steering controls Phase 1 ships: WIP cap and reassignment.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import type { Agent, Task, Metrics } from "../types.js";

// Board columns map task statuses to lifecycle stages, left to right.
const COLUMNS: { label: string; statuses: string[] }[] = [
  { label: "Ready", statuses: ["ready"] },
  { label: "Running", statuses: ["claimed", "running"] },
  { label: "Verifying", statuses: ["verifying"] },
  { label: "Review", statuses: ["review"] },
  { label: "Needs fix", statuses: ["blocked", "failed"] },
  { label: "Merged", statuses: ["merged"] },
];

export function renderEngine(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Engine room"]),
      el("p", { class: "muted" }, [
        "Live scheduler activity. Glance here to calibrate trust — you don't operate it.",
      ]),
    ]),
    el("div", { id: "engine-metrics", class: "metrics-bar" }, []),
    el("h2", { class: "section-h" }, ["Trust & autonomy"]),
    el("div", { id: "engine-trust", class: "metrics-bar" }, []),
    el("h2", { class: "section-h" }, ["Agents"]),
    el("div", { id: "engine-agents", class: "card-list" }, ["Loading…"]),
    el("h2", { class: "section-h" }, ["Board"]),
    el("div", { id: "engine-board", class: "board" }, []),
  );
  refresh();
}

async function refresh(): Promise<void> {
  if (!document.getElementById("engine-board")) return;
  try {
    const [metrics, agents, tasks] = await Promise.all([
      api.metrics(),
      api.listAgents(),
      api.listTasks(),
    ]);
    renderMetrics(metrics);
    renderTrust(metrics);
    renderAgents(metrics);
    renderBoard(tasks, agents);
  } catch (e) {
    const board = document.getElementById("engine-board");
    if (board) board.textContent = (e as Error).message;
  }
}

function renderMetrics(m: Metrics): void {
  const bar = document.getElementById("engine-metrics");
  if (!bar) return;
  clear(bar);

  const stat = (label: string, value: string) =>
    el("div", { class: "metric" }, [
      el("span", { class: "metric-value" }, [value]),
      el("span", { class: "metric-label" }, [label]),
    ]);

  const wipText = m.wipCap > 0 ? `${m.wip} / ${m.wipCap}` : String(m.wip);

  bar.append(
    stat("In flight", wipText),
    stat("Ready", String(m.ready)),
    stat("In review", String(m.inReview)),
    stat("Needs fix", String(m.blocked)),
    stat("Merged", String(m.merged)),
    wipCapControl(m.wipCap),
  );
}

function wipCapControl(current: number): HTMLElement {
  const input = el("input", {
    type: "number",
    min: "0",
    value: String(current || 0),
    title: "0 = unlimited",
  }) as HTMLInputElement;
  return el("form", {
    class: "wip-cap",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      try {
        await api.putSettings({ wip_cap: String(parseInt(input.value, 10) || 0) });
        refresh();
      } catch (err) {
        alert((err as Error).message);
      }
    },
  }, [
    el("label", {}, ["WIP cap"]),
    input,
    el("button", { class: "primary", type: "submit" }, ["Set"]),
  ]);
}

// renderTrust shows the Phase 3 trust numbers and the autonomy controls. The
// headline pairing is touches-per-unit (drive down) vs change-failure-rate (keep
// flat) as auto-merge share widens (SPECS §13, §14).
function renderTrust(m: Metrics): void {
  const bar = document.getElementById("engine-trust");
  if (!bar) return;
  clear(bar);

  const stat = (label: string, value: string, title = "") =>
    el("div", { class: "metric", title }, [
      el("span", { class: "metric-value" }, [value]),
      el("span", { class: "metric-label" }, [label]),
    ]);
  const pct = (n: number) => `${Math.round(n * 100)}%`;

  bar.append(
    stat("Touches / unit", m.merged > 0 ? m.touchesPerUnit.toFixed(2) : "—",
      "Human interventions per shipped unit. The anti-bottleneck number — drive it down."),
    stat("Change-fail rate", m.merged > 0 ? pct(m.changeFailRate) : "—",
      "Share of merges later reverted. The trust number — keep it flat as autonomy widens."),
    stat("Auto-merged", m.merged > 0 ? `${m.autoMerged} (${pct(m.autoMergeShare)})` : "0",
      "Merged by the machine without you."),
    stat("Audit queue", String(m.auditQueue), "Auto-merges sampled for a post-merge spot-check."),
    autonomyControls(m),
  );
}

function autonomyControls(m: Metrics): HTMLElement {
  const rate = el("input", {
    type: "number", min: "0", max: "1", step: "0.05",
    value: String(m.auditRate ?? 0),
    title: "Fraction of auto-merges to sample for audit (0–1)",
  }) as HTMLInputElement;

  const mutation = el("input", {
    type: "checkbox",
    title: "Run mutation testing on green branches before auto-merge",
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

  return el("form", {
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
}

function renderAgents(m: Metrics): void {
  const list = document.getElementById("engine-agents");
  if (!list) return;
  clear(list);
  if (m.agents.length === 0) {
    list.append(el("p", { class: "muted" }, ["No agents registered."]));
    return;
  }
  for (const am of m.agents) {
    const busy = am.running > 0;
    list.append(
      el("div", { class: "card agent-activity" }, [
        el("div", { class: "card-main" }, [
          el("div", { class: "card-title" }, [
            am.name,
            el("span", { class: am.enabled ? "pill on" : "pill off" }, [
              am.enabled ? "enabled" : "disabled",
            ]),
            el("span", { class: busy ? "pill busy" : "pill idle" }, [
              busy ? "working" : "idle",
            ]),
          ]),
          el("div", { class: "card-meta" }, [
            el("span", { class: "muted" }, [`load ${am.running} / ${am.concurrency}`]),
            el("span", { class: "muted" }, [`shipped ${am.merged}`]),
            el("span", { class: "muted" }, [
              `kick-back ${am.kickedBack} (${Math.round(am.kickbackRate * 100)}%)`,
            ]),
          ]),
        ]),
      ]),
    );
  }
}

function renderBoard(tasks: Task[], agents: Agent[]): void {
  const board = document.getElementById("engine-board");
  if (!board) return;
  clear(board);

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
  if (document.getElementById("engine-board")) refresh();
}
