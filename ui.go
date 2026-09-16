package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
)

const (
	reset  = "\x1b[0m"
	green  = "\x1b[32m"
	red    = "\x1b[31m"
	yellow = "\x1b[33m"
	cyan   = "\x1b[36m"
	dim    = "\x1b[2m"
)

type picker struct {
	// agents is the latest successful inventory. Poll failures leave it intact.
	agents []Agent
	// cursor indexes visibleAgents, not agents, because fuzzy search reorders it.
	cursor        int
	width, height int

	filter    string
	filtering bool

	// git is a lifetime cache. gitPending prevents duplicate requests while a
	// lookup is still running.
	git        map[string]string
	gitPending map[string]bool

	preview    string
	previewTop int
	// previewBusy serializes capture-pane calls. follow pins the viewport to
	// the bottom until the user scrolls.
	follow, previewBusy bool

	err error
}

type inventoryMsg struct {
	agents []Agent
	err    error
}
type inventoryTick struct{}
type previewMsg struct {
	pane, content string
	err           error
}
type previewTick struct{}
type gitMsg struct{ path, info string }
type switchMsg struct{ err error }

func pick() error {
	final, err := tea.NewProgram(newPicker()).Run()
	if err != nil {
		return err
	}
	return final.(picker).err
}

func newPicker() picker {
	return picker{width: 100, height: 24, git: map[string]string{}, gitPending: map[string]bool{}, follow: true}
}

func (p picker) Init() tea.Cmd { return loadInventoryCmd }

func loadInventoryCmd() tea.Msg {
	agents, err := loadAgents()
	return inventoryMsg{agents, err}
}

func inventoryTickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return inventoryTick{} })
}

func captureCmd(pane string) tea.Cmd {
	return func() tea.Msg {
		out, err := tmux("capture-pane", "-p", "-e", "-S", "-200", "-t", pane)
		return previewMsg{pane, string(out), err}
	}
}

func previewTickCmd() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return previewTick{} })
}

func gitCmd(path string) tea.Cmd {
	return func() tea.Msg { return gitMsg{path, gitInfoDisplay(path)} }
}

func switchPaneCmd(pane string) tea.Cmd {
	return func() tea.Msg {
		_, err := tmux("switch-client", "-t", pane)
		return switchMsg{err}
	}
}

func (p picker) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return p.updateKey(msg)
	case inventoryTick:
		return p, loadInventoryCmd
	case inventoryMsg:
		// Schedule the next poll only after this one completes. This keeps slow
		// tmux servers from accumulating overlapping list-panes processes.
		cmds := []tea.Cmd{inventoryTickCmd()}
		if msg.err != nil {
			p.err = msg.err
			return p, tea.Batch(cmds...)
		}
		selected := p.selectedPane()
		p.setAgents(msg.agents, selected, p.cursor)
		p.err = nil
		for _, agent := range msg.agents {
			if agent.Path != "" && !p.gitPending[agent.Path] {
				p.gitPending[agent.Path] = true
				cmds = append(cmds, gitCmd(agent.Path))
			}
		}
		if p.selectedPane() != selected {
			p.resetPreview()
			cmds = append(cmds, p.requestPreview())
		}
		return p, tea.Batch(cmds...)
	case previewTick:
		cmd := p.requestPreview()
		return p, cmd
	case previewMsg:
		p.previewBusy = false
		// Selection can change while capture-pane is running. Never render output
		// from the pane that was previously selected.
		if msg.pane != p.selectedPane() {
			cmd := p.requestPreview()
			return p, cmd
		}
		if msg.err != nil {
			p.err = msg.err
		} else if msg.content != p.preview {
			p.preview = msg.content
			p.err = nil
		}
		return p, previewTickCmd()
	case gitMsg:
		selected := p.selectedPane()
		p.git[msg.path] = msg.info
		p.setAgents(p.agents, selected, p.cursor)
		if p.selectedPane() != selected {
			p.resetPreview()
			cmd := p.requestPreview()
			return p, cmd
		}
	case switchMsg:
		p.err = msg.err
		return p, tea.Quit
	}
	return p, nil
}

func (p picker) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return p, tea.Quit
	}
	if p.filtering {
		selected, cursor := p.selectedPane(), p.cursor
		switch key {
		case "esc":
			p.filter, p.filtering = "", false
		case "enter":
			p.filtering = false
			if pane := p.selectedPane(); pane != "" {
				return p, switchPaneCmd(pane)
			}
		case "backspace":
			runes := []rune(p.filter)
			if len(runes) > 0 {
				p.filter = string(runes[:len(runes)-1])
			}
		default:
			p.filter += msg.Key().Text
		}
		p.setAgents(p.agents, selected, cursor)
		if p.selectedPane() != selected {
			p.resetPreview()
			cmd := p.requestPreview()
			return p, cmd
		}
		return p, nil
	}

	before := p.selectedPane()
	switch key {
	case "q":
		return p, tea.Quit
	case "esc":
		if p.filter == "" {
			return p, tea.Quit
		}
		p.filter = ""
		p.setAgents(p.agents, before, p.cursor)
	case "/":
		p.filtering = true
	case "j", "down":
		if p.cursor < len(p.visibleAgents())-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "G":
		if n := len(p.visibleAgents()); n > 0 {
			p.cursor = n - 1
		}
	case "ctrl+u":
		p.scrollPreview(-max(1, p.bodyHeight()/2))
	case "ctrl+d":
		p.scrollPreview(max(1, p.bodyHeight()/2))
	case "enter":
		if before != "" {
			return p, switchPaneCmd(before)
		}
	}
	if p.selectedPane() != before {
		p.resetPreview()
		cmd := p.requestPreview()
		return p, cmd
	}
	return p, nil
}

