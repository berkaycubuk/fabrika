import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { toToml, fromToml } from "../src/views/config.js";
import type { ConfigManifest } from "../src/types.js";

describe("config TOML round-trip", () => {
  it("preserves a plain manifest through toToml → fromToml", () => {
    const m: ConfigManifest = {
      project: { name: "Fabrika" },
      verbs: {
        setup: "make setup",
        build: "make build",
        test: "go test ./...",
        lint: "",
        typecheck: "tsc --noEmit",
        verify: "",
        e2e: "",
        run: "",
      },
      risk: { high: ["**/*.sql"], medium: ["internal/api/**"] },
      autonomy: { auto_merge: ["low"], escalate: ["medium", "high"] },
    };
    assert.deepEqual(fromToml(toToml(m)), m);
  });

  it("escapes embedded quotes in the project name", () => {
    const m: ConfigManifest = {
      project: { name: 'My "Quoted" Project' },
      verbs: Object.fromEntries(
        ["setup", "build", "test", "lint", "typecheck", "verify", "e2e", "run"].map((k) => [k, ""]),
      ) as ConfigManifest["verbs"],
      risk: {},
      autonomy: {},
    };
    const toml = toToml(m);
    assert.match(toml, /name = "My \\"Quoted\\" Project"/);
    assert.equal(fromToml(toml).project.name, 'My "Quoted" Project');
  });

  it("escapes embedded quotes in verb commands", () => {
    const m: ConfigManifest = {
      project: { name: "p" },
      verbs: Object.fromEntries(
        ["setup", "build", "test", "lint", "typecheck", "verify", "e2e", "run"].map((k) => [k, ""]),
      ) as ConfigManifest["verbs"],
      risk: {},
      autonomy: {},
    };
    m.verbs.test = 'echo "hi"';
    assert.equal(fromToml(toToml(m)).verbs.test, 'echo "hi"');
  });
});
