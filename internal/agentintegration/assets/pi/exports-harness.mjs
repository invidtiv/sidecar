// Reports the asset's export surface.
//
// Pi's extension loader takes a module's default export and drops the module
// entirely when it is not a function -- `if (typeof factory !== "function")
// return undefined`, dist/core/extensions/loader.js:428-432, with no error
// logged anywhere. The extension then installs cleanly, loads, and reports
// nothing at all, which is invisible to every offline test that does not run
// the module.
//
// OpenCode's loader is stricter still: EVERY export must be a plugin factory
// there. Sidecar's Pi asset holds itself to the stricter rule as well, because
// the cost is zero and the two assets then have one export convention between
// them rather than two.
import mod, * as namespace from "./sidecar-lifecycle.js"

const names = Object.keys(namespace)
const nonFunctions = names.filter((k) => typeof namespace[k] !== "function")
process.stdout.write(
  JSON.stringify({
    names,
    nonFunctions,
    defaultIsFunction: typeof mod === "function",
    defaultName: typeof mod === "function" ? mod.name : "",
  }),
)
