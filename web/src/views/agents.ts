// Agents screen: create/edit/enable/disable the registered agent pool
// (SPECS.md §7, §10). Persisted in the global store, reusable across repos.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { button, pill, tag, field } from "../components.js";
import { ROLES, type Agent } from "../types.js";
import { DEFAULT_AVATAR } from "../avatar.js";
import { openModal, closeModal } from "../ui.js";
import { AGENT_KINDS, type AgentKind } from "../agentKinds.js";

const kindFor = (command: string) => AGENT_KINDS.find((k) => k.command === command);

// Routing priority levels. Higher number = higher routing priority; 0 = normal.
// A higher-priority agent is chosen even if a lower-priority agent's tags match.
const PRIORITY_OPTIONS = [
  { value: -1, label: "Low" },
  { value: 0, label: "Normal" },
  { value: 1, label: "High" },
];
const priorityLabel = (p: number) => PRIORITY_OPTIONS.find((o) => o.value === p)?.label ?? "Normal";

// First model in a kind's catalog is the recommended default. Falls back to ""
// for a back-compat kind that lists no models (command without a {model} token).
const defaultModel = (kind: AgentKind) => kind.models[0]?.id ?? "";

const labelForModel = (kind: AgentKind | undefined, id: string) =>
  kind?.models.find((m) => m.id === id)?.label ?? id;

export function renderAgents(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Agents"]),
      el("p", { class: "muted" }, [
        "Registered workers, reusable across repos. Pick the local coding agent to run — Fabrika handles the rest.",
      ]),
    ]),
    el("div", { class: "form-actions" }, [
      button("Add agent", { variant: "primary", onclick: () => openAgentModal() }),
    ]),
    el("div", { id: "agent-list", class: "card-list" }, ["Loading…"]),
  );
  refresh();
}

function openAgentModal(agent?: Agent): void {
  let editingId: string | null = agent?.id ?? null;

  const name = el("input", { placeholder: "Name (e.g. Claude Code)" }) as HTMLInputElement;
  const kind = el(
    "select",
    {},
    AGENT_KINDS.map((k) => el("option", { value: k.id }, [k.label])),
  ) as HTMLSelectElement;
  const model = el("select", {}) as HTMLSelectElement;

  const populateModels = (kindId: string, selected?: string) => {
    const k = AGENT_KINDS.find((x) => x.id === kindId) ?? AGENT_KINDS[0];
    clear(model);
    model.append(...k.models.map((m) => el("option", { value: m.id }, [m.label])));
    model.value = selected && k.models.some((m) => m.id === selected) ? selected : defaultModel(k);
  };
  kind.addEventListener("change", () => populateModels(kind.value));
  populateModels(kind.value);

  const tags = el("input", { placeholder: "Tags, comma-separated (go, frontend)" }) as HTMLInputElement;
  const concurrency = el("input", { type: "number", value: "1", min: "1" }) as HTMLInputElement;
  const timeout = el("input", { placeholder: "20m", value: "20m" }) as HTMLInputElement;
  const maxAttempts = el("input", { type: "number", value: "3", min: "1" }) as HTMLInputElement;
  const priority = el(
    "select",
    {},
    PRIORITY_OPTIONS.map((p) => el("option", { value: String(p.value) }, [p.label])),
  ) as HTMLSelectElement;
  priority.value = "0";

  const roleBoxes = ROLES.map((r) => {
    const cb = el("input", { type: "checkbox", value: r }) as HTMLInputElement;
    if (r === "implementer") cb.checked = true;
    return { role: r, cb };
  });

  const photoInput = el("input", { type: "file", accept: "image/*", class: "attach-file" }) as HTMLInputElement;
  const photoPreview = el("img", { class: "avatar-preview", src: DEFAULT_AVATAR, alt: "" }) as HTMLImageElement;
  const photoBtn = el("button", { type: "button", onclick: () => photoInput.click() }, ["Choose photo…"]);
  const photoHint = el("span", { class: "muted sm" }, ["Optional — default avatar otherwise."]);
  let photoDataUrl = "";

  photoInput.addEventListener("change", () => {
    const file = photoInput.files?.[0];
    if (!file) { photoPreview.src = DEFAULT_AVATAR; photoDataUrl = ""; return; }
    if (!file.type.startsWith("image/")) {
      err.textContent = "File must be an image.";
      photoInput.value = "";
      photoDataUrl = "";
      return;
    }
    if (file.size > 2 * 1024 * 1024) {
      err.textContent = "Image must be under 2 MiB.";
      photoInput.value = "";
      photoDataUrl = "";
      return;
    }
    err.textContent = "";
    const reader = new FileReader();
    reader.onload = () => {
      photoDataUrl = reader.result as string;
      photoPreview.src = photoDataUrl;
      photoHint.textContent = file.name;
    };
    reader.readAsDataURL(file);
  });

  const err = el("div", { class: "form-error" });
  const submitBtn = button(agent ? "Save changes" : "Add agent", { variant: "primary", type: "submit" });

  const form = el("form", {
    class: "modal-form",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      err.textContent = "";
      const selectedKind = AGENT_KINDS.find((k) => k.id === kind.value) ?? AGENT_KINDS[0];
      const payload: Partial<Agent> = {
        name: name.value.trim() || selectedKind.label,
        command: selectedKind.command,
        model: model.value,
        roles: roleBoxes.filter((b) => b.cb.checked).map((b) => b.role),
        tags: tags.value.split(",").map((s) => s.trim()).filter(Boolean),
        concurrency: parseInt(concurrency.value, 10) || 1,
        timeout: timeout.value.trim(),
        maxAttempts: parseInt(maxAttempts.value, 10) || 1,
        priority: parseInt(priority.value, 10) || 0,
        enabled: true,
        photo: photoDataUrl,
      };
      try {
        if (editingId) {
          await api.updateAgent(editingId, payload);
        } else {
          await api.createAgent(payload);
        }
        closeModal();
        refresh();
      } catch (e2) {
        err.textContent = (e2 as Error).message;
      }
    },
  }, [
    field("Name", name),
    field("Agent", kind),
    field("Model", model),
    el("div", { class: "field-row" }, [
      field("Tags", tags),
      field("Concurrency", concurrency),
      field("Timeout", timeout),
      field("Max attempts", maxAttempts),
      field("Priority", priority),
    ]),
    field("Roles", el("div", { class: "checkbox-row" }, roleBoxes.flatMap((b) => [
      el("label", { class: "checkbox" }, [b.cb, b.role]),
    ]))),
    field("Photo", el("div", { class: "photo-row" }, [photoPreview, photoBtn, photoHint, photoInput])),
    err,
    el("div", { class: "form-actions" }, [submitBtn]),
  ]) as HTMLFormElement;

  // Pre-populate when editing.
  if (agent) {
    name.value = agent.name;
    kind.value = (kindFor(agent.command) ?? AGENT_KINDS[0]).id;
    populateModels(kind.value, agent.model);
    tags.value = agent.tags?.join(", ") ?? "";
    concurrency.value = String(agent.concurrency);
    timeout.value = agent.timeout;
    maxAttempts.value = String(agent.maxAttempts || 1);
    priority.value = String(agent.priority ?? 0);
    roleBoxes.forEach((b) => (b.cb.checked = agent.roles?.includes(b.role) ?? false));
    photoDataUrl = agent.photo || "";
    photoPreview.src = photoDataUrl || DEFAULT_AVATAR;
    photoHint.textContent = photoDataUrl ? "Current photo." : "Optional — default avatar otherwise.";
  }

  openModal(agent ? "Edit agent" : "Add agent", form);
}

