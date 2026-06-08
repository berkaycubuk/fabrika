import { el } from "../dom.js";

export function ciBadge(t: { ciStatus?: string; ciRunUrl?: string }): HTMLElement | null {
  if (t.ciStatus !== "failure") return null;
  return el("a", {
    href: t.ciRunUrl || "",
    target: "_blank",
    rel: "noopener",
    class: "ci-fail",
  }, ["CI ✗"]);
}
