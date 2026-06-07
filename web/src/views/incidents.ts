// Incidents screen: list and triage production errors (SPECS-PHASE4 §7).
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { button, pill, tag } from "../components.js";
import type { Incident } from "../types.js";

let refreshTimer: ReturnType<typeof setTimeout> | null = null;
const DEBOUNCE_MS = 150;

export function renderIncidents(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Incidents"]),
      el("p", { class: "muted" }, ["Production errors, deduplicated and triaged into fixing tasks."]),
    ]),
    el("div", { id: "incident-list", class: "card-list" }, ["Loading…"]),
  );
  refresh();
}

async function refresh(): Promise<void> {
  const list = document.getElementById("incident-list");
  if (!list) return;
  try {
    const incidents = await api.listIncidents();
    clear(list);
    if (incidents.length === 0) {
      list.append(el("p", { class: "muted" }, ["No incidents."]));
      return;
    }
    for (const inc of incidents) list.append(incidentCard(inc));
  } catch (e) {
    list.textContent = (e as Error).message;
  }
}

function incidentCard(inc: Incident): HTMLElement {
  const meta: (Node | string)[] = [
    pill(inc.status, statusPillClass(inc.status)),
    tag(`×${inc.count}`),
    el("span", { class: "muted sm" }, [`first: ${fmtDate(inc.firstSeen)} · last: ${fmtDate(inc.lastSeen)}`]),
  ];
  if (inc.suspectTaskId) {
    meta.push(tag(`suspect: ${inc.suspectTaskId.slice(0, 6)}`, "dep"));
  }
  if (inc.suspectReleaseId) {
    meta.push(tag(`release: ${inc.suspectReleaseId.slice(0, 6)}`));
  }

  const actions: (Node | string)[] = [];
  if (inc.taskId) {
    actions.push(el("a", { href: "#board", class: "tag" }, [`fix task: ${inc.taskId.slice(0, 6)}`]));
  }
  if (inc.status === "open" || inc.status === "fixing") {
    actions.push(
      button("Ignore", {
        onclick: async () => {
          await api.ignoreIncident(inc.id);
          void refresh();
        },
      }),
      button("Resolve", {
        onclick: async () => {
          await api.resolveIncident(inc.id);
          void refresh();
        },
      }),
    );
  }

  return el("div", { class: `card incident-card` }, [
    el("div", { class: "card-main" }, [
      el("div", { class: "card-title" }, [
        inc.title,
        pill(inc.status, statusPillClass(inc.status)),
      ]),
      el("div", { class: "card-meta" }, meta),
    ]),
    actions.length ? el("div", { class: "card-actions" }, actions) : el("span", {}),
  ]);
}

function statusPillClass(status: string): string {
  switch (status) {
    case "open": return "incident-open";
    case "fixing": return "soon";
    case "resolved": return "on";
    case "ignored": return "off";
    default: return "off";
  }
}

function fmtDate(s: string): string {
  if (!s) return "—";
  try {
    return new Date(s).toLocaleString(undefined, { dateStyle: "short", timeStyle: "short" });
  } catch {
    return s;
  }
}

export function onIncidentEvent(): void {
  if (!document.getElementById("incident-list")) return;
  if (refreshTimer !== null) clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => {
    refreshTimer = null;
    void refresh();
  }, DEBOUNCE_MS);
}
