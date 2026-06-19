import { el, clear } from "../dom.js";
import { openModal } from "../ui.js";
import { api } from "../api.js";

const IMAGE_EXT = /\.(png|jpe?g|gif|webp)$/i;
const VIDEO_EXT = /\.(mp4|webm)$/i;

export function attachmentGallery(urls: string[]): HTMLElement {
  return el("div", { class: "attachments" }, urls.map((url) =>
    IMAGE_EXT.test(url)
      ? el("img", { src: url, class: "attach-thumb", alt: "attachment", loading: "lazy", onclick: () => openAttachment(url) }, [])
      : el("div", { class: "attach-link", onclick: () => openAttachment(url) }, [
          url.split("/").pop() ?? url,
        ]),
  ));
}

export function openAttachment(url: string): void {
  const filename = url.split("/").pop() ?? url;
  openModal(filename, attachmentBody(url), { wide: true });
}

export function attachmentBody(url: string): HTMLElement {
  if (IMAGE_EXT.test(url)) {
    return el("img", { src: url, class: "attach-full", alt: "attachment" }, []);
  }
  if (VIDEO_EXT.test(url)) {
    return el("video", { src: url, class: "attach-full", controls: true }, []);
  }
  return el("iframe", { src: url, class: "attach-frame" }, []);
}

export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  if (kb < 1024) return `${Math.round(kb)} KB`;
  return `${(kb / 1024).toFixed(1)} MB`;
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let val = n / 1024;
  let unit = units[0];
  for (let i = 0; i < units.length; i++) {
    unit = units[i];
    if (val < 1024 || i === units.length - 1) break;
    val /= 1024;
  }
  const str = val.toFixed(1);
  return `${str.endsWith(".0") ? str.slice(0, -2) : str} ${unit}`;
}

export function attachmentList(urls: string[]): HTMLElement {
  const rows = urls.map((url) => {
    const filename = url.split("/").pop() ?? url;
    const sizeEl = el("span", { class: "attachment-size" }, ["—"]);
    const meta = el("div", { class: "attachment-meta" }, [
      el("span", { class: "attachment-name", onclick: () => openAttachment(url) }, [filename]),
      sizeEl,
      el("a", { href: url, download: "", class: "attachment-dl" }, ["↓"]),
    ]);
    const children = IMAGE_EXT.test(url)
      ? [
          el("img", {
            src: url,
            class: "attachment-thumb",
            alt: filename,
            loading: "lazy",
            onclick: () => openAttachment(url),
          }, []),
          meta,
        ]
      : [meta];
    const row = el("div", { class: "attachment-row" }, children);
    fetch(url, { method: "HEAD" }).then((r) => {
      const len = r.headers.get("content-length");
      if (len) sizeEl.textContent = formatBytes(Number(len));
    }).catch(() => {});
    return row;
  });
  return el("div", { class: "attachment-list" }, rows);
}

export function fileChip(opts: { name: string; size: number; onRemove: () => void }): HTMLElement {
  const chip = el("div", { class: "file-chip" }, []);

  const svgNS = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(svgNS, "svg");
  svg.setAttribute("class", "chip-clip");
  svg.setAttribute("viewBox", "0 0 12 12");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.5");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  const path = document.createElementNS(svgNS, "path");
  path.setAttribute("d", "M10.5 5.5L5 11a3 3 0 01-4.243-4.243l5.657-5.657a1.5 1.5 0 012.121 2.121L3.879 8.879a.75.75 0 01-1.061-1.06L7.5 3");
  svg.appendChild(path);

  chip.appendChild(svg);
  chip.appendChild(el("span", { class: "chip-name" }, [opts.name]));
  chip.appendChild(el("span", { class: "chip-size" }, [formatFileSize(opts.size)]));
  chip.appendChild(el("button", { type: "button", class: "chip-remove", onclick: opts.onRemove }, ["✕"]));

  return chip;
}

