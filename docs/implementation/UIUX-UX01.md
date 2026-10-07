# UIUX-UX01 — Foundation & Responsive Shell

## Scope

UX01 creates the design-system and responsive-shell substrate for the Rclone Nexus WebUI revamp. It does not change routes, navigation ownership, runtime semantics, or backend protocols.

## Required evidence

The canonical DevTool workflow is:

```sh
./devtoolw ui-ux01
```

It runs three target-local, read-only jobs:

1. `ux01-tokens` — design-token and minimum-target contract;
2. `ux01-shell` — safe-area, viewport, and long-value shell contract;
3. `ux01-responsive` — mobile breakpoint and narrow-layout contract.

Atomic overlay application runs equivalent first-class DevTool TestSpecs. These checks are intentionally narrower than the existing complete WebUI workflow.

## Acceptance

- `viewport-fit=cover` remains present.
- Safe-area insets are consumed by top-level shell surfaces.
- 691px width resolves through the mobile-shell rules.
- Controls have a 44px minimum target.
- Long technical values cannot force grid/flex overflow.
- Existing WebUI routes and backend contracts remain untouched.
