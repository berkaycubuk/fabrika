import { el } from "../dom.js";
import { openModal } from "../ui.js";

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
