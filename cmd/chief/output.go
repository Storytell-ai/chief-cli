package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"golang.org/x/term"
)

// printer owns all stdout writes for a command invocation, serializing them so
// styled lines from parallel uploads don't interleave. When live is set it also
// paints an animated in-place region of in-flight assets below the permanent
// result lines.
type printer struct {
	out  io.Writer
	err  io.Writer
	json bool
	live bool
	tty  bool

	mdRenderer *glamour.TermRenderer

	mu sync.Mutex

	liveActive     bool
	total          int
	done           int
	spinnerIdx     int
	width          int
	lastBlockLines int
	active         []*liveRow
	tickerStop     chan struct{}

	ok     lipgloss.Style
	fail   lipgloss.Style
	skip   lipgloss.Style
	key    lipgloss.Style
	header lipgloss.Style
	subtle lipgloss.Style
}

type liveRow struct {
	name  string
	state string
}

// newPrinter resolves the output mode from the flags and the terminal. Color
// and the live region need a TTY; --no-color drops color, --json drops both.
func newPrinter(asJSON, noColor bool) *printer {
	tty := term.IsTerminal(int(os.Stdout.Fd()))
	color := !noColor && !asJSON && tty
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}
	p := &printer{
		out:   os.Stdout,
		err:   os.Stderr,
		json:  asJSON,
		live:  tty && !asJSON,
		tty:   tty,
		width: width,
	}

	if color {
		bold := func(c string) lipgloss.Style {
			return lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(true)
		}
		p.ok = bold("10")
		p.fail = bold("9")
		p.skip = bold("11")
		p.key = bold("12")
		p.header = bold("13")
		p.subtle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	}
	return p
}

func (p *printer) line(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintln(p.out, s)
}

func (p *printer) errline(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintln(p.err, s)
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// startLive begins the in-place live region. It is a no-op off a terminal.
func (p *printer) startLive(total int) {
	if !p.live {
		return
	}
	p.mu.Lock()
	p.total = total
	p.liveActive = true
	p.tickerStop = make(chan struct{})
	p.renderLocked()
	p.mu.Unlock()

	go p.animate()
}

// addRow registers an in-flight asset and returns a handle for state updates,
// or nil off a terminal.
func (p *printer) addRow(name string) *liveRow {
	if !p.live {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	row := &liveRow{name: name, state: "uploading"}
	p.active = append(p.active, row)
	p.renderLocked()
	return row
}

// setRowState updates an in-flight asset's phase, such as "ingesting".
func (p *printer) setRowState(row *liveRow, state string) {
	if row == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.liveActive {
		return
	}
	row.state = state
	p.renderLocked()
}

// finishRow settles an asset: it prints permanent above the live region (or
// directly when not live) and drops the asset's row.
func (p *printer) finishRow(row *liveRow, permanent string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.liveActive {
		fmt.Fprintln(p.out, permanent)
		return
	}
	p.eraseBlockLocked()
	fmt.Fprintln(p.out, permanent)
	p.removeRowLocked(row)
	p.done++
	p.drawBlockLocked()
}

// stopLive ends the animation and erases the live region.
func (p *printer) stopLive() {
	p.mu.Lock()
	if !p.liveActive {
		p.mu.Unlock()
		return
	}
	p.liveActive = false
	p.eraseBlockLocked()
	p.mu.Unlock()
	close(p.tickerStop)
}

func (p *printer) animate() {
	t := time.NewTicker(120 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-p.tickerStop:
			return
		case <-t.C:
			p.mu.Lock()
			if p.liveActive {
				p.spinnerIdx++
				p.renderLocked()
			}
			p.mu.Unlock()
		}
	}
}

func (p *printer) removeRowLocked(row *liveRow) {
	for i, r := range p.active {
		if r == row {
			p.active = append(p.active[:i], p.active[i+1:]...)
			return
		}
	}
}

// renderLocked erases the prior live region and repaints it. Caller holds mu.
func (p *printer) renderLocked() {
	p.eraseBlockLocked()
	p.drawBlockLocked()
}

// eraseBlockLocked moves the cursor to the top of the live region and clears to
// the end of the screen. Caller holds mu.
func (p *printer) eraseBlockLocked() {
	if p.lastBlockLines > 0 {
		fmt.Fprintf(p.out, "\x1b[%dA", p.lastBlockLines)
	}
	fmt.Fprint(p.out, "\r\x1b[J")
	p.lastBlockLines = 0
}

// drawBlockLocked paints the in-flight rows and a progress footer. Caller holds
// mu.
func (p *printer) drawBlockLocked() {
	frame := spinnerFrames[p.spinnerIdx%len(spinnerFrames)]
	for _, row := range p.active {
		fmt.Fprintln(p.out, p.activeRowLine(frame, row.name, row.state))
	}
	footerFrame := p.key.Render(frame)
	fmt.Fprintf(p.out, "%s %d/%d complete\n", footerFrame, p.done, p.total)
	p.lastBlockLines = len(p.active) + 1
}

// activeRowLine formats one in-flight asset, truncating the name so the row
// can't wrap and throw off the cursor math.
func (p *printer) activeRowLine(frame, name, state string) string {
	avail := max(p.width-len([]rune(state))-4, 8)
	name = truncateName(name, avail)
	glyph, st := p.subtle.Render(frame), p.subtle.Render(state)
	return fmt.Sprintf("%s %s %s", glyph, name, st)
}

func truncateName(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	if limit <= 1 {
		return string(r[:limit])
	}
	return string(r[:limit-1]) + "…"
}

func (p *printer) kv(key, value string) {
	p.line(fmt.Sprintf("%s %s", p.key.Render(key+":"), value))
}

// table renders a borderless, left-aligned grid with a styled header row.
func (p *printer) table(headers []string, rows [][]string) {
	t := table.New().
		Border(lipgloss.Border{}).
		BorderTop(false).BorderBottom(false).BorderLeft(false).
		BorderRight(false).BorderColumn(false).BorderRow(false).BorderHeader(false).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return p.header.PaddingRight(2)
			}
			return lipgloss.NewStyle().PaddingRight(2)
		})
	p.line(t.String())
}

