// Tiny DOM helper — keeps the vanilla-TS views readable without a framework.

type Attrs = Record<string, string | number | boolean | EventListener | undefined>;

export function el(
  tag: string,
  attrs: Attrs = {},
  children: (Node | string)[] = [],
): HTMLElement {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === false) continue;
    if (k.startsWith("on") && typeof v === "function") {
      node.addEventListener(k.slice(2).toLowerCase(), v as EventListener);
    } else if (k === "class") {
      node.className = String(v);
    } else if (k === "value") {
      (node as HTMLInputElement).value = String(v);
    } else {
      node.setAttribute(k, String(v));
    }
  }
  for (const c of children) {
    node.append(c instanceof Node ? c : document.createTextNode(c));
  }
  return node;
}

export function clear(node: HTMLElement): void {
  node.replaceChildren();
}
