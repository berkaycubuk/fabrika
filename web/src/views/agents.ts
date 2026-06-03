// Agents screen: create/edit/enable/disable the registered agent pool
// (SPECS.md §7, §10). Persisted in the global store, reusable across repos.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { ROLES, type Agent } from "../types.js";

// Supported local coding agents. Picking one wires the invocation for you — the
// run command is fixed per kind (Fabrika runs it inside the task's worktree and
// passes the rendered prompt as {prompt_file}). The same command serves the
// implementer/planner/reviewer roles.
const AGENT_KINDS = [
  { id: "claude-code", label: "Claude Code", command: `claude -p "$(cat {prompt_file})" --dangerously-skip-permissions` },
  { id: "opencode", label: "OpenCode", command: `opencode run "$(cat {prompt_file})"` },
  { id: "pi", label: "Pi", command: `pi "$(cat {prompt_file})"` },
];

const kindFor = (command: string) => AGENT_KINDS.find((k) => k.command === command);

export function renderAgents(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Agents"]),
      el("p", { class: "muted" }, [
        "Registered workers, reusable across repos. Pick the local coding agent to run — Fabrika handles the rest.",
      ]),
    ]),
    agentForm(root),
    el("div", { id: "agent-list", class: "card-list" }, ["Loading…"]),
  );
  refresh();
}

let editingId: string | null = null;

function agentForm(root: HTMLElement): HTMLElement {
  const name = el("input", { placeholder: "Name (e.g. Claude Code)" }) as HTMLInputElement;
  const kind = el(
    "select",
    {},
    AGENT_KINDS.map((k) => el("option", { value: k.id }, [k.label])),
  ) as HTMLSelectElement;
  const tags = el("input", { placeholder: "Tags, comma-separated (go, frontend)" }) as HTMLInputElement;
  const concurrency = el("input", { type: "number", value: "1", min: "1" }) as HTMLInputElement;
  const timeout = el("input", { placeholder: "20m", value: "20m" }) as HTMLInputElement;

  const roleBoxes = ROLES.map((r) => {
    const cb = el("input", { type: "checkbox", value: r }) as HTMLInputElement;
    if (r === "implementer") cb.checked = true;
    return { role: r, cb };
  });

  const err = el("div", { class: "form-error" });
  const submitBtn = el("button", { class: "primary", type: "submit" }, ["Add agent"]);

  const form = el("form", {
    class: "agent-form card",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      err.textContent = "";
      const selectedKind = AGENT_KINDS.find((k) => k.id === kind.value) ?? AGENT_KINDS[0];
      const payload: Partial<Agent> = {
        name: name.value.trim() || selectedKind.label,
        command: selectedKind.command,
        roles: roleBoxes.filter((b) => b.cb.checked).map((b) => b.role),
        tags: tags.value.split(",").map((s) => s.trim()).filter(Boolean),
        concurrency: parseInt(concurrency.value, 10) || 1,
        timeout: timeout.value.trim(),
        maxAttempts: 1,
        enabled: true,
      };
      try {
        if (editingId) {
          await api.updateAgent(editingId, payload);
        } else {
          await api.createAgent(payload);
        }
        resetForm();
        refresh();
      } catch (e2) {
        err.textContent = (e2 as Error).message;
      }
    },
  }, [
    el("div", { class: "field" }, [el("label", {}, ["Name"]), name]),
    el("div", { class: "field" }, [el("label", {}, ["Agent"]), kind]),
    el("div", { class: "field-row" }, [
      el("div", { class: "field" }, [el("label", {}, ["Tags"]), tags]),
      el("div", { class: "field" }, [el("label", {}, ["Concurrency"]), concurrency]),
      el("div", { class: "field" }, [el("label", {}, ["Timeout"]), timeout]),
    ]),
    el("div", { class: "field" }, [
      el("label", {}, ["Roles"]),
      el("div", { class: "checkbox-row" }, roleBoxes.flatMap((b) => [
        el("label", { class: "checkbox" }, [b.cb, b.role]),
      ])),
    ]),
    err,
    el("div", { class: "form-actions" }, [submitBtn]),
  ]) as HTMLFormElement;

  function resetForm() {
    editingId = null;
    form.reset();
    roleBoxes.forEach((b) => (b.cb.checked = b.role === "implementer"));
    kind.value = AGENT_KINDS[0].id;
    concurrency.value = "1";
    timeout.value = "20m";
    submitBtn.textContent = "Add agent";
  }

  // Expose a way for the list to populate the form for editing.
  (root as any).__editAgent = (a: Agent) => {
    editingId = a.id;
    name.value = a.name;
    kind.value = (kindFor(a.command) ?? AGENT_KINDS[0]).id;
    tags.value = a.tags?.join(", ") ?? "";
    concurrency.value = String(a.concurrency);
    timeout.value = a.timeout;
    roleBoxes.forEach((b) => (b.cb.checked = a.roles?.includes(b.role) ?? false));
    submitBtn.textContent = "Save changes";
    form.scrollIntoView({ behavior: "smooth" });
  };

  return form;
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
  const root = document.getElementById("app")!;
  return el("div", { class: "card agent-card" }, [
    el("div", { class: "card-main" }, [
      el("div", { class: "card-title" }, [
        a.name,
        el("span", { class: a.enabled ? "pill on" : "pill off" }, [a.enabled ? "enabled" : "disabled"]),
      ]),
      el("code", { class: "card-cmd" }, [kindFor(a.command)?.label ?? a.command]),
      el("div", { class: "card-meta" }, [
        ...(a.roles ?? []).map((r) => el("span", { class: "tag role" }, [r])),
        ...(a.tags ?? []).map((t) => el("span", { class: "tag" }, [t])),
        el("span", { class: "muted" }, [`×${a.concurrency} · ${a.timeout || "no timeout"}`]),
      ]),
    ]),
    el("div", { class: "card-actions" }, [
      el("button", { onclick: () => (root as any).__editAgent(a) }, ["Edit"]),
      el("button", {
        onclick: async () => {
          a.enabled ? await api.disableAgent(a.id) : await api.enableAgent(a.id);
          refresh();
        },
      }, [a.enabled ? "Disable" : "Enable"]),
      el("button", {
        class: "danger",
        onclick: async () => {
          if (confirm(`Delete agent “${a.name}”?`)) {
            await api.deleteAgent(a.id);
            refresh();
          }
        },
      }, ["Delete"]),
    ]),
  ]);
}

export function onAgentEvent(): void {
  if (document.getElementById("agent-list")) refresh();
}
