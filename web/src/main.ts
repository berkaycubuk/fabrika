// Fabrika cockpit shell. Phase 0 surfaces Tasks + Agents live; the remaining
// surfaces (Define/Approve/Decide/Accept/Engine room) are placeholders wired to
// the same nav so the layout is real before their backends land (SPECS.md §10).
import { el, clear } from "./dom.js";
import { connectEvents } from "./ws.js";
import { renderAgents, onAgentEvent } from "./views/agents.js";
import { renderTasks, onTaskEvent } from "./views/tasks.js";
import { renderAccept, onReviewEvent } from "./views/accept.js";
import { api } from "./api.js";
import type { FabrikaEvent } from "./types.js";

interface Nav {
  id: string;
  label: string;
  group: "needs-you" | "factory";
  render: (root: HTMLElement) => void;
  soon?: boolean;
}

const NAV: Nav[] = [
  { id: "tasks", label: "Tasks", group: "factory", render: renderTasks },
  { id: "agents", label: "Agents", group: "factory", render: renderAgents },
  { id: "accept", label: "Accept", group: "needs-you", render: renderAccept },
  { id: "decide", label: "Decide", group: "needs-you", render: placeholder("Decide", "The decision queue — answer questions agents can't resolve. Arrives with the planner (Phase 2)."), soon: true },
  { id: "approve", label: "Approve", group: "needs-you", render: placeholder("Approve", "Review a proposed plan before work starts. Arrives with the planner (Phase 2)."), soon: true },
  { id: "engine", label: "Engine room", group: "factory", render: placeholder("Engine room", "Live task DAG, per-agent activity, and metrics. Arrives with the scheduler (Phase 1)."), soon: true },
];

function placeholder(title: string, body: string) {
  return (root: HTMLElement) => {
    clear(root);
    root.append(
      el("div", { class: "view-header" }, [el("h1", {}, [title])]),
      el("div", { class: "card placeholder" }, [
        el("span", { class: "pill soon" }, ["coming soon"]),
        el("p", {}, [body]),
      ]),
    );
  };
}

let current = "tasks";

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
      n.id === "accept" ? el("span", { class: "count" }, []) : el("span", {}),
      n.soon ? el("span", { class: "dot" }, []) : el("span", {}),
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

  const go = () => route(content, location.hash.replace("#", "") || "tasks");
  window.addEventListener("hashchange", go);
  go();

  updateBadge();
  connectEvents((e: FabrikaEvent) => {
    const conn = document.getElementById("conn");
    if (conn) {
      conn.textContent = "live";
      conn.className = "pill on";
    }
    if (e.type.startsWith("agent.")) onAgentEvent();
    if (e.type.startsWith("task.") || e.type.startsWith("bigtask.")) {
      onTaskEvent();
      onReviewEvent();
      updateBadge();
    }
  });
}

// updateBadge shows the count of items awaiting acceptance on the Accept nav.
async function updateBadge(): Promise<void> {
  try {
    const items = await api.listReviews();
    const nav = document.querySelector('.nav-item[data-id="accept"] .count');
    if (nav) nav.textContent = items.length > 0 ? String(items.length) : "";
  } catch {
    /* ignore */
  }
}

main();
