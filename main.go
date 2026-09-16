package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 2 {
		return
	}
	switch os.Args[1] {
	case "clear":
		clearAgent()
	case "list":
		for _, agent := range agents() {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n", agent.Pane, agent.Agent, agent.Status, agent.Target, agent.Path)
		}
	case "count":
		fmt.Println(countAgents())
	case "acknowledge":
		acknowledge("")
	default:
		if len(os.Args) >= 2 && os.Args[1] == "acknowledge" {
			if len(os.Args) == 3 {
				acknowledge(os.Args[2])
				return
			}
		}
		if len(os.Args) >= 3 && os.Args[1] == "set" {
			agent := os.Args[2]
			status := ""
			if len(os.Args) == 4 {
				status = os.Args[3]
			}
			if agent != "opencode" && agent != "copilot" {
				usage()
			}
			if status != "" &&
				status != "working" &&
				status != "waiting" &&
				status != "done" {
				usage()
			}

			setAgent(agent, status)
			return
		}

		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr,
		"usage: aimux set AGENT [working|waiting|done] | clear | acknowledge [PANE] | list | count | pick")
	os.Exit(2)
}
