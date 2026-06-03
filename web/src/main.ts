// Fabrika cockpit shell. Phase 0 surfaces Tasks + Agents live; the remaining
// surfaces (Define/Approve/Decide/Accept/Engine room) are placeholders wired to
// the same nav so the layout is real before their backends land (SPECS.md §10).
import { el } from "./dom.js";
import { connectEvents } from "./ws.js";
import { renderAgents, onAgentEvent } from "./views/agents.js";
import { renderTasks, onTaskEvent } from "./views/tasks.js";
import { renderAccept, onReviewEvent } from "./views/accept.js";
import { renderAudit, onAuditEvent } from "./views/audit.js";
import { renderEngine, onEngineEvent } from "./views/engine.js";
import { renderDefine } from "./views/define.js";
import { renderApprove, onPlanEvent } from "./views/approve.js";
import { renderDecide, onDecisionEvent } from "./views/decide.js";
import { api } from "./api.js";
import type { FabrikaEvent } from "./types.js";

interface Nav {
  id: string;
  label: string;
  group: "needs-you" | "factory";
  render: (root: HTMLElement) => void;
}

const NAV: Nav[] = [
  { id: "define", label: "Define", group: "needs-you", render: renderDefine },
  { id: "approve", label: "Approve", group: "needs-you", render: renderApprove },
  { id: "decide", label: "Decide", group: "needs-you", render: renderDecide },
  { id: "accept", label: "Accept", group: "needs-you", render: renderAccept },
  { id: "audit", label: "Audit", group: "needs-you", render: renderAudit },
  { id: "tasks", label: "Tasks", group: "factory", render: renderTasks },
  { id: "agents", label: "Agents", group: "factory", render: renderAgents },
  { id: "engine", label: "Engine room", group: "factory", render: renderEngine },
];

let current = "define";

function route(content: HTMLElement, id: string): void {
  const nav = NAV.find((n) => n.id === id) ?? NAV[0];
  current = nav.id;
  document.querySelectorAll(".nav-item").forEach((n) => {
    n.classList.toggle("active", (n as HTMLElement).dataset.id === current);
  });
  nav.render(content);
}

function sidebar(): HTMLElement {
  const item = (n: Nav) =>
    el("a", {
      class: "nav-item" + (n.id === current ? " active" : ""),
      "data-id": n.id,
      href: `#${n.id}`,
      onclick: (e: Event) => {
        e.preventDefault();
        location.hash = n.id;
      },
    }, [
      n.label,
      ["accept", "approve", "decide", "audit"].includes(n.id)
        ? el("span", { class: "count", "data-badge": n.id }, [])
        : el("span", {}),
    ]);

  const group = (label: string, g: Nav["group"]) =>
    el("div", { class: "nav-group" }, [
      el("div", { class: "nav-group-label" }, [label]),
      ...NAV.filter((n) => n.group === g).map(item),
    ]);

  return el("aside", { class: "sidebar" }, [
    el("div", { class: "brand" }, ["fabrika"]),
    group("Needs you", "needs-you"),
    group("Factory", "factory"),
    el("div", { class: "sidebar-foot" }, [
      el("span", { id: "conn", class: "pill off" }, ["connecting…"]),
    ]),
  ]);
}

function main(): void {
  const app = document.getElementById("app")!;
  const content = el("main", { class: "content" });
  app.append(el("div", { class: "layout" }, [sidebar(), content]));

  const go = () => route(content, location.hash.replace("#", "") || "define");
  window.addEventListener("hashchange", go);
  go();

  updateBadges();
  connectEvents((e: FabrikaEvent) => {
    const conn = document.getElementById("conn");
    if (conn) {
      conn.textContent = "live";
      conn.className = "pill on";
    }
    if (e.type.startsWith("agent.")) {
      onAgentEvent();
      onEngineEvent();
    }
    if (e.type.startsWith("plan.")) {
      onPlanEvent();
      onTaskEvent();
      onEngineEvent();
    }
    if (e.type.startsWith("decision.") || e.type.startsWith("convention.")) {
      onDecisionEvent();
    }
    if (e.type.startsWith("task.") || e.type.startsWith("bigtask.")) {
      onTaskEvent();
      onReviewEvent();
      onAuditEvent();
      onPlanEvent();
      onDecisionEvent();
      onEngineEvent();
    }
    updateBadges();
  });
}

// updateBadges refreshes the counts on the Needs-you nav items (work waiting).
async function updateBadges(): Promise<void> {
  const set = (id: string, n: number) => {
    const nav = document.querySelector(`.nav-item .count[data-badge="${id}"]`);
    if (nav) nav.textContent = n > 0 ? String(n) : "";
  };
  try {
    const [reviews, plans, decisions, audits] = await Promise.all([
      api.listReviews(),
      api.listPlans(),
      api.listDecisions(),
      api.listAudits(),
    ]);
    set("accept", reviews.length);
    set("approve", plans.filter((p) => p.status === "proposed").length);
    set("decide", decisions.length);
    set("audit", audits.length);
  } catch {
    /* ignore */
  }
}

main();
