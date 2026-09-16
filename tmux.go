package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
	"strconv"
)

type Agent struct {
	Pane, Agent, Status, Target, Path string
}

// Run a tmux comamnd with a timeout
func tmux(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", args...).Output()
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

	statusBytes, _ := tmux("show-option", "-pqv", "-t", pane, "@aimux_status")
	status := strings.TrimSpace(string(statusBytes))

	if status == "waiting" || status == "done" {
		tmux("set-option", "-pu", "-t", pane, "@aimux_status")
	}
}

func agents() []Agent {
	const format = "#{pane_id}\t#{?@aimux_agent,#{@aimux_agent},-}\t#{?@aimux_status,#{@aimux_status},idle}\t#{session_name}:#{window_index}.#{pane_index}\t#{pane_current_path}"

	out, err := tmux("list-panes", "-a", "-F", format)
	if err != nil {
		return nil
	}

	var result []Agent
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || fields[1] == "-" {
			continue
		}
		result = append(result, Agent{
			Pane:   fields[0],
			Agent:  fields[1],
			Status: fields[2],
			Target: fields[3],
			Path:   fields[4],
		})
	}
	return result
}

func countAgents() string {
	counts := map[string]int{
		"working": 0,
		"waiting": 0,
		"idle":    0,
		"done":    0,
	}

	for _, agent := range agents() {
		counts[agent.Status]++
	}

	return "[" +
		strconv.Itoa(counts["working"]) + " " +
		strconv.Itoa(counts["waiting"]) + " " +
		strconv.Itoa(counts["idle"]) + " " +
		strconv.Itoa(counts["done"]) + "]"
}
