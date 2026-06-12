// Sessions screen: interactive chat with a coding agent in its own worktree —
// the in-UI replacement for dropping to a terminal for ad-hoc fixes and small
// features. Each send is one agent turn; Finish routes the session's work
// through the normal gate + merge pipeline.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { button, pill, field } from "../components.js";
import type { Agent, FabrikaEvent, Session, SessionHeartbeat, SessionMessage, SessionStream } from "../types.js";
import { DEFAULT_AVATAR } from "../avatar.js";
import { openModal, closeModal } from "../ui.js";
import { AGENT_KINDS } from "../agentKinds.js";
import { createModelPicker } from "./modelPicker.js";
import { attachmentGallery, imageAttach } from "./attachment.js";

// The session whose chat is open, or null for the list. Module state so live
// events know which surface to refresh.
let openId: string | null = null;
let agentsById: Map<string, Agent> = new Map();

export function renderSessions(root: HTMLElement): void {
  openId = null;
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Sessions"]),
      el("p", { class: "muted" }, [
        "Chat with an agent in an isolated worktree for ad-hoc fixes and custom work. Finishing runs the gate and merges, same as any task.",
      ]),
    ]),
    el("div", { class: "form-actions" }, [
      button("New session", { variant: "primary", onclick: openNewSessionModal }),
    ]),
    el("div", { id: "session-list", class: "card-list" }, ["Loading…"]),
  );
  void refreshList();
}

const statusTone = (s: Session): string =>
  s.status === "active" ? (s.busy ? "soon" : "on")
  : s.status === "gating" ? "soon"
  : s.status === "merged" ? "status-merged"
  : "off";

const statusLabel = (s: Session): string =>
  s.status === "active" && s.busy ? "working" : s.status;

async function loadAgents(): Promise<void> {
  try {
    agentsById = new Map((await api.listAgents()).map((a) => [a.id, a]));
  } catch {
    /* names degrade to ids */
  }
}

async function refreshList(): Promise<void> {
  const list = document.getElementById("session-list");
  if (!list) return;
  try {
    const [sessions] = await Promise.all([api.listSessions(), loadAgents()]);
    clear(list);
    if (sessions.length === 0) {
      list.append(el("p", { class: "muted" }, ["No sessions yet. Start one to chat with an agent about this repo."]));
      return;
    }
    for (const s of sessions) list.append(sessionCard(s));
  } catch (e) {
    list.textContent = (e as Error).message;
  }
}

function sessionCard(s: Session): HTMLElement {
  const agent = agentsById.get(s.agentId);
  return el("div", {
    class: "card session-card",
    onclick: () => openChat(s.id),
  }, [
    el("div", { class: "card-main" }, [
      el("img", { class: "agent-avatar", src: agent?.photo || DEFAULT_AVATAR, alt: "" }),
      el("div", { class: "card-title" }, [
        s.title || "New session",
        pill(statusLabel(s), statusTone(s)),
      ]),
      el("div", { class: "card-meta" }, [
        el("span", { class: "muted" }, [
          `${agent?.name ?? s.agentId} · ${s.branch} → ${s.baseBranch}`,
        ]),
      ]),
    ]),
  ]);
}

function openNewSessionModal(): void {
  const agentSel = el("select", {}) as HTMLSelectElement;
  const picker = createModelPicker();
  const base = el("input", { placeholder: "Base branch (default: current)" }) as HTMLInputElement;
  const err = el("div", { class: "form-error" });

  const agents = [...agentsById.values()].filter((a) => a.enabled);
  for (const a of agents) agentSel.append(el("option", { value: a.id }, [a.name]));

  const syncModels = () => {
    const a = agentsById.get(agentSel.value);
    const kind = AGENT_KINDS.find((k) => k.command === a?.command);
    picker.setModels(kind?.models ?? [], a?.model || undefined);
  };
  agentSel.addEventListener("change", syncModels);
  syncModels();

  const form = el("form", {
    class: "modal-form",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      err.textContent = "";
      try {
        const s = await api.createSession({
          agentId: agentSel.value,
          model: picker.getValue(),
          baseBranch: base.value.trim(),
        });
        closeModal();
        await openChat(s.id);
      } catch (e2) {
        err.textContent = (e2 as Error).message;
      }
    },
  }, [
    field("Agent", agentSel),
    field("Model", picker.el),
    field("Base branch", base),
    err,
    el("div", { class: "form-actions" }, [
      button("Start session", { variant: "primary", type: "submit", disabled: agents.length === 0 }),
      ...(agents.length === 0 ? [el("span", { class: "muted" }, ["Register an agent first."])] : []),
    ]),
  ]) as HTMLFormElement;

  openModal("New session", form);
}

