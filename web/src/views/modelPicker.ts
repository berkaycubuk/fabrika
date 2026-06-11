import { el, clear } from "../dom.js";
import type { ModelOption } from "../agentKinds.js";

const CUSTOM = "__custom__";

export interface ModelPicker {
  el: HTMLElement;
  getValue(): string;
  setModels(models: ModelOption[], selected?: string): void;
}

export function createModelPicker(): ModelPicker {
  const select = el("select", { class: "model-select" }) as HTMLSelectElement;
  const custom = el("input", { class: "model-custom", type: "text", placeholder: "provider/model-id" }) as HTMLInputElement;
  custom.hidden = true;

  select.addEventListener("change", () => {
    custom.hidden = select.value !== CUSTOM;
  });

  const container = el("div", { class: "model-picker" }, [select, custom]);

  function setModels(models: ModelOption[], selected?: string): void {
    clear(select);
    for (const m of models) {
      select.append(el("option", { value: m.id }, [m.label]));
    }
    select.append(el("option", { value: CUSTOM }, ["Custom…"]));

    if (selected && selected.length > 0) {
      const found = models.some((m) => m.id === selected);
      if (found) {
        select.value = selected;
        custom.hidden = true;
      } else {
        select.value = CUSTOM;
        custom.value = selected;
        custom.hidden = false;
      }
    } else {
      select.value = models.length > 0 ? models[0].id : CUSTOM;
      custom.hidden = true;
    }
  }

  function getValue(): string {
    if (select.value === CUSTOM) {
      return custom.value.trim();
    }
    return select.value;
  }

  return { el: container, getValue, setModels };
}