export function fileAttach(opts: { err: HTMLElement; pasteTarget?: HTMLTextAreaElement }): {
  el: HTMLElement;
  urls: () => string[];
  reset: () => void;
  accept: (files: File[]) => void;
} {
  type PendingFile = { name: string; size: number; url: string };
  const pending: PendingFile[] = [];

  const wrap = el("div", { class: "attach-files" }, []);

  const picker = el("input", {
    type: "file",
    accept: "image/png,image/jpeg,image/gif,image/webp,.txt,.log,.json,.mp4,.webm",
    multiple: true,
    class: "attach-file",
    onchange: () => {
      accept(Array.from(picker.files ?? []));
      picker.value = "";
    },
  }) as HTMLInputElement;

  const addBtn = el("div", { class: "attach-add", onclick: () => picker.click() }, ["+ Attach file"]);

  const render = () => {
    wrap.replaceChildren();
    pending.forEach((pf, i) => {
      wrap.appendChild(fileChip({
        name: pf.name,
        size: pf.size,
        onRemove: () => {
          pending.splice(i, 1);
          render();
        },
      }));
    });
    wrap.appendChild(picker);
    wrap.appendChild(addBtn);
  };

  const accept = async (files: File[]) => {
    opts.err.textContent = "";
    for (const f of files) {
      try {
        const url = await api.uploadImage(f);
        pending.push({ name: f.name, size: f.size, url });
      } catch (e) {
        opts.err.textContent = (e as Error).message;
      }
    }
    render();
  };

  if (opts.pasteTarget) {
    opts.pasteTarget.addEventListener("paste", (e: ClipboardEvent) => {
      const files = Array.from(e.clipboardData?.files ?? []).filter((f) => f.type.startsWith("image/"));
      if (files.length === 0) return;
      e.preventDefault();
      void accept(files);
    });
  }

  render();

  return {
    el: wrap,
    urls: () => pending.map((pf) => pf.url),
    reset: () => {
      pending.length = 0;
      render();
    },
    accept,
  };
}

// imageAttach bundles the shared image-attach UI used by the comment composer,
// the create/define forms, and the session chat: a hidden file picker, an
// "Attach image" button, pending thumbnails with remove buttons, and
// paste-to-attach wiring on a textarea. Files upload immediately; urls() is
// what submit should send, and reset() clears the pending set after a
// successful post.
export function imageAttach(pasteTarget: HTMLTextAreaElement, err: HTMLElement) {
  const pending: string[] = []; // uploaded image URLs awaiting submit
  const previews = el("div", { class: "attachments" }, []);

  const render = () => {
    clear(previews);
    pending.forEach((url, i) => {
      previews.append(el("div", { class: "attach-pending" }, [
        el("img", { src: url, class: "attach-thumb", alt: "attachment" }),
        el("button", { type: "button", class: "attach-remove", title: "Remove image", onclick: () => {
          pending.splice(i, 1);
          render();
        } }, ["×"]),
      ]));
    });
  };

  const upload = async (files: File[]) => {
    err.textContent = "";
    for (const f of files) {
      if (!f.type.startsWith("image/")) continue;
      try {
        pending.push(await api.uploadImage(f));
      } catch (e) {
        err.textContent = (e as Error).message;
      }
    }
    render();
  };

  const picker = el("input", {
    type: "file",
    accept: "image/png,image/jpeg,image/gif,image/webp",
    multiple: true,
    class: "attach-file",
    onchange: () => {
      upload(Array.from(picker.files ?? []));
      picker.value = "";
    },
  }) as HTMLInputElement;

  pasteTarget.addEventListener("paste", (e: ClipboardEvent) => {
    const files = Array.from(e.clipboardData?.files ?? []).filter((f) => f.type.startsWith("image/"));
    if (files.length === 0) return;
    e.preventDefault();
    upload(files);
  });

  const attachBtn = el("button", {
    type: "button",
    title: "Attach images (or paste one into the text field)",
    onclick: () => picker.click(),
  }, ["Attach image"]);

  return {
    previews,
    controls: [picker, attachBtn],
    urls: () => [...pending],
    reset: () => {
      pending.length = 0;
      render();
    },
  };
}
