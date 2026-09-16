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

# Clear waiting/done when its pane is focused.
set-hook -g pane-focus-in 'run-shell -b "~/.local/bin/aimux acknowledge #{pane_id}"'
```

Optional status-bar counts:

```txt
set -g status-right '#(~/.local/bin/aimux count) .... rest of you confiruation
```

Reload tmux with `tmux source-file ~/.tmux.conf`.

## Agent Hooks

Hooks must run inside the agent's tmux pane. They report this lifecycle:

```sh
aimux set <agent_name>          # register an idle agent
aimux set <agent_name> working  # processing
aimux set <agent_name> waiting  # needs user input
aimux set <agent_name> done     # finished
aimux clear                     # agent exited
```

these should be automatic and configured by hooks

### OpenCode

Create `~/.config/opencode/plugins/aimux.ts`:

```ts
import type { Plugin } from '@opencode-ai/plugin'

export const Aimux: Plugin = async ({ $ }) => {
  const report = async (status = '') => {
    try {
      await $`aimux set opencode ${status}`.quiet()
    } catch {}
  }

  await report()

  return {
    event: async ({ event }) => {
      if (event.type === 'session.status') {
        await report(event.properties.status.type === 'busy' ? 'working' : 'done')
      }
      if (event.type === 'permission.asked' || event.type === 'question.asked') {
        await report('waiting')
      }
      if (event.type === 'permission.replied' || event.type === 'question.replied') {
        await report('working')
      }
      if (event.type === 'session.idle') await report('done')
    },
    dispose: async () => {
      try {
        await $`aimux clear`.quiet()
      } catch {}
    },
  }
}
```

### GitHub Copilot CLI

Create `~/.copilot/hooks/aimux.json`:

```json
{
  "version": 1,
  "hooks": {
    "sessionStart": [
      { "type": "command", "exec": "aimux", "args": ["set", "copilot"] }
    ],
    "userPromptSubmitted": [
      { "type": "command", "exec": "aimux", "args": ["set", "copilot", "working"] }
    ],
    "notification": [
      {
        "type": "command",
        "matcher": "permission_prompt|elicitation_dialog",
        "exec": "aimux",
        "args": ["set", "copilot", "waiting"]
      }
    ],
    "preToolUse": [
      { "type": "command", "exec": "aimux", "args": ["set", "copilot", "working"] }
    ],
    "agentStop": [
      { "type": "command", "exec": "aimux", "args": ["set", "copilot", "done"] }
    ],
    "sessionEnd": [
      { "type": "command", "exec": "aimux", "args": ["clear"] }
    ]
  }
}
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
