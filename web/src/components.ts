import { el } from "./dom.js";

export function button(
  label: string,
  opts?: {
    variant?: "primary" | "danger" | "link";
    onclick?: () => void;
    type?: string;
    disabled?: boolean;
    title?: string;
  },
): HTMLElement {
  const node = el("button", { class: opts?.variant ?? "" }, [label]);
  if (opts?.onclick !== undefined) node.addEventListener("click", opts.onclick);
  if (opts?.type !== undefined) node.setAttribute("type", opts.type);
  if (opts?.disabled !== undefined) (node as HTMLButtonElement).disabled = opts.disabled;
  if (opts?.title !== undefined) node.setAttribute("title", opts.title);
  return node;
}

export function pill(text: string, tone?: string): HTMLElement {
  return el("span", { class: tone ? `pill ${tone}` : "pill" }, [text]);
}

export function tag(text: string, tone?: string): HTMLElement {
  return el("span", { class: tone ? `tag ${tone}` : "tag" }, [text]);
}

export function field(label: string, control: HTMLElement): HTMLElement {
  return el("div", { class: "field" }, [el("label", {}, [label]), control]);
}

export function formatTokens(n: number): string {
  return n.toLocaleString("en-US");
}

export function formatTokensShort(n: number): string {
  return n >= 1000 ? `${Math.round(n / 1000)}k` : String(n);
}
