// Factory: the metrics dashboard + autonomy dials, promoted out of the old
// "Settings" modal into a first-class surface. Throughput and trust numbers
// (SPECS §14) live here, alongside the knobs that widen or narrow autonomy
// (WIP cap, audit rate, mutation testing) and the per-agent share of work.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { button, pill, formatTokens } from "../components.js";
import type { Metrics, AgentMetrics, Convention } from "../types.js";

const BARS = ["var(--accent)", "var(--tan)", "var(--teal)", "var(--green)", "var(--amber)", "var(--red)"];

export function renderFactory(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Factory"]),
      el("p", { class: "muted" }, [
        "Throughput, trust, and the autonomy dials — the numbers that tell you whether to widen or narrow it.",
      ]),
    ]),
    el("div", { id: "factory-body" }, [el("p", { class: "muted" }, ["Loading…"])]),
  );
  refresh();
}

async function refresh(): Promise<void> {
  const body = document.getElementById("factory-body");
  if (!body) return;
  try {
    const m = await api.metrics();
    clear(body);
    const pct = (n: number) => `${Math.round(n * 100)}%`;
    body.append(
      el("div", { class: "section-h sm" }, ["Throughput"]),
      el("div", { class: "stat-grid" }, [
        stat("In flight", String(m.wip), m.wipCap > 0 ? `/ ${m.wipCap}` : undefined),
        stat("Ready", String(m.ready)),
        stat("In review", String(m.inReview)),
        stat("Merged", String(m.merged)),
        stat("Tokens", m.totalTokens ? formatTokens(m.totalTokens) : "—"),
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
      el("div", { id: "factory-conventions" }, [el("div", { class: "muted sm" }, ["Loading conventions…"])]),
    );
    renderConventions();
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
  }, [
    el("label", {}, ["WIP cap"]),
    wip,
    button("Set", { variant: "primary", type: "submit" }),
    el("span", { class: "muted sm" }, ["0 = unlimited"]),
  ]);

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
    button("Set", { variant: "primary", type: "submit" }),
    el("label", { class: "checkbox" }, [mutation, "mutation testing"]),
  ]);

  return el("div", { class: "metrics-bar", style: "margin-top:16px" }, [setWip, setRate]);
}

async function saveSetting(s: Record<string, string>): Promise<void> {
  try {
    await api.putSettings(s);
    refresh();
  } catch (e) {
    alert((e as Error).message);
  }
}

function shareTable(m: Metrics): HTMLElement {
  if (m.agents.length === 0) return el("p", { class: "share-empty" }, ["No agents registered."]);
  // Shipped counts merged code plus planned decompositions, so a pure planner
  // that broke down N big tasks reads as N rather than 0.
  const shipped = (a: AgentMetrics) => a.merged + (a.planned ?? 0);
  const totalMerged = m.agents.reduce((s, a) => s + shipped(a), 0);
  const byLoad = totalMerged === 0;
  // In-flight load counts both running tasks and active planning runs, so the
  // planner doesn't read as idle while it's decomposing a big task.
  const load = (a: AgentMetrics) => a.running + (a.planning ?? 0);
  const weight = (a: AgentMetrics) => (byLoad ? load(a) : shipped(a));
  const total = m.agents.reduce((s, a) => s + weight(a), 0);
  const ranked = [...m.agents].sort((x, y) => weight(y) - weight(x));

  const tbody = el("tbody", {});
  ranked.forEach((a, i) => {
    const w = weight(a);
    const share = total > 0 ? w / total : 0;
    const planning = (a.planning ?? 0) > 0;
    const busy = a.running > 0 || planning;
    const pillLabel = busy ? (a.running > 0 ? "working" : "planning") : "idle";
    tbody.append(
      el("tr", {}, [
        el("td", { class: "who" }, [
          pill(pillLabel, busy ? "busy" : "idle"),
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
        el("td", { class: "num" }, [String(byLoad ? load(a) : shipped(a))]),
        el("td", { class: "num" }, [(a.totalTokens ?? 0) > 0 ? formatTokens(a.totalTokens as number) : "—"]),
      ]),
    );
  });
  return el("table", { class: "share-table" }, [
    el("thead", {}, [
      el("tr", {}, [
        el("th", {}, ["Agent"]),
        el("th", {}, [byLoad ? "Share of load" : "Share of work"]),
        el("th", { class: "num" }, [byLoad ? "In flight" : "Shipped"]),
        el("th", { class: "num" }, ["Tokens"]),
      ]),
    ]),
    tbody,
  ]);
}

async function renderConventions(): Promise<void> {
  const slot = document.getElementById("factory-conventions");
  if (!slot) return;
  try {
    const cs: Convention[] = await api.listConventions("proposed");
    clear(slot);
    slot.append(el("div", { class: "section-h sm" }, ["Proposed conventions"]));
    if (cs.length === 0) {
      slot.append(el("p", { class: "muted sm" }, ["No proposed conventions."]));
      return;
    }
    for (const c of cs) {
      const self = el("div", { class: "convention-item" }, [
        el("div", { class: "convention-rule" }, [c.rule]),
        el("div", { class: "card-actions" }, [
          button("Approve", { variant: "primary", onclick: () => {
            api.approveConvention(c.id).then(renderConventions).catch((e: unknown) => alert((e as Error).message));
          }}),
          button("Reject", { variant: "danger", onclick: () => {
            api.rejectConvention(c.id).then(renderConventions).catch((e: unknown) => alert((e as Error).message));
          }}),
        ]),
      ]);
      slot.append(self);
    }
  } catch (e) {
    clear(slot);
    slot.append(el("p", { class: "form-error" }, [(e as Error).message]));
  }
}

function stat(label: string, value: string, unit?: string): HTMLElement {
  const v = el("div", { class: "stat-value" }, [value]);
  if (unit) v.append(el("span", { class: "unit" }, [` ${unit}`]));
  return el("div", { class: "stat" }, [el("div", { class: "stat-label" }, [label]), v]);
}

// onFactoryEvent refreshes the dashboard when any event arrives while the view
// is mounted. Bursts coalesce behind a short debounce (a task churning through
// stages emits several events back-to-back).
let refreshTimer: ReturnType<typeof setTimeout> | null = null;

export function onFactoryEvent(): void {
  if (!document.getElementById("factory-body")) return;
  if (refreshTimer !== null) clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => {
    refreshTimer = null;
    refresh();
  }, 300);
}
