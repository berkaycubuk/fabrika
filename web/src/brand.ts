import { el } from "./dom.js";

export function brand(projectName: string): HTMLElement {
  if (projectName) {
    return el("div", { class: "brand" }, [
      el("span", { class: "brand-project" }, [projectName]),
      el("span", { class: "brand-label" }, ["fabrika"]),
    ]);
  }
  return el("div", { class: "brand" }, ["fabrika"]);
}
