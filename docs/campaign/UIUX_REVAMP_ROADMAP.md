# Rclone Nexus UI/UX Revamp Campaign

This campaign replaces the current desktop-derived WebUI with a mobile-first control center while preserving backend authority, static-runtime semantics, and expert diagnostics.

## Validation hierarchy

1. **Overlay validation** proves only the surface changed by that overlay and is the rollback barrier.
2. **Milestone gates** prove convergence across a completed UX slice.
3. **FG1 final qualification** proves the whole current product and both runtime-provider package paths.
4. **FS1 final seal** proves roadmap closure, fixed-point source/package state, and release ancestry.

Historical mutable-runtime campaign tests are not current-product UI gates.

## Overlay sequence

| ID | Scope | Status |
| --- | --- | --- |
| UX01 | Design system + responsive shell foundation | ACTIVE |
| UX02 | Navigation + information architecture | PLANNED |
| MG1 | Shell & Navigation Gate | PLANNED |
| UX03 | Home control center | PLANNED |
| UX04 | Mount cards + contextual actions | PLANNED |
| UX05 | Guided Add Mount wizard | PLANNED |
| UX06 | Remote browser | PLANNED |
| MG2 | Primary Interaction Gate | PLANNED |
| UX07 | Runtime provenance + static-runtime semantics | PLANNED |
| UX08 | Activity + legacy history separation | PLANNED |
| UX09 | Doctor severity semantics | PLANNED |
| MG3 | Product Semantics Gate | PLANNED |
| UX10 | Dense technical logs | PLANNED |
| UX11 | Grouped settings | PLANNED |
| UX12 | Accessibility + async state + polish | PLANNED |
| MG4 | UX Reliability Gate | PLANNED |
| FG1 | Final UI Qualification Gate | PLANNED |
| FS1 | Final UI/UX Seal | PLANNED |

## UX01 acceptance contract

UX01 is intentionally structural. It must not redesign navigation or move screens.

It establishes:

- reusable design tokens for color, spacing, radii, touch size, content width, and safe areas;
- Android safe-area padding via `env(safe-area-inset-*)`;
- `100dvh` viewport sizing and root horizontal-overflow containment;
- a mobile-shell breakpoint that includes the observed 691px portrait viewport;
- minimum 44px controls;
- shrink-before-wrap behavior for technical values in grid/flex contexts;
- responsive one-column fallbacks for metadata/form/detail surfaces;
- DevTool target-local UX01 jobs and an executable `ux01-foundation` workflow.

UX02 owns primary-navigation replacement. No bottom navigation is introduced by UX01.
