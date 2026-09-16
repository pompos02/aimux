package main

import (
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
	bold   = "\x1b[1m"
)

var selected = ansi.Style{}.BackgroundColor(ansi.RGBColor{R: 38, G: 61, B: 57}).String()

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
		targets[i] = status + " " + agent.Session + " " + p.git[agent.Path] + " " + agent.Title
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

func (p picker) panelHeights() (list, preview int) {
	available := max(2, p.height-4)
	list = max(1, available*30/100)
	return list, max(1, available-list)
}

func (p picker) bodyHeight() int {
	_, preview := p.panelHeights()
	return preview
}

func (p picker) View() tea.View {
	listHeight, previewHeight := p.panelHeights()
	visible := p.visibleAgents()
	remaining := max(0, p.width-25)
	sessionWidth := max(6, remaining/4)
	gitWidth := max(10, remaining*2/5)
	columns := fit(bold+"#"+reset, 4) + fit(bold+"STATUS"+reset, 11) + fit(bold+"SESSION"+reset, sessionWidth) + fit(bold+"BRANCH"+reset, gitWidth) + fit(bold+"TIME"+reset, 10) + bold + "TITLE" + reset
	rows := make([]string, listHeight)
	start := max(0, p.cursor-listHeight+1)
	for row := range listHeight {
		i := start + row
		entry := ""
		isSelected := false
		if i < len(visible) {
			agent := visible[i]
			number := "  " + strconv.Itoa(i+1)
			if i == p.cursor {
				number = "> " + strconv.Itoa(i+1)
				isSelected = true
			}
			title := agent.Title
			if title == "" {
				title = "-"
			}
			entry = fit(number, 4) + fit(statusLabel(agent.Status), 11) + fit(agent.Session, sessionWidth) + fit(p.git[agent.Path], gitWidth) + fit(dim+activeFor(agent.Started)+reset, 10) + title
		} else if len(visible) == 0 && row == 0 {
			entry = "No registered agents"
		}
		rows[row] = fit(entry, p.width)
		if isSelected {
			rows[row] = selected + strings.ReplaceAll(rows[row], reset, reset+selected) + reset
		}
	}

	preview := make([]string, previewHeight)
	for row := range previewHeight {
		preview[row] = fit(p.previewLine(row, previewHeight), p.width)
	}
	separator := strings.Repeat("─", max(0, p.width))
	footer := "j/k move  / filter  Enter switch  q quit  Ctrl-U/D preview"
	if p.filtering {
		footer = "/" + p.filter + "_"
	} else if p.filter != "" {
		footer = "/" + p.filter + "  Esc clear  Enter switch  q quit"
	}
	if p.err != nil {
		footer = p.err.Error()
	}
	content := fit(columns, p.width) + "\n" + strings.Join(rows, "\n") + "\n" +
		fit(bold+"# Preview"+reset, p.width) + "\n" + separator + "\n" + strings.Join(preview, "\n") + "\n" + fit(footer, p.width)
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

func activeFor(started int64) string {
	if started <= 0 {
		return "-"
	}
	return max(time.Duration(0), time.Since(time.UnixMilli(started))).Truncate(time.Second).String()
}

func gitInfoDisplay(path string) string {
	info := gitInfo(path)
	return yellow + info.branch + reset + "(" + green + "+" + strconv.Itoa(info.added) + reset + "/" + red + "-" + strconv.Itoa(info.removed) + reset + ")"
}
