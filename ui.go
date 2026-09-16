package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

var (
	selected      = ansi.Style{}.BackgroundColor(ansi.RGBColor{R: 38, G: 61, B: 57}).String()
	workingFrames = [...]string{"◐", "◓", "◑", "◒"}
)

type picker struct {
	// agents is the latest successful inventory. Poll failures leave it intact.
	agents        []Agent
	cursor        int
	width, height int

	// git is a lifetime cache. gitPending prevents duplicate requests while a
	// lookup is still running.
	git        map[string]string
	gitPending map[string]bool

	preview    string
	previewTop int
	// previewBusy serializes capture-pane calls. follow pins the viewport to
	// the bottom until the user scrolls.
	follow, previewBusy, inputMode bool

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
type inputMsg struct {
	pane, content string
	err           error
}

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

func previewTickCmd(inputMode bool) tea.Cmd {
	delay := 300 * time.Millisecond
	if inputMode {
		delay = 100 * time.Millisecond
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return previewTick{} })
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

func sendKeyCmd(pane, key string, literal bool) tea.Cmd {
	return func() tea.Msg {
		args := []string{"send-keys", "-t", pane}
		if literal {
			args = append(args, "-l")
		}
		if _, err := tmux(append(args, key)...); err != nil {
			return inputMsg{pane: pane, err: err}
		}
		out, err := tmux("capture-pane", "-p", "-e", "-S", "-200", "-t", pane)
		return inputMsg{pane: pane, content: string(out), err: err}
	}
}

func pasteCmd(pane, content string) tea.Cmd {
	return func() tea.Msg {
		if err := tmuxInput(content, "load-buffer", "-"); err != nil {
			return inputMsg{pane: pane, err: err}
		}
		if _, err := tmux("paste-buffer", "-t", pane, "-p", "-d"); err != nil {
			return inputMsg{pane: pane, err: err}
		}
		out, err := tmux("capture-pane", "-p", "-e", "-S", "-200", "-t", pane)
		return inputMsg{pane: pane, content: string(out), err: err}
	}
}

func (p picker) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return p.updateKey(msg)
	case tea.PasteMsg:
		if pane := p.selectedPane(); p.inputMode && pane != "" && msg.Content != "" {
			return p, pasteCmd(pane, msg.Content)
		}
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
		return p, previewTickCmd(p.inputMode)
	case inputMsg:
		if msg.pane != p.selectedPane() {
			return p, nil
		}
		p.err = msg.err
		if msg.err == nil {
			p.preview = msg.content
		}
		return p, nil
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
	if p.inputMode {
		if key == "esc" {
			p.inputMode = false
			return p, nil
		}
		if pane := p.selectedPane(); pane != "" {
			if key, literal := inputKey(msg); key != "" {
				return p, sendKeyCmd(pane, key, literal)
			}
		}
		return p, nil
	}
	if key == "ctrl+c" {
		return p, tea.Quit
	}

	before := p.selectedPane()
	switch key {
	case "q", "esc":
		return p, tea.Quit
	case "i":
		if before != "" {
			p.inputMode = true
		}
	case "j", "down":
		if p.cursor < len(p.agents)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "G":
		if n := len(p.agents); n > 0 {
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

func inputKey(msg tea.KeyPressMsg) (string, bool) {
	switch msg.String() {
	case "enter":
		return "Enter", false
	case "backspace":
		return "BSpace", false
	case "tab":
		return "Tab", false
	case "up":
		return "Up", false
	case "down":
		return "Down", false
	case "left":
		return "Left", false
	case "right":
		return "Right", false
	}
	key := msg.Key()
	if key.Text != "" {
		return key.Text, true
	}
	// ponytail: modifiers are ignored; add explicit control-sequence mapping if
	// full terminal key forwarding becomes necessary.
	if unicode.IsPrint(key.Code) {
		return string(key.Code), true
	}
	return "", false
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

func (p picker) selectedPane() string {
	if p.cursor < 0 || p.cursor >= len(p.agents) {
		return ""
	}
	return p.agents[p.cursor].Pane
}

func (p *picker) setAgents(agents []Agent, selected string, oldCursor int) {
	slices.SortStableFunc(agents, func(a, b Agent) int { return statusRank(a.Status) - statusRank(b.Status) })
	p.agents = agents
	if len(agents) == 0 {
		p.cursor = 0
		return
	}
	// Pane IDs survive reordering and inventory refreshes. If a pane vanished,
	// the clamped old index selects the nearest surviving row.
	p.cursor = min(oldCursor, len(agents)-1)
	for i, agent := range agents {
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
	visible := p.agents
	numberWidth, statusWidth, sessionWidth, gitWidth, timeWidth := 3, 6, 7, 6, 8
	for i, agent := range visible {
		numberWidth = max(numberWidth, 2+len(strconv.Itoa(i+1)))
		statusWidth = max(statusWidth, ansi.StringWidth(statusLabel(agent.Status)))
		sessionWidth = max(sessionWidth, ansi.StringWidth(agent.Session))
		gitWidth = max(gitWidth, ansi.StringWidth(p.git[agent.Path]))
		timeWidth = max(timeWidth, ansi.StringWidth(activeFor(agent.Started)))
	}
	numberWidth += 3
	statusWidth += 3
	sessionWidth += 3
	gitWidth += 3
	timeWidth += 3
	columns := fit(bold+"  #"+reset, numberWidth) + fit(bold+"SESSION"+reset, sessionWidth) + fit(bold+"STATUS"+reset, statusWidth) + fit(bold+"BRANCH"+reset, gitWidth) + fit(bold+"TIME"+reset, timeWidth) + bold + "TITLE" + reset
	rows := make([]string, listHeight)
	start := max(0, p.cursor-listHeight+1)
	for row := range listHeight {
		i := start + row
		entry := ""
		isSelected := false
		if i < len(visible) {
			agent := visible[i]
			number := "  " + cyan + strconv.Itoa(i+1) + reset
			if i == p.cursor {
				number = "> " + cyan + strconv.Itoa(i+1) + reset
				isSelected = true
			}
			title := agent.Title
			if title == "" {
				title = "-"
			}
			entry = fit(number, numberWidth) + fit(agent.Session, sessionWidth) + fit(statusLabel(agent.Status), statusWidth) + fit(p.git[agent.Path], gitWidth) + fit(activeFor(agent.Started), timeWidth) + title
		} else if len(visible) == 0 && row == 0 {
			entry = "No registered agents"
		}
		rows[row] = fit(entry, p.width)
		if isSelected {
			rows[row] = selected + strings.ReplaceAll(rows[row], reset, reset+selected) + reset
		}
	}

	footer := "  j/k move  Enter switch  i input  q quit  Ctrl-U/D preview"
	border, label := dim, " Preview "
	if p.inputMode {
		footer = "Esc exit input mode"
		border, label = green, " INPUT "
	}
	innerWidth := max(0, p.width-2)
	preview := make([]string, previewHeight)
	for row := range previewHeight {
		preview[row] = border + "│" + reset + fit(p.previewLine(row, previewHeight), innerWidth) + border + "│" + reset
	}
	top := fit(border+"┌─"+reset+bold+label+reset+border+strings.Repeat("─", max(0, p.width-ansi.StringWidth(label)-3))+"┐"+reset, p.width)
	bottom := fit(border+"└"+strings.Repeat("─", innerWidth)+"┘"+reset, p.width)
	if p.err != nil {
		footer = p.err.Error()
	}
	content := fit(columns, p.width) + "\n" + strings.Join(rows, "\n") + "\n" +
		top + "\n" + strings.Join(preview, "\n") + "\n" + bottom + "\n" + fit(footer, p.width)
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
	preview = strings.TrimRight(preview, " \t\r\n")
	if preview == "" {
		return nil
	}
	return strings.Split(preview, "\n")
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
		return green + workingFrames[time.Now().UnixMilli()/500%int64(len(workingFrames))] + " working" + reset
	case "waiting":
		return red + "! blocked" + reset
	case "done":
		return cyan + "● done   " + reset
	default:
		return dim + "○ idle   " + reset
	}
}

func statusRank(status string) int {
	switch status {
	case "done":
		return 0
	case "waiting":
		return 1
	case "working":
		return 2
	default:
		return 3
	}
}

func activeFor(started int64) string {
	if started <= 0 {
		return "-"
	}
	elapsed := max(time.Duration(0), time.Since(time.UnixMilli(started))).Truncate(time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", elapsed/time.Hour, elapsed/time.Minute%60, elapsed/time.Second%60)
}

func gitInfoDisplay(path string) string {
	info := gitInfo(path)
	return yellow + info.branch + reset + "(" + green + "+" + strconv.Itoa(info.added) + reset + "/" + red + "-" + strconv.Itoa(info.removed) + reset + ")"
}
