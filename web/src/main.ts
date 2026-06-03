// Fabrika cockpit shell. The Board surface unifies the human gates (approve /
// decide / accept / audit) into one kanban and seeds new work via Define /
// Create task; Agents exposes the registry + observability views.
import { el } from "./dom.js";
import { connectEvents } from "./ws.js";
import { renderAgents, onAgentEvent } from "./views/agents.js";
import { renderBoard, onBoardEvent } from "./views/board.js";
import type { FabrikaEvent } from "./types.js";

interface Nav {
  id: string;
  label: string;
  render: (root: HTMLElement) => void;
}

const NAV: Nav[] = [
  { id: "board", label: "Board", render: renderBoard },
  { id: "agents", label: "Agents", render: renderAgents },
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
  return el("aside", { class: "sidebar" }, [
    el("div", { class: "brand" }, ["fabrika"]),
    el("div", { class: "nav-group" }, NAV.map(navItem)),
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
    // board owns the human gates (refreshing on every event, including
    // task/plan), the factory views own the registry/metrics.
    onBoardEvent();
    if (e.type.startsWith("agent.")) onAgentEvent();
  });
}

main();
