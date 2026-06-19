// Tasks view: unified list of all tasks across the factory.
import { el, clear } from "../dom.js";
import { button } from "../components.js";

export function renderTasks(root: HTMLElement): void {
  clear(root);
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
      }),
      el("div", { class: "header-actions" }, [
        button("Create task", { onclick: () => {} }),
        button("Define big task", { variant: "primary", onclick: () => {} }),
      ]),
    ]),
  );
}
