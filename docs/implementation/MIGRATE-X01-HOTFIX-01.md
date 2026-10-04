# MIGRATE-X01-HOTFIX-01 — selected-mount finalization proof

## Purpose

CANONICAL-SCOPE-HOTFIX-01 discovered RNX-P508: migration must start only the Nexus-defined mounts explicitly selected during review. The production implementation already carried reviewed mount names through `ImportedMounts`, but the previous gate did not execute final mount startup and therefore could not truthfully seal the newly canonical obligation.

## Behavioral closure

The hotfix does not add an alternate migration path. It requalifies the existing production authority:

1. discover two provider-owned mounts;
2. explicitly select exactly one public mount ID;
3. preview/apply and require only that selected mount to enter the Nexus registry, disabled;
4. require the other provider mount to remain outside Nexus authority;
5. externally disable the legacy provider;
6. finalize through the normal migration authority;
7. require the selected Nexus mount to be live with a real process identity;
8. require the unselected provider mount to remain `not-configured`;
9. require durable migration evidence to bind exactly the selected mount.

The compiled `racctl migration` gate executes the same sequence in addition to the package-level Go regression.

## Canonical effect

- RNX-P508: `PARTIALLY_ADOPTED` → `IMPLEMENTED_AND_PRODUCTION_ADOPTED`.
- Canonical maximum remains RNX-P514.
- No prior RNX IDs are renumbered.
- Active UX-X01 position remains position 10; the prior reopened-promise marker is removed.
