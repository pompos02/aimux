# aimux

`aimux` is a tmux dashboard for coding agents. It lists registered agent panes,
shows their status and elapsed time, previews their output, and can forward
keyboard input to the selected pane.

## Install

Requires Go and tmux.

```sh
go build -o ~/.local/bin/aimux .
```

Make sure `~/.local/bin` is on the `PATH` used by your agents.

## Tmux

Add this to `~/.tmux.conf`:

```txt
# (Optional) Prefix + a opens the dashboard.
bind a display-popup -x C -y C -w 70% -h 70% -E "~/.local/bin/aimux pick"

# Completed output becomes idle when its pane is focused.
set-hook -g pane-focus-in 'run-shell -b "~/.local/bin/aimux acknowledge #{pane_id}"'
```

Optional `[working blocked idle done]` status-bar counts (colors are included):

```txt
set -g status-right '#(~/.local/bin/aimux count) ...rest of your configuration...'
```

Reload tmux with `tmux source-file ~/.tmux.conf`.

## Agent Hooks

Hooks must run inside the agent's tmux pane. They report this lifecycle:

```sh
aimux set idle     # registered, no unobserved output
aimux set working  # agent is running
aimux set blocked  # waiting for user input
aimux set done     # stopped with unobserved output
aimux clear        # agent exited
```

These transitions should be automatic and configured by hooks. A missing
`@aimux_status` means the pane is not registered. `aimux acknowledge` changes
only `done` to `idle`; blocked agents remain blocked until their agent resumes.

### OpenCode

Use a plugin in `~/.config/opencode/plugins/` and aggregate all sessions in the
pane. Report `working` while any session is `busy` or `retry`, `blocked` while
any permission or question remains unanswered, and `done` when all tracked
sessions are idle. Clear the pane from the plugin's `dispose` hook.

### GitHub Copilot CLI

Configure `~/.copilot/hooks/*.json` with these mappings:

| Event | Status |
| --- | --- |
| `sessionStart` | `idle` |
| `userPromptSubmitted`, `preToolUse` | `working` |
| `notification` matching `permission_prompt\|elicitation_dialog` | `blocked` |
| `agentStop` | `done` |
| `sessionEnd` | `aimux clear` |

Copilot's configuration hooks do not expose dialog completion. Add a user
extension at `~/.copilot/extensions/aimux/extension.mjs` that reports `working`
on `permission.completed`, `user_input.completed`, and `elicitation.completed`.

```js
import { execFile } from "node:child_process";
import { joinSession } from "@github/copilot-sdk/extension";

const session = await joinSession();
const working = () => execFile("aimux", ["set", "working"], () => {});

session.on("permission.completed", working);
session.on("user_input.completed", working);
session.on("elicitation.completed", working);
```

## Controls

| Key | Action |
| --- | --- |
| `j` / `k` | Select agent |
| `Enter` | Switch to its pane |
| `Ctrl+u` / `Ctrl+d` | Scroll preview |
| `i` | Forward input to the pane |
| `Esc` | Leave input mode |
| `q` | Quit |
