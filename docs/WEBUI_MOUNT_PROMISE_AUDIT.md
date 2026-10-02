# WebUI Mount Creation Promise Audit

Scope: guided mount creation remediation, audited after v1 and closed in v2.
Baseline: `2d14889`.
Roadmap position: post-GRAND-G1 remediation within the existing 16/16 roadmap; not a new roadmap position.

## Promise universe and disposition

| # | Promise | v2 disposition |
|---:|---|---|
| 1 | Preserve the backend-authoritative `config.preview` -> one-use proof -> `config.apply` mutation boundary. | Delivered. `config.validate` is query-only and does not issue a proof. |
| 2 | Add non-mutating, multi-issue validation before preview. | Delivered by `config.validate` plus local validation and final preview. |
| 3 | Return structured, categorized, field-addressable errors with safe suggestions. | Delivered; issues now also identify the affected mount so another registry entry is not misrepresented as the current form field. |
| 4 | Use the existing credential-safe remote list/browser in mount creation. | Delivered with remote selection, folder browsing, parent navigation and manual fallback. |
| 5 | Provide explicit remote connection testing and meaningful provider readiness feedback. | Delivered; rclone, FUSE helper, provider config, configured remote identity and path reachability are surfaced independently. |
| 6 | Generate useful mount names and shared-storage mountpoint defaults. | Delivered from selected remote/path, with manual override. |
| 7 | Give mountpoint conflict/protected-path feedback without claiming backend availability before backend validation completes. | Delivered; local absolute-path status is distinct from backend protected-path and overlap checks. |
| 8 | Make VFS profiles the primary performance UI and retain Custom for explicit tuning. | Delivered. Non-Custom profiles synchronize effective VFS values and disable raw controls while profile-owned. |
| 9 | Show the device-aware VFS recommendation and the resource observations behind it. | Delivered with recommendation reason, RAM, cache-free space and cache target. |
| 10 | Rewrite technical controls in user-intent language and keep startup/diagnostic knobs advanced. | Delivered. |
| 11 | Render inline field feedback plus a categorized error summary. | Delivered; registry-wide issues from another mount stay in the summary instead of being attached to the current field. |
| 12 | Turn preview into a human-readable Review/Create step while retaining technical details. | Delivered with source, destination, performance, auto-start, visibility, access, probing and policy summaries. |
| 13 | Make destructive/delete semantics explicit and state cache preservation. | Delivered with an explicit safety panel and deletion-specific review. |
| 14 | Show preview proof expiry as a countdown and explain expiry without implying mutation. | Delivered; expiry explicitly states that nothing changed. |
| 15 | Show operation progress during apply. | Delivered from the persistent operation journal and lifecycle progress events. |
| 16 | Distinguish configuration publication from lifecycle partial failure and give recovery actions. | Delivered with Retry start, Edit mount, View operations and View logs where applicable. |
| 17 | Protect dirty forms from backdrop/Escape loss. | Delivered; async helper completion can no longer re-baseline fast user edits as clean. |
| 18 | Improve modal keyboard focus and mobile full-screen/sticky-action behavior. | Delivered with focus trapping/restoration and mobile full-screen editor/sticky footer. |
| 19 | Preserve credential secrecy and avoid browser persistence, HTML injection helpers and eval-like primitives. | Delivered and enforced by WebUI contract checks. |
| 20 | Qualify the finished WebUI through targeted source checks, Go tests and the root-module package contract. | Delivered; artifact validation runs the strengthened WebUI contract, targeted Go packages and package contract. |
| 21 | Restore the accidentally deleted `internal/cache` package required by current production imports without changing its behavior. | Delivered byte-for-byte from the parent baseline. |

## v1 audit gaps closed by v2

The v1 functional implementation passed its tests but the promise audit found seven gaps: provider readiness was not surfaced in the editor; destination success could be shown before authoritative backend validation; non-Custom VFS raw fields remained editable even though ignored at runtime; device recommendation omitted its measured resource context; review omitted key behavior choices and an explicit safety result; lifecycle partial-success recovery lacked direct retry/edit/log actions; and asynchronous helper loading could overwrite the dirty-form baseline after a fast user edit. v2 closes all seven and adds mount identity to structured validation issues so full-registry validation remains field-truthful.
