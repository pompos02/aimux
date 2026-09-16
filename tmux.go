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

type Agent struct {
	Pane, Agent, Status, Target, Path string
}

type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	const limit = 4 << 20
	n := len(p)
	if remaining := limit - b.Len(); remaining > 0 {
		_, _ = b.Buffer.Write(p[:min(len(p), remaining)])
	}
	return n, nil
}

func output(ctx context.Context, name string, args ...string) ([]byte, error) {
	// ponytail: 4 MiB bounds subprocess output; raise it if 200 tmux rows ever exceed that.
	var out cappedBuffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), err
}

func tmux(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return output(ctx, "tmux", args...)
}

func setAgent(agent, status string) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return
	}
	tmux("set-option", "-p", "-t", pane, "@aimux_agent", agent)
	if status == "" {
		tmux("set-option", "-pu", "-t", pane, "@aimux_status")
	} else {
		tmux("set-option", "-p", "-t", pane, "@aimux_status", status)
	}
	if status == "waiting" || status == "done" {
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
	if value := strings.TrimSpace(string(status)); value == "waiting" || value == "done" {
		tmux("set-option", "-pu", "-t", pane, "@aimux_status")
	}
}

func agents() []Agent {
	agents, _ := loadAgents()
	return agents
}

func loadAgents() ([]Agent, error) {
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

func gitInfo(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	branch, _ := output(ctx, "git", "-C", path, "branch", "--show-current")
	if len(strings.TrimSpace(string(branch))) == 0 {
		branch, _ = output(ctx, "git", "-C", path, "rev-parse", "--short", "HEAD")
	}
	name := strings.TrimSpace(string(branch))
	if name == "" {
		return ""
	}
	// ponytail: tracked changes only; add an untracked scan if it becomes useful.
	diff, _ := output(ctx, "git", "-C", path, "diff", "--no-ext-diff", "--numstat", "HEAD")
	added, removed := parseNumstat(string(diff))
	return name + " +" + strconv.Itoa(added) + "/-" + strconv.Itoa(removed)
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