func (p *picker) requestPreview() tea.Cmd {
	pane := p.selectedPane()
	if pane == "" || p.previewBusy {
		return nil
	}
	p.previewBusy = true
	return captureCmd(pane)
}

func (p *picker) resetPreview() {
	p.preview, p.previewTop, p.follow = "", 0, true
}

func (p picker) visibleAgents() []Agent {
	query := strings.TrimSpace(p.filter)
	if query == "" {
		return p.agents
	}
	targets := make([]string, len(p.agents))
	for i, agent := range p.agents {
		status := agent.Status
		if status == "waiting" {
			status += " blocked"
		}
		targets[i] = status + " " + projectName(agent.Path) + " " + p.git[agent.Path] + " " + agent.Target
	}
	// fuzzy.Find both filters and ranks, giving fzf-like ordering without
	// coupling the picker to a full list widget.
	matches := fuzzy.Find(query, targets)
	visible := make([]Agent, len(matches))
	for i, match := range matches {
		visible[i] = p.agents[match.Index]
	}
	return visible
}

func (p picker) selectedPane() string {
	agents := p.visibleAgents()
	if p.cursor < 0 || p.cursor >= len(agents) {
		return ""
	}
	return agents[p.cursor].Pane
}

func (p *picker) setAgents(agents []Agent, selected string, oldCursor int) {
	p.agents = agents
	visible := p.visibleAgents()
	if len(visible) == 0 {
		p.cursor = 0
		return
	}
	// Pane IDs survive reordering and inventory refreshes. If a pane vanished,
	// the clamped old index selects the nearest surviving row.
	p.cursor = min(oldCursor, len(visible)-1)
	for i, agent := range visible {
		if agent.Pane == selected {
			p.cursor = i
			return
		}
	}
}

func (p *picker) scrollPreview(delta int) {
	lines := previewLines(p.preview)
	maxTop := max(0, len(lines)-p.bodyHeight())
	if p.follow {
		// Materialize the current bottom position before leaving follow mode.
		p.previewTop = maxTop
		p.follow = false
	}
	p.previewTop = max(0, min(maxTop, p.previewTop+delta))
	if p.previewTop == maxTop && delta > 0 {
		p.follow = true
	}
}

func (p picker) bodyHeight() int { return max(1, p.height-3) }

func (p picker) View() tea.View {
	bodyHeight := p.bodyHeight()
	visible := p.visibleAgents()
	// Below 80 cells the preview costs more readability than it provides.
	wide := p.width >= 80
	leftWidth := p.width
	if wide {
		leftWidth = max(30, p.width*35/100)
	}
	rightWidth := max(1, p.width-leftWidth-3)
	rows := make([]string, bodyHeight)
	start := max(0, p.cursor-bodyHeight+1)
	for row := range bodyHeight {
		i := start + row
		left := ""
		if i < len(visible) {
			agent := visible[i]
			cursor := "  "
			if i == p.cursor {
				cursor = "> "
			}
			left = cursor + statusLabel(agent.Status) + "  " + projectName(agent.Path) + "  " + p.git[agent.Path] + "  " + agent.Target
		} else if len(visible) == 0 && row == 0 {
			left = "No registered agents"
		}
		if wide {
			preview := p.previewLine(row, bodyHeight)
			rows[row] = fit(left, leftWidth) + " │ " + fit(preview, rightWidth)
		} else {
			rows[row] = fit(left, leftWidth)
		}
	}

	header := fit("Agents", leftWidth)
	separator := strings.Repeat("─", max(0, leftWidth))
	if wide {
		header += " │ " + fit("Preview", rightWidth)
		separator += "─┼─" + strings.Repeat("─", rightWidth)
	}
	footer := "j/k move  / filter  Enter switch  q quit  Ctrl-U/D preview"
	if p.filtering {
		footer = "/" + p.filter + "_"
	} else if p.filter != "" {
		footer = "/" + p.filter + "  Esc clear  Enter switch  q quit"
	}
	if p.err != nil {
		footer = p.err.Error()
	}
	content := header + "\n" + separator + "\n" + strings.Join(rows, "\n") + "\n" + fit(footer, p.width)
	view := tea.NewView(content)
	view.AltScreen = true
	return view
}

func (p picker) previewLine(row, height int) string {
	lines := previewLines(p.preview)
	start := p.previewTop
	if p.follow {
		start = max(0, len(lines)-height)
	}
	i := start + row
	if i >= len(lines) {
		return ""
	}
	return lines[i] + reset
}

func previewLines(preview string) []string {
	if preview == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(preview, "\n"), "\n")
}

func fit(value string, width int) string {
	if width <= 0 {
		return ""
	}
	// Truncate by display cells and terminate pane-provided styles before the
	// surrounding UI is rendered.
	value = ansi.Truncate(value, width, "") + reset
	return value + strings.Repeat(" ", max(0, width-ansi.StringWidth(value)))
}

func statusLabel(status string) string {
	switch status {
	case "working":
		return green + "● working" + reset
	case "waiting":
		return yellow + "! blocked" + reset
	case "done":
		return cyan + "✓ done   " + reset
	default:
		return dim + "○ idle   " + reset
	}
}

func projectName(path string) string {
	if path == "" {
		return "-"
	}
	return filepath.Base(path)
}

func gitInfoDisplay(path string) string {
	info := gitInfo(path)
	return yellow + info.branch + green + " +" + strconv.Itoa(info.added) + red + " -" + strconv.Itoa(info.removed) + reset
}
