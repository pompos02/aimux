package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Agent is the pane-local state reported an agent hook.
// Pane is the stable identity; Target and Path are display metadata.
type Agent struct {
	Pane   string
	Agent  string
	Status string
	Target string
	Path   string
}

// cappedBuffer accepts all writes while retaining only a bounded prefix. This
// lets child processes finish normally without allowing their output to grow
// memory without bound.
type cappedBuffer struct{ bytes.Buffer }

const outLimit = 4 << 20

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := outLimit - b.Len(); remaining > 0 {
		_, _ = b.Buffer.Write(p[:min(len(p), remaining)])
	}
	return len(p), nil
}

func output(ctx context.Context, name string, args ...string) ([]byte, error) {
	var out cappedBuffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &out // implicit call of cappedBuffer.Write()
	err := cmd.Run()
	return out.Bytes(), err
}

const stuckTimeout = 2

func tmux(args ...string) ([]byte, error) {
	// Every tmux call is user-facing or runs in a hook. A stuck server must not
	// block either indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), stuckTimeout*time.Second)
	defer cancel()
	return output(ctx, "tmux", args...)
}

func setAgent(agent, status string) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return
	}
	// Hook callers intentionally ignore tmux failures. Agent tools must keep
	// running even if their pane disappears during shutdown.
	tmux("set-option", "-p", "-t", pane, "@aimux_agent", agent)
	if status == "" {
		tmux("set-option", "-pu", "-t", pane, "@aimux_status")
	} else {
		tmux("set-option", "-p", "-t", pane, "@aimux_status", status)
	}
	if status == "waiting" || status == "done" {
		// A result observed while its pane is already focused is not unseen.
		focused, _ := tmux("display-message", "-p", "-t", pane, "#{&&:#{pane_active},#{&&:#{window_active},#{session_attached}}}")
		if strings.TrimSpace(string(focused)) == "1" {
			acknowledge(pane)
		}
	}
}

func clearAgent() {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return
	}
	tmux("set-option", "-pu", "-t", pane, "@aimux_agent")
	tmux("set-option", "-pu", "-t", pane, "@aimux_status")
}

func acknowledge(pane string) {
	if pane == "" {
		pane = os.Getenv("TMUX_PANE")
	}
	if pane == "" {
		return
	}
	status, _ := tmux("show-option", "-pqv", "-t", pane, "@aimux_status")
	// Working is durable until the agent reports another state. Only statuses
	// representing unseen user attention are acknowledged.
	if value := strings.TrimSpace(string(status)); value == "waiting" || value == "done" {
		tmux("set-option", "-pu", "-t", pane, "@aimux_status")
	}
}

func agents() []Agent {
	agents, _ := loadAgents()
	return agents
}

func loadAgents() ([]Agent, error) {
	// One list-panes call keeps count and dashboard refreshes cheap across all
	// sessions. A missing agent option marks a pane as unregistered.
	const format = "#{pane_id}\t#{?@aimux_agent,#{@aimux_agent},-}\t#{?@aimux_status,#{@aimux_status},idle}\t#{session_name}:#{window_index}.#{pane_index}\t#{pane_current_path}"
	out, err := tmux("list-panes", "-a", "-F", format)
	if err != nil {
		return nil, err
	}
	return parseAgents(string(out)), nil
}

func parseAgents(out string) []Agent {
	out = strings.TrimSuffix(out, "\n")
	if out == "" {
		return nil
	}
	var result []Agent
	for line := range strings.SplitSeq(out, "\n") {
		// SplitN preserves tabs in the final path field.
		fields := strings.SplitN(line, "\t", 5)
		if len(fields) != 5 || fields[1] == "-" {
			continue
		}
		if fields[2] == "" {
			fields[2] = "idle"
		}
		result = append(result, Agent{
			Pane: fields[0], Agent: fields[1], Status: fields[2], Target: fields[3], Path: fields[4],
		})
	}
	return result
}

func countAgents() string {
	return countSummary(agents())
}

func countSummary(agents []Agent) string {
	var working, waiting, idle, done int
	for _, agent := range agents {
		switch agent.Status {
		case "working":
			working++
		case "waiting":
			waiting++
		case "idle":
			idle++
		case "done":
			done++
		}
	}
	return fmt.Sprintf("[%d,%d,%d,%d]", working, waiting, idle, done)
}

type GitInfo struct {
	branch         string
	added, removed int
}

func gitInfo(path string) GitInfo {
	// The picker caches this result by path, so these commands run once per
	// newly observed working directory rather than on every poll.
	ctx, cancel := context.WithTimeout(context.Background(), stuckTimeout*time.Second)
	defer cancel()
	branch, _ := output(ctx, "git", "-C", path, "branch", "--show-current")
	if len(strings.TrimSpace(string(branch))) == 0 {
		branch, _ = output(ctx, "git", "-C", path, "rev-parse", "--short", "HEAD")
	}
	name := strings.TrimSpace(string(branch))
	if name == "" {
		return GitInfo{}
	}
	// show only git tracked changes
	diff, _ := output(ctx, "git", "-C", path, "diff", "--no-ext-diff", "--numstat", "HEAD")
	added, removed := parseNumstat(string(diff))
	return GitInfo{name, added, removed}
}

func parseNumstat(out string) (added, removed int) {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		a, errA := strconv.Atoi(fields[0])
		r, errR := strconv.Atoi(fields[1])
		if errA == nil && errR == nil {
			added += a
			removed += r
		}
	}
	return added, removed
}