async function refresh(): Promise<void> {
  const list = document.getElementById("agent-list");
  if (!list) return;
  try {
    const agents = await api.listAgents();
    clear(list);
    if (agents.length === 0) {
      list.append(el("p", { class: "muted" }, ["No agents yet. Add one above."]));
      return;
    }
    for (const a of agents) list.append(agentCard(a));
  } catch (e) {
    list.textContent = (e as Error).message;
  }
}

function agentCard(a: Agent): HTMLElement {
  return el("div", { class: "card agent-card" }, [
    el("div", { class: "card-main" }, [
      el("img", { class: "agent-avatar", src: a.photo || DEFAULT_AVATAR, alt: "" }),
      el("div", { class: "card-title" }, [
        a.name,
        pill(a.enabled ? "enabled" : "disabled", a.enabled ? "on" : "off"),
      ]),
      el("code", { class: "card-cmd" }, [kindFor(a.command)?.label ?? a.command]),
      el("div", { class: "card-meta" }, [
        ...(a.model ? [tag(labelForModel(kindFor(a.command), a.model), "model")] : []),
        ...(a.roles ?? []).map((r) => tag(r, "role")),
        ...(a.tags ?? []).map((t) => tag(t)),
        el("span", { class: "muted" }, [`×${a.concurrency} · ${a.timeout || "no timeout"} · priority: ${priorityLabel(a.priority ?? 0)}`]),
      ]),
    ]),
    el("div", { class: "card-actions" }, [
      button("Edit", { onclick: () => openAgentModal(a) }),
      button(a.enabled ? "Disable" : "Enable", {
        onclick: async () => {
          a.enabled ? await api.disableAgent(a.id) : await api.enableAgent(a.id);
          refresh();
        },
      }),
      button("Delete", {
        variant: "danger",
        onclick: async () => {
          if (confirm(`Delete agent "${a.name}"?`)) {
            await api.deleteAgent(a.id);
            refresh();
          }
        },
      }),
    ]),
  ]);
}

export function onAgentEvent(): void {
  if (document.getElementById("agent-list")) refresh();
}
