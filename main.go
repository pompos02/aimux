package main

import (
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
	}

	switch args[0] {
	case "clear":
		runClear(args[1:])
	case "list":
		runList(args[1:])
	case "count":
		runCount(args[1:])
	case "acknowledge":
		runAcknowledge(args[1:])
	case "set":
		runSet(args[1:])
	case "pick":
		runPick(args[1:])
	default:
		usage()
	}
}

func runClear(args []string) {
	if len(args) != 0 {
		usage()
	}
	clearAgent()
}

func runList(args []string) {
	if len(args) != 0 {
		usage()
	}
	for _, agent := range agents() {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", agent.Pane, agent.Agent, agent.Status, agent.Target, agent.Path)
	}
}

func runCount(args []string) {
	if len(args) != 0 {
		usage()
	}
	fmt.Println(countAgents())
}

func runAcknowledge(args []string) {
	if len(args) > 1 {
		usage()
	}
	if len(args) == 1 {
		acknowledge(args[0])
		return
	}
	acknowledge("")
}

func runSet(args []string) {
	if len(args) < 1 || len(args) > 2 {
		usage()
	}
	status := ""
	if len(args) == 2 {
		status = args[1]
	}
	switch args[0] {
	case "opencode", "copilot":
	default:
		usage()
	}
	switch status {
	case "", "working", "waiting", "done":
	default:
		usage()
	}
	setAgent(args[0], status)
}

func runPick(args []string) {
	if len(args) != 0 {
		usage()
	}
	if err := pick(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr,
		"usage: aimux set AGENT [working|waiting|done] | clear | acknowledge [PANE] | list | count | pick")
	os.Exit(2)
}
