// Reports the asset's export surface.
//
// OpenCode's plugin loader requires every export of a plugin module to be a
// plugin factory; one non-function export makes it skip the module entirely,
// without an error. Measured against 1.18.25.
import * as mod from "./sidecar-lifecycle.js"

const names = Object.keys(mod)
const nonFunctions = names.filter((k) => typeof mod[k] !== "function")
process.stdout.write(JSON.stringify({ names, nonFunctions }))
