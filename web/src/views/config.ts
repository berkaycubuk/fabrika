import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { button, field } from "../components.js";
import { renderRelaySection } from "./relay.js";
import type { ConfigManifest } from "../types.js";

const VERB_KEYS = ["setup", "build", "test", "lint", "typecheck", "verify", "e2e", "run"] as const;

function toToml(m: ConfigManifest): string {
  const lines: string[] = [];
  lines.push("[project]");
  lines.push(`name = "${m.project.name}"`);
  lines.push("");
  lines.push("[verbs]");
  for (const k of VERB_KEYS) {
    const v = m.verbs[k];
    lines.push(`${k} = ${v ? `"${v.replace(/"/g, '\\"')}"` : '""'}`);
  }
  lines.push("");
  lines.push("[risk]");
  lines.push(`high = [${(m.risk.high ?? []).map((s) => `"${s}"`).join(", ")}]`);
  lines.push(`medium = [${(m.risk.medium ?? []).map((s) => `"${s}"`).join(", ")}]`);
  lines.push("");
  lines.push("[autonomy]");
  lines.push(`auto_merge = [${(m.autonomy.auto_merge ?? []).map((s) => `"${s}"`).join(", ")}]`);
  lines.push(`escalate = [${(m.autonomy.escalate ?? []).map((s) => `"${s}"`).join(", ")}]`);
  return lines.join("\n") + "\n";
}

function fromToml(s: string): ConfigManifest {
  const m: ConfigManifest = {
    project: { name: "" },
    verbs: Object.fromEntries(VERB_KEYS.map((k) => [k, ""])) as ConfigManifest["verbs"],
    risk: {},
    autonomy: {},
  };

  let section = "";
  for (const line of s.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;

    const secMatch = trimmed.match(/^\[(\w+)\]$/);
    if (secMatch) {
      section = secMatch[1];
      continue;
    }

    const kvMatch = trimmed.match(/^(\w+)\s*=\s*(.+)$/);
    if (!kvMatch) continue;
    const key = kvMatch[1];
    const rawVal = kvMatch[2].trim();

    if (rawVal.startsWith("[")) {
      const inner = rawVal.slice(1, -1).trim();
      const arr = inner
        ? inner.split(",").map((s) => s.trim().replace(/^"(.*)"$/, "$1")).filter(Boolean)
        : [];
      if (section === "risk") {
        if (key === "high") m.risk.high = arr;
        else if (key === "medium") m.risk.medium = arr;
      } else if (section === "autonomy") {
        if (key === "auto_merge") m.autonomy.auto_merge = arr;
        else if (key === "escalate") m.autonomy.escalate = arr;
      }
    } else {
      const val = rawVal.replace(/^"(.*)"$/, "$1").replace(/\\"/g, '"');
      if (section === "project") {
        if (key === "name") m.project.name = val;
      } else if (section === "verbs") {
        if (VERB_KEYS.includes(key as typeof VERB_KEYS[number]) && val) {
          m.verbs[key as typeof VERB_KEYS[number]] = val;
        }
      }
    }
  }

  return m;
}

export function renderConfig(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Settings"]),
      el("p", { class: "muted" }, [
        "Edit the project manifest (fabrika.toml). Write raw TOML — the editor round-trips through the API on save.",
      ]),
    ]),
    el("div", { id: "config-editor" }, [el("p", { class: "muted" }, ["Loading…"])]),
  );
  load();
  renderRelaySection(root);
}

async function load(): Promise<void> {
  const slot = document.getElementById("config-editor");
  if (!slot) return;
  try {
    const m = await api.getConfig();
    renderEditor(slot, m);
  } catch (e) {
    slot.textContent = (e as Error).message;
  }
}

function renderEditor(slot: HTMLElement, m: ConfigManifest): void {
  const textarea = el("textarea", {
    class: "config-toml",
    rows: "24",
  }) as HTMLTextAreaElement;
  textarea.value = toToml(m);

  const err = el("div", { class: "form-error" });
  const saveBtn = button("Save changes", { variant: "primary", type: "submit" });

  const form = el("form", {
    class: "modal-form",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      err.textContent = "";
      try {
        const parsed = fromToml(textarea.value);
        await api.putConfig(parsed);
        err.textContent = "Saved.";
        err.classList.add("form-ok");
      } catch (e2) {
        err.classList.remove("form-ok");
        err.textContent = (e2 as Error).message;
      }
    },
  }, [
    field("fabrika.toml", textarea),
    err,
    el("div", { class: "form-actions" }, [saveBtn]),
  ]);

  clear(slot);
  slot.append(form);
}
