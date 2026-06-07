import { el } from "./dom.js";

export function brand(projectName: string): HTMLElement {
  return el("div", { class: "brand" }, [projectName]);
}
