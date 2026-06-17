// Relay section of the Settings view: connect this daemon to a self-hosted
// fabrika-portal so a phone can answer decisions/approvals remotely. Pairing
// is by QR code; the phone talks E2E-encrypted through the portal.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { button, field, toggle } from "../components.js";
import type { RelayInfo } from "../types.js";

export function renderRelaySection(root: HTMLElement): void {
  const slot = el("div", { id: "relay-section" }, [el("p", { class: "muted" }, ["Loading…"])]);
  root.append(
    el("div", { class: "view-header relay-header" }, [
      el("h1", {}, ["Phone relay"]),
      el("p", { class: "muted" }, [
        "Keep Fabrika running here and answer its decisions and approvals from your phone, " +
          "through your own fabrika-portal server. The daemon dials out — nothing on this machine is exposed.",
      ]),
    ]),
    slot,
  );
  void load(slot);
}

async function load(slot: HTMLElement): Promise<void> {
  try {
    renderRelay(slot, await api.getRelay());
  } catch (e) {
    clear(slot);
    slot.append(el("p", { class: "form-error" }, [(e as Error).message]));
  }
}

function statusPill(info: RelayInfo): HTMLElement {
  if (!info.enabled) return el("span", { class: "pill" }, ["disabled"]);
  if (info.connected) return el("span", { class: "pill on" }, ["connected"]);
  return el("span", { class: "pill soon" }, ["connecting"]);
}

function renderRelay(slot: HTMLElement, info: RelayInfo): void {
  clear(slot);

  // --- Connection form ---
  const enabledBox = el("input", { type: "checkbox" }) as HTMLInputElement;
  enabledBox.checked = info.enabled;
  const urlInput = el("input", {
    type: "url",
    placeholder: "https://relay.example.com",
    value: info.url,
  }) as HTMLInputElement;
  const tokenInput = el("input", {
    type: "password",
    placeholder: info.tokenSet ? "(stored — leave empty to keep)" : "frk_…",
    autocomplete: "off",
  }) as HTMLInputElement;

  const err = el("div", { class: "form-error" });
  const form = el("form", {
    class: "modal-form",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      err.textContent = "";
      try {
        const updated = await api.putRelay({
          enabled: enabledBox.checked,
          url: urlInput.value.trim(),
          token: tokenInput.value.trim(),
        });
        renderRelay(slot, updated);
      } catch (e2) {
        err.textContent = (e2 as Error).message;
      }
    },
  }, [
    el("div", { class: "field relay-status-row" }, [
      el("label", {}, ["Status"]),
      el("div", {}, [
        statusPill(info),
        info.lastError ? el("span", { class: "muted relay-error" }, [` ${info.lastError}`]) : "",
      ]),
    ]),
    el("div", { class: "field relay-enable-row" }, [
      el("label", {}, ["Enabled"]),
      toggle(enabledBox),
    ]),
    field("Portal URL", urlInput),
    el("div", { class: "field" }, [
      el("label", {}, ["Relay token"]),
      tokenInput,
      el("p", { class: "muted relay-token-hint" }, [
        "Contact berkay@berkaycubuk.com to get a relay token.",
      ]),
    ]),
    err,
    el("div", { class: "form-actions" }, [
      button("Save & reconnect", { variant: "primary", type: "submit" }),
      button("Refresh", { onclick: () => void load(slot) }),
    ]),
  ]);
  slot.append(form);

  // --- Pairing ---
  const pairSlot = el("div", { class: "relay-pair" });
  slot.append(
    el("h2", { class: "relay-subhead" }, ["Pair a phone"]),
    el("p", { class: "muted" }, [
      "Scan the QR with the phone's camera; it opens the portal app and pairs end-to-end encrypted. " +
        "Each code works once and expires after 5 minutes.",
    ]),
    el("div", { class: "form-actions" }, [
      button("Show pairing QR", {
        variant: "primary",
        disabled: !info.connected,
        title: info.connected ? "" : "Relay must be connected first",
        onclick: async () => {
          clear(pairSlot);
          try {
            const p = await api.pairRelay();
            renderPairing(pairSlot, p);
          } catch (e) {
            pairSlot.append(el("p", { class: "form-error" }, [(e as Error).message]));
          }
        },
      }),
    ]),
    pairSlot,
  );

  // --- Paired devices ---
  slot.append(el("h2", { class: "relay-subhead" }, ["Paired devices"]));
  if (!info.devices.length) {
    slot.append(el("p", { class: "muted" }, ["No phones paired yet."]));
  } else {
    slot.append(el("div", {}, info.devices.map((d) =>
      el("div", { class: "relay-device-row" }, [
        el("span", {}, [d.name || "Unnamed device"]),
        el("span", { class: "muted" }, [` last seen ${d.lastSeen}`]),
        button("Remove", {
          variant: "danger",
          onclick: async () => {
            if (!confirm(`Unpair "${d.name || d.id}"? It will need a new QR to reconnect.`)) return;
            try {
              await api.deleteRelayDevice(d.id);
              void load(slot);
            } catch (e) {
              alert((e as Error).message);
            }
          },
        }),
      ]),
    )));
  }
}

function renderPairing(slot: HTMLElement, p: { url: string; png: string; expiresAt: string }): void {
  clear(slot);
  const countdown = el("p", { class: "muted" });
  const img = el("img", {
    class: "relay-qr",
    alt: "Pairing QR code",
    src: `data:image/png;base64,${p.png}`,
  });
  const tick = () => {
    const left = Math.max(0, Math.floor((new Date(p.expiresAt).getTime() - Date.now()) / 1000));
    countdown.textContent = left > 0 ? `Expires in ${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}` : "Expired — generate a new code.";
    if (left <= 0) {
      img.classList.add("relay-qr-expired");
      clearInterval(timer);
    }
  };
  const timer = setInterval(tick, 1000);
  tick();
  slot.append(
    img,
    countdown,
    el("p", { class: "muted relay-pair-url" }, ["Or open on the phone: ", el("code", {}, [p.url])]),
  );
}
