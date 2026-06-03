// Fabrika cockpit shell. The Board surface unifies the human gates (approve /
// decide / accept / audit) into one kanban and seeds new work via Define /
// Create task; the Factory group stays for the registry + observability views.
import { el } from "./dom.js";
import { connectEvents } from "./ws.js";
import { renderAgents, onAgentEvent } from "./views/agents.js";
import { renderTasks, onTaskEvent } from "./views/tasks.js";
import { renderBoard, onBoardEvent } from "./views/board.js";
import type { FabrikaEvent } from "./types.js";

interface Nav {
  id: string;
  label: string;
  group: "you" | "factory";
  render: (root: HTMLElement) => void;
}

const NAV: Nav[] = [
  { id: "board", label: "Board", group: "you", render: renderBoard },
  { id: "tasks", label: "Tasks", group: "factory", render: renderTasks },
  { id: "agents", label: "Agents", group: "factory", render: renderAgents },
];

let current = "board";

function route(content: HTMLElement, id: string): void {
  const nav = NAV.find((n) => n.id === id) ?? NAV[0];
  current = nav.id;
  document.querySelectorAll(".nav-item").forEach((n) => {
    n.classList.toggle("active", (n as HTMLElement).dataset.id === current);
  });
  nav.render(content);
}

function navItem(n: Nav): HTMLElement {
  return el("a", {
    class: "nav-item" + (n.id === current ? " active" : ""),
    "data-id": n.id,
    href: `#${n.id}`,
    onclick: (e: Event) => {
      e.preventDefault();
      location.hash = n.id;
    },
  }, [n.label]);
}

function sidebar(): HTMLElement {
  const group = (label: string, g: Nav["group"]) =>
    el("div", { class: "nav-group" }, [
      el("div", { class: "nav-group-label" }, [label]),
      ...NAV.filter((n) => n.group === g).map(navItem),
    ]);

  return el("aside", { class: "sidebar" }, [
    el("div", { class: "brand" }, ["fabrika"]),
    el("div", { class: "nav-group" }, [NAV.filter((n) => n.group === "you").map(navItem)[0]]),
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

  const go = () => route(content, location.hash.replace("#", "") || "board");
  window.addEventListener("hashchange", go);
  go();

  connectEvents((e: FabrikaEvent) => {
    const conn = document.getElementById("conn");
    if (conn) {
      conn.textContent = "live";
      conn.className = "pill on";
    }
    // Every surface guards on its own DOM presence, so fan out broadly: the
    // board owns the human gates, the factory views own the registry/metrics.
    onBoardEvent();
    if (e.type.startsWith("agent.")) onAgentEvent();
    if (e.type.startsWith("task.") || e.type.startsWith("bigtask.") || e.type.startsWith("plan.")) {
      onTaskEvent();
    }
  });
}

main();