// emit writes v as JSON in --json mode, otherwise runs human for styled output.
// It centralizes the json-wins decision every command shares.
func (p *printer) emit(v any, human func()) error {
	if p.json {
		return p.writeJSON(v)
	}
	human()
	return nil
}

// writeJSON marshals v to stdout, indented on a TTY and compact when piped.
func (p *printer) writeJSON(v any) error {
	var (
		raw []byte
		err error
	)
	if p.tty {
		raw, err = json.MarshalIndent(v, "", "  ")
	} else {
		raw, err = json.Marshal(v)
	}
	if err != nil {
		return fmt.Errorf("encode json output: %w", err)
	}
	p.line(string(raw))
	return nil
}

// writeRawJSON pretty-prints already-encoded JSON on a TTY and emits it
// verbatim when piped.
func (p *printer) writeRawJSON(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	if !p.tty {
		p.line(string(raw))
		return nil
	}
	pretty, err := json.MarshalIndent(json.RawMessage(raw), "", "  ")
	if err != nil {
		p.line(string(raw))
		return nil
	}
	p.line(string(pretty))
	return nil
}

// markdown renders s as styled markdown on a TTY and prints it verbatim when
// piped or in --json mode. A render failure falls back to the raw text.
func (p *printer) markdown(s string) {
	if !p.tty || p.json {
		p.line(s)
		return
	}
	out, err := p.markdownRenderer().Render(s)
	if err != nil {
		p.line(s)
		return
	}
	p.line(strings.TrimRight(out, "\n"))
}

// markdownRenderer builds the glamour renderer once, theming it from the
// terminal background.
func (p *printer) markdownRenderer() *glamour.TermRenderer {
	if p.mdRenderer == nil {
		style := styles.DarkStyle
		if !lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
			style = styles.LightStyle
		}
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(style),
			glamour.WithWordWrap(p.width),
		)
		if err != nil {
			panic(fmt.Sprintf("glamour.NewTermRenderer: %v", err))
		}
		p.mdRenderer = r
	}
	return p.mdRenderer
}
