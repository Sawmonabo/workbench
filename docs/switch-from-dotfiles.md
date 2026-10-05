# Switch a machine from dotfiles

Workbench manages the same files the dotfiles repository did. Switch each
machine once; keeping both active makes plain `chezmoi` and Workbench fight
over the same files.

1. **Install Workbench without configuring anything yet.**

   ```sh
   curl -fsSL https://github.com/Sawmonabo/workbench/releases/latest/download/install.sh | sh
   ```

   Installing never applies the machine; the CLI's last line says to run
   `workbench apply`. From a checkout instead, build with
   `go build -o bin/workbench ./cmd/workbench` and add `--local-build` to every
   `apply` below, run inside the checkout.

2. **Adopt your existing answers.** This copies only the `[data]` table into
   Workbench's private `machine.toml`. `sourceDir`, hooks and any other keys
   are ignored and never run.

   ```sh
   workbench init --answers-from ~/.config/chezmoi/chezmoi.toml
   ```

   `apply` reads only `machine.toml`.

3. **Review the plan, then apply.**

   ```sh
   workbench apply
   ```

   The first apply asks. It shows each changed file with its diff and each
   provisioning step in plain words with what it would do here; turn off any step
   you don't want (space), then press `a`. Turn off every step to apply files
   only, or keep them to provision now. Your choices are remembered: the next
   `workbench apply` prints the plan and applies them without asking, and asks
   again only for a step that is new or a file Workbench owns that you edited
   since. `workbench apply --choose` asks anyway. Every changed file is
   checkpointed; `workbench revert` lists them to restore.
   The first apply sets the managed VS Code keys in
   `home/.chezmoidata/vscode.json` (zoom, confirm prompts, ty/Ruff as the
   Python language tools). Remove any you don't want enforced before applying.

4. **Retire plain chezmoi's dotfiles config** so `chezmoi apply` can no longer
   reapply the dotfiles source:

   ```sh
   mv ~/.config/chezmoi/chezmoi.toml ~/.config/chezmoi/chezmoi.toml.dotfiles-retired
   ```

   Until you do, `workbench doctor` reports a `native-chezmoi` conflict.

5. **Provision when you're ready.** If you turned steps off in step 3, run
   `apply --choose` and turn them on; `apply --reset` forgets your choices and
   starts from the defaults.

   ```sh
   workbench apply --choose
   ```

   Every full apply includes the Homebrew cleanup dotfiles ran on every apply
   (`brew-maintenance` in the plan). Workbench keeps its own chezmoi state, so
   the first full apply runs every `run_once_` and `run_onchange_` script again;
   they skip tools that are already installed.
   If the apply has to install Homebrew or apps, it asks for your Mac password
   once before it starts, then uses your terminal for installers.

6. **Archive the dotfiles repository** with a README pointer to Workbench, for
   example `gh repo archive Sawmonabo/dotfiles` after pushing the pointer.

Claude Code and Codex keep the model and effort you choose: Workbench sets
neither, fills only missing defaults in `~/.claude/settings.json` and enforces
its policy keys (see [configuration ownership](chezmoi-local-overrides.md)).
