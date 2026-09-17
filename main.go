// Command aimux tracks coding agents in tmux panes and provides a live
// watchroom for their status and output.
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
		fmt.Printf("%s\t%s\t%s\t%s\n", agent.Pane, agent.Status, agent.Target, agent.Path)
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
	if len(args) != 1 {
		usage()
	}
	switch args[0] {
	case "working", "blocked", "idle", "done":
	default:
		usage()
	}
	setAgent(args[0])
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
		"usage: aimux set working|blocked|idle|done | clear | acknowledge [PANE] | list | count | pick")
	os.Exit(2)
}
