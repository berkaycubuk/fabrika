// Builds the Fabrika UI into web/dist for go:embed. Bundles src/main.ts and
// copies the static index.html + style.css. Run via `npm run build`.
import esbuild from "esbuild";
import { copyFileSync, mkdirSync, readdirSync } from "node:fs";

const watch = process.argv.includes("--watch");
mkdirSync("dist", { recursive: true });
mkdirSync("dist/fonts", { recursive: true });

const copyStatic = () => {
  copyFileSync("src/index.html", "dist/index.html");
  copyFileSync("src/style.css", "dist/style.css");
  copyFileSync("src/favicon.png", "dist/favicon.png");
  for (const f of readdirSync("src/fonts")) {
    copyFileSync(`src/fonts/${f}`, `dist/fonts/${f}`);
  }
};

const opts = {
  entryPoints: ["src/main.ts"],
  bundle: true,
  format: "esm",
  target: "es2020",
  outfile: "dist/app.js",
  sourcemap: true,
  logLevel: "info",
};

if (watch) {
  const ctx = await esbuild.context(opts);
  copyStatic();
  await ctx.watch();
  console.log("watching…");
} else {
  await esbuild.build(opts);
  copyStatic();
  console.log("built web/dist");
}
