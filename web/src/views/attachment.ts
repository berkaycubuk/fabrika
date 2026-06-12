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