// --- chat surface ---

async function openChat(id: string): Promise<void> {
  const root = document.querySelector("main.content") as HTMLElement | null;
  if (!root) return;
  openId = id;
  if (agentsById.size === 0) await loadAgents();
  clear(root);
  root.append(el("div", { id: "session-chat", class: "session-chat", "data-id": id }, ["Loading…"]));
  await refreshChat();
}

async function refreshChat(): Promise<void> {
  if (!openId) return;
  const host = document.getElementById("session-chat");
  if (!host || host.dataset.id !== openId) return;
  try {
    const { session, messages } = await api.getSession(openId);
    renderChat(host, session, messages);
  } catch (e) {
    host.textContent = (e as Error).message;
  }
}

function renderChat(host: HTMLElement, s: Session, messages: SessionMessage[]): void {
  const agent = agentsById.get(s.agentId);
  const canChat = s.status === "active" && !s.busy;

  const input = el("textarea", {
    class: "chat-input",
    rows: "2",
    placeholder: s.status === "active" ? "Type a message… (Enter to send, Shift+Enter for newline, paste or attach images)" : `Session is ${s.status}.`,
    disabled: !canChat,
  }) as HTMLTextAreaElement;
  const attachErr = el("div", { class: "form-error" });
  const attach = imageAttach(input, attachErr);

  const send = async () => {
    const body = input.value.trim();
    const attachments = attach.urls();
    if (!body && attachments.length === 0) return;
    input.value = "";
    attach.reset();
    try {
      await api.sendSessionMessage(s.id, body, attachments);
    } catch (e) {
      alert((e as Error).message);
    }
    void refreshChat();
  };
  input.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void send();
    }
  });

  const pulse = el("div", { id: "chat-pulse", class: "chat-pulse" + (s.busy || s.status === "gating" ? "" : " hidden") }, [
    s.status === "gating" ? "● running the gate…" : "● working…",
  ]);

  clear(host);
  host.append(
    el("div", { class: "chat-head" }, [
      button("← Sessions", {
        variant: "link",
        onclick: () => {
          const root = document.querySelector("main.content") as HTMLElement;
          if (root) renderSessions(root);
        },
      }),
      el("div", { class: "chat-head-title" }, [
        el("img", { class: "agent-avatar", src: agent?.photo || DEFAULT_AVATAR, alt: "" }),
        el("span", { class: "card-title" }, [s.title || "New session"]),
        pill(statusLabel(s), statusTone(s)),
      ]),
      el("span", { class: "muted sm" }, [`${agent?.name ?? s.agentId} · ${s.branch} → ${s.baseBranch}`]),
      el("div", { class: "chat-actions" }, [
        button("Finish & gate", {
          variant: "primary",
          disabled: !canChat,
          title: "Commit the worktree, run the gate, and merge into " + s.baseBranch,
          onclick: async () => {
            try {
              await api.finishSession(s.id);
            } catch (e) {
              alert((e as Error).message);
            }
            void refreshChat();
          },
        }),
        button("Discard", {
          variant: "danger",
          disabled: s.status !== "active",
          onclick: async () => {
            if (!confirm("Discard this session? Its branch and uncommitted work are dropped.")) return;
            try {
              await api.discardSession(s.id);
            } catch (e) {
              alert((e as Error).message);
            }
            void refreshChat();
          },
        }),
      ]),
    ]),
    el("div", { id: "chat-messages", class: "chat-messages" },
      messages.length === 0
        ? [el("p", { class: "muted chat-empty" }, ["Describe the bug to fix or the feature to build — the agent works in this session's worktree."])]
        : messages.map((m) => messageEl(m, agent))),
    pulse,
    el("div", { class: "chat-compose" }, [
      attach.previews,
      attachErr,
      el("div", { class: "chat-input-row" }, [
        input,
        ...(canChat ? attach.controls : []),
        button("Send", { variant: "primary", disabled: !canChat, onclick: () => void send() }),
      ]),
    ]),
  );
  const msgs = document.getElementById("chat-messages");
  if (msgs) msgs.scrollTop = msgs.scrollHeight;
  if (canChat) input.focus();
}

