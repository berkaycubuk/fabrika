import { el } from "../dom.js";
import type { Transition } from "../types.js";

export function renderTransitionTimeline(transitions: Transition[]): HTMLElement {
  const container = el("div", { class: "transition-timeline" });
  if (transitions.length === 0) {
    container.append(el("div", { class: "muted sm" }, ["No history yet."]));
    return container;
  }
  for (const t of transitions) {
    const parts: string[] = [`${t.fromStatus} → ${t.toStatus}`, `by ${t.actor}`];
    if (t.reason) parts.push(`(${t.reason})`);
    container.append(el("div", { class: "transition-row" }, [parts.join(" ")]));
  }
  return container;
}
