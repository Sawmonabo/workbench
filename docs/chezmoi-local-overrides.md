# Configuration ownership and local overrides

Workbench uses chezmoi as its machine engine with explicit source, destination,
answers, cache and persistent-state paths. Use Workbench lifecycle commands for
Workbench-managed state; an unrelated default chezmoi context does not share
Workbench consent/checkpoints or release identity.

## Machine answers

`home/.chezmoi.toml.tmpl` owns the native questionnaire. Approved setup saves
validated private `[data]` answers, not arbitrary native hooks, source overrides
or Git auto-push configuration. `--machine-config PATH` explicitly selects an
existing private answer file. Complete unattended inputs include identity,
role, editor, version mode, derived role/platform flags and conditional fields.

| Role | Identity and integration |
| --- | --- |
| `personal` | Default Git email; personal trust root; no work tokens/hooks. |
| `work` | Default Git email; work trust root and selected work integration. |
| `both` | Personal email under `~/dev/`, work email under `~/repos/`; both roots. |

Roles derive `has_personal` and `has_work`; native host detection derives
`is_wsl`. Editor choices are `code`/`vim`; version mode is `pinned`/`latest`.
Mixed role asks for both emails, work roles for local-only service credentials,
and WSL for its sizing/restart-path inputs. Secrets never belong in source,
public plans or project provenance. Role changes can remove exact managed
targets; configuration-only mode still previews/checkpoints those removals.

A minimal personal answer file for macOS or native Linux is:

```toml
[data]
name = "Developer"
email = "developer@example.invalid"
machine_role = "personal"
has_personal = true
has_work = false
is_wsl = false
editor = "code"
versions_mode = "pinned"
```

Save private runtime answers outside the repository with mode `0600`, replace
the sample identity deliberately, and supply that file with `--machine-config`.
This example is not a WSL/work-role input and does not authorize application.

## Local versus managed data

- `.zshrc.local` and `.bash_aliases` remain unmanaged shell overrides.
- Global VS Code preferences are owned by `home/.chezmoidata/vscode.json`.
  The shared merge preserves unrelated nested values and custom color rules,
  accepts JSONC, and emits JSON without comments.
- Codex's managed body and application-owned state use the existing native
  modify target; malformed TOML fails rather than replacing unrelated state.
- Portable project policy belongs in `project/python/`, not global settings.
  Project editing preserves supported comments/order through format-aware
  libraries and rejects unsupported round trips.

The machine policy includes security-sensitive AI trust/permission settings,
including Codex full-access/no-approval defaults, Claude's permission-related
wrapper, executable hooks and work-session handling. These must remain visible
in the machine plan; applying a role is a deliberate trust decision, not a
claim that disabling safeguards is universally appropriate. The shell wrapper
stays a single `claude()` function in each native shell, not an IDE-routing layer.
VS Code is the editor integration; no alternative-IDE routing is introduced.

## Edits and recovery

Do not treat a managed file as an unmanaged override: a future approved apply
can replace its owned content. Update canonical source when changing shared
policy, regenerate source trust and rebuild. Keep local-only values in their
intended override/configuration owner.

Recovery restores recorded pre-images only after all post-images still match.
A later manual edit blocks the whole restore. Do not delete checkpoints or
reset native state to bypass a conflict. File recovery does not roll back
packages, extension installations, registry state or executed hooks. See
[usage](usage.md#recovery-and-runtime-storage) for supported limits.