function messageEl(m: SessionMessage, agent?: Agent): HTMLElement {
  if (m.role === "system") {
    return el("div", { class: "chat-msg system" }, [el("pre", {}, [m.body])]);
  }
  const who = m.role === "user" ? "You" : agent?.name ?? "Agent";
  return el("div", { class: `chat-msg ${m.role}` }, [
    el("div", { class: "chat-msg-who muted sm" }, [who]),
    ...(m.body ? [el("pre", {}, [m.body])] : []),
    ...(m.attachments?.length ? [attachmentGallery(m.attachments)] : []),
  ]);
}

// --- live events ---

// onSessionsEvent refreshes whichever session surface is on screen. Message and
// session events for the open chat refetch the transcript; anything else
// refreshes the list when it's visible.
export function onSessionsEvent(e?: FabrikaEvent): void {
  const chat = document.getElementById("session-chat");
  if (chat && openId) {
    const p = e?.payload as { sessionId?: string; id?: string } | undefined;
    const sid = p?.sessionId ?? p?.id;
    if (!e || !sid || sid === openId) void refreshChat();
    return;
  }
  if (document.getElementById("session-list")) void refreshList();
}

// onSessionStream renders the in-flight turn's reply as it forms: a streaming
// agent bubble pinned after the transcript, its text replaced in place on each
// pulse (payloads carry the full text-so-far, so a dropped event self-heals).
// The bubble is transient — the transcript refetch on session.message.added
// re-renders the chat and the final stored reply takes its place.
export function onSessionStream(st: SessionStream): void {
  const chat = document.getElementById("session-chat");
  if (!chat || chat.dataset.id !== st.sessionId) return;
  const msgs = document.getElementById("chat-messages");
  if (!msgs) return;
  let bubble = document.getElementById("chat-stream");
  if (!bubble) {
    bubble = el("div", { id: "chat-stream", class: "chat-msg agent streaming" }, [
      el("div", { class: "chat-msg-who muted sm" }, [st.agentName || "Agent"]),
      el("pre", {}, []),
    ]);
    msgs.append(bubble);
  }
  const pre = bubble.querySelector("pre");
  if (!pre || pre.textContent === st.text) return;
  // Follow the stream only when the reader is already at the bottom — don't
  // yank the scroll out from under someone reviewing earlier messages.
  const atBottom = msgs.scrollHeight - msgs.scrollTop - msgs.clientHeight < 48;
  pre.textContent = st.text;
  if (atBottom) msgs.scrollTop = msgs.scrollHeight;
}

// onSessionHeartbeat updates the open chat's working pulse in place — no refetch.
export function onSessionHeartbeat(hb: SessionHeartbeat): void {
  const chat = document.getElementById("session-chat");
  if (!chat || chat.dataset.id !== hb.sessionId) return;
  const pulse = document.getElementById("chat-pulse");
  if (!pulse) return;
  pulse.classList.remove("hidden");
  const mins = Math.floor(hb.runningSeconds / 60);
  const dur = mins > 0 ? `${mins}m${hb.runningSeconds % 60}s` : `${hb.runningSeconds}s`;
  pulse.textContent = hb.idleSeconds > 30
    ? `● quiet · no output for ${hb.idleSeconds}s · ${dur}`
    : `● working · ${dur}${hb.lastLine ? " · " + hb.lastLine : ""}`;
  pulse.classList.toggle("quiet", hb.idleSeconds > 30);
}
