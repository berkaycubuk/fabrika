// Fabrika cockpit shell. The Board surface unifies the human gates (approve /
// decide / accept / audit) into one kanban and seeds new work via Define /
// Create task; Agents exposes the registry + observability views.
import { el } from "./dom.js";
import { brand } from "./brand.js";
import { initTheme, themeToggle } from "./theme.js";
import { connectEvents } from "./ws.js";
import { renderAgents, onAgentEvent } from "./views/agents.js";
import { renderBoard, onBoardEvent, onHeartbeat } from "./views/board.js";
import { renderFactory, onFactoryEvent } from "./views/factory.js";
import { renderConfig } from "./views/config.js";
import { renderCrons } from "./views/crons.js";
import { renderTasks, onTasksEvent } from "./views/tasks.js";
import type { FabrikaEvent, Heartbeat } from "./types.js";

interface Nav {
  id: string;
  label: string;
  render: (root: HTMLElement) => void;
}

const NAV: Nav[] = [
  { id: "board", label: "Board", render: renderBoard },
  { id: "tasks", label: "Tasks", render: renderTasks },
  { id: "factory", label: "Factory", render: renderFactory },
  { id: "agents", label: "Agents", render: renderAgents },
  { id: "crons", label: "Schedules", render: renderCrons },
  { id: "settings", label: "Settings", render: renderConfig },
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
  const versionEl = el("span", { id: "build-version", class: "build-version" }, ["…"]);
  const brandEl = brand("");
  fetch("/api/version")
    .then((r) => r.json())
    .then((d: { version: string; project?: string }) => {
      versionEl.textContent = d.version;
      if (d.project) {
        const newBrand = brand(d.project);
        brandEl.replaceWith(newBrand);
        document.title = `fabrika — ${d.project}`;
      }
    })
    .catch(() => { versionEl.textContent = ""; });
  return el("aside", { class: "sidebar" }, [
    brandEl,
    el("div", { class: "nav-group" }, NAV.map(navItem)),
    el("div", { class: "conn-row" }, [
      el("span", { id: "conn", class: "pill soon" }, ["connecting"]),
      themeToggle(),
    ]),
    el("a", { class: "nav-item", href: "https://fabrika-ai.com/docs/", target: "_blank", rel: "noopener" }, ["Docs"]),
    versionEl,
  ]);
}

// Reflects the live-event socket state in the sidebar pill.
function setConn(state: "live" | "reconnecting"): void {
  const conn = document.getElementById("conn");
  if (!conn) return;
  if (state === "live") {
    conn.textContent = "live";
    conn.className = "pill on";
  } else {
    conn.textContent = "reconnecting";
    conn.className = "pill soon";
  }
}

function main(): void {
  initTheme();
  const app = document.getElementById("app")!;
  const content = el("main", { class: "content" });
  app.append(el("div", { class: "layout" }, [sidebar(), content]));

  const go = () => route(content, location.hash.replace("#", "") || "board");
  window.addEventListener("hashchange", go);
  go();

  connectEvents((e: FabrikaEvent) => {
    setConn("live");
    // Heartbeats are high-frequency liveness pulses: update the running card in
    // place and stop — fanning them out to a full board refetch would hammer the
    // API every few seconds per in-flight task.
    if (e.type === "task.heartbeat") {
      onHeartbeat(e.payload as Heartbeat);
      return;
    }
    // Every surface guards on its own DOM presence, so fan out broadly: the
    // board owns the human gates (refreshing on every event, including
    // task/plan), the factory views own the registry/metrics.
    onBoardEvent(e);
    onFactoryEvent();
    onTasksEvent();
    if (e.type.startsWith("agent.")) onAgentEvent();
  }, {
    // Link is up — reflect it immediately, even before any event arrives.
    onConnect: () => setConn("live"),
    // The socket dropped and silently missed events; pull a full snapshot so
    // the board doesn't sit stale until a manual reload.
    onReconnect: () => {
      setConn("live");
      onBoardEvent();
      onFactoryEvent();
      onTasksEvent();
      onAgentEvent();
    },
    onDisconnect: () => setConn("reconnecting"),
  });
}

main();
