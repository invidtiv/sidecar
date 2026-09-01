# Third-party notices

Sidecar is distributed under the licence in `LICENSE`. It also carries copies of files from other projects. Each entry below names the project, the licence, and where the copies live. Go module dependencies are declared in `go.mod` and carry their own licences with their source; they are not repeated here.

## Herdr

- **Project:** Herdr
- **Repository:** https://github.com/herdrdev/herdr
- **Licence:** Apache License, Version 2.0
- **Vendored at:** `internal/agentactivity/manifests/upstream/`
- **Files:** the agent-detection manifests (`*.toml`), the published catalog index (`index.toml`), and Herdr's `LICENSE`.

These files are unmodified copies. The exact upstream commit, the catalog ETag, and a sha256 for every file are recorded in `internal/agentactivity/manifests/upstream.lock.json`; `internal/agentactivity/manifests/upstream/NOTICE` carries the Apache-2.0 attribution alongside the files themselves, as the licence requires. `docs/reference/herdr-detection-parity.md` explains what is vendored and how to refresh it.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use these files except in compliance with the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.
