package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
)

// followEvery bounds one wait of --follow, so that words keep coming out while
// someone talks without the pause a wait otherwise ends at.
const followEvery = 30 * time.Second

type captureWaitFlags struct {
	captureScopeFlags
	cursor      string
	captureID   string
	live        bool
	follow      bool
	timeout     time.Duration
	screenshots string
}

func newCapturesWaitCommand(state *app) *cobra.Command {
	f := &captureWaitFlags{}
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for your captures to change",
		Long: "wait blocks until nothing is being recorded any more, which is when who said what has settled, and prints what " +
			"changed since the cursor. If nothing is being recorded it prints that at once. With --live it prints what came as " +
			"soon as there is a pause. If it is interrupted or times out while something is still being recorded, it prints " +
			"none of the words, and waiting again brings them. Waiting again from the cursor it prints misses nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCapturesWait(cmd.Context(), state, f, followEvery)
		},
	}
	cmd.Flags().StringVar(&f.cursor, "cursor", "", "wait for what changes after this cursor (default from now)")
	cmd.Flags().StringVar(&f.captureID, "capture", "", "wait for this capture alone, in any project")
	cmd.Flags().BoolVar(&f.live, "live", false, "print what comes as it comes; who said it can still change")
	cmd.Flags().BoolVar(&f.follow, "follow", false, "keep printing what comes, one JSON object per line when piped (implies --live)")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 0, "stop waiting after this long, with an empty result if nothing came (0 waits for as long as it takes)")
	cmd.Flags().StringVar(&f.screenshots, "screenshots", "", "save new screenshots in this directory")
	f.register(cmd)
	return cmd
}

// captureUpdates is what a wait brought. Next is the command that goes on from
// it.
type captureUpdates struct {
	Notice string `json:"notice,omitempty"`
	*chief.CaptureChangesResponse
	Screenshots []savedScreenshot `json:"screenshots,omitempty"`
	Next        string            `json:"next,omitempty"`
}

type savedScreenshot struct {
	CaptureID string `json:"capture_id"`
	EntryID   string `json:"entry_id"`
	Path      string `json:"path,omitempty"`
	Error     string `json:"error,omitempty"`
}

func runCapturesWait(ctx context.Context, state *app, f *captureWaitFlags, every time.Duration) error {
	opts := chief.WaitOptions{Cursor: f.cursor, CaptureID: f.captureID, UntilStopped: !f.live && !f.follow}
	if f.captureID == "" {
		project, err := f.project(state)
		if err != nil {
			return err
		}
		opts.ProjectID = project
	}
	var dir *screenshotDir
	if f.screenshots != "" {
		var err error
		if dir, err = openScreenshotDir(f.screenshots); err != nil {
			return err
		}
		defer func() { _ = dir.root.Close() }()
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	// A second Ctrl-C ends the command even while it is still finishing up.
	context.AfterFunc(ctx, stop)
	waitCtx := ctx
	if f.timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, f.timeout)
		defer cancel()
	}

	if f.follow {
		return followCaptures(ctx, waitCtx, state, opts, dir, every)
	}
	page, err := state.chief.Captures.Wait(waitCtx, opts)
	if chief.IsGone(err) {
		return errors.New("changes as old as this cursor are no longer kept: read the captures again with " +
			"`chief captures read`, and wait from the cursor it prints")
	}
	if err != nil {
		return err
	}

	updates := captureUpdates{CaptureChangesResponse: page, Screenshots: saveScreenshots(ctx, state, dir, page), Next: f.next(state, page)}
	if len(page.Data) > 0 {
		updates.Notice = captureRules
		if len(page.Recording) > 0 {
			updates.Notice += " " + captureStillRecording
		}
	}
	return state.printer.emitPiped(updates, func() {
		p := state.printer
		switch {
		case page.Recording == nil:
			p.line("the Chief API did not answer in time")
		case len(page.Recording) > 0 && !f.live:
			p.line(fmt.Sprintf("still recording: %d entries so far, printed once it stops", page.Held))
		case len(page.Data) == 0:
			p.line("nothing came")
		}
		if page.Recording != nil && len(page.Recording) == 0 {
			p.line("nothing is being recorded")
		}
		renderUpdates(p, updates)
		p.line("")
		p.kv("Go on waiting", updates.Next)
	})
}

// next is the command that goes on from page. While nothing is being recorded it
// is one with --live: without it the command would return at once, and an agent
// told to run it would spin.
func (f *captureWaitFlags) next(state *app, page *chief.CaptureChangesResponse) string {
	args := []string{"chief captures wait"}
	if page.Cursor != "" {
		args = append(args, "--cursor "+page.Cursor)
	}
	switch {
	case f.captureID != "":
		args = append(args, "--capture "+f.captureID)
	case f.allProjects:
		args = append(args, "--all-projects")
	case state.resolved.ProjectSource == "flag" && state.resolved.Project != "":
		args = append(args, "--project "+state.resolved.Project)
	}
	stopped := page.Recording != nil && len(page.Recording) == 0 && !page.HasMore
	if f.live || stopped {
		args = append(args, "--live")
	}
	if f.timeout > 0 {
		args = append(args, "--timeout "+f.timeout.String())
	}
	if f.screenshots != "" {
		args = append(args, "--screenshots "+shellQuote(f.screenshots))
	}
	return strings.Join(args, " ")
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// followCaptures prints changes until waitCtx ends, and what is being recorded
// whenever that changes. A change that came before a cursor too old is lost, and
// said to be: it starts over from now. ctx outlives waitCtx's timeout, for the
// screenshots of what came last.
func followCaptures(
	ctx, waitCtx context.Context, state *app, opts chief.WaitOptions, dir *screenshotDir, every time.Duration,
) error {
	rules := captureRules
	var recording []string
	for waitCtx.Err() == nil {
		page, err := waitAtMost(waitCtx, state, opts, every)
		if chief.IsGone(err) && opts.Cursor != "" {
			opts.Cursor = ""
			const missed = "Changes as old as the cursor are no longer kept, so some were missed: read the captures again. Following from now."
			if err := emitLine(state.printer, map[string]string{"notice": missed}, func() { state.printer.line(missed) }); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		opts.Cursor = page.Cursor

		turned := recording != nil && page.Recording != nil && !slices.Equal(recording, page.Recording)
		if page.Recording != nil {
			recording = page.Recording
		}
		if len(page.Data) == 0 && !turned {
			continue
		}
		notice := rules
		switch {
		case len(page.Recording) > 0:
			notice = strings.TrimSpace(notice + " " + captureStillRecording)
		case turned:
			notice = strings.TrimSpace(notice + " Recording stopped: who said what should have settled. Go by the last version of each entry.")
		}
		updates := captureUpdates{Notice: notice, CaptureChangesResponse: page, Screenshots: saveScreenshots(ctx, state, dir, page)}
		err = emitLine(state.printer, updates, func() {
			if turned && len(page.Recording) == 0 {
				state.printer.line(state.printer.subtle.Render("recording stopped"))
			}
			if turned && len(page.Recording) > 0 {
				state.printer.line(state.printer.subtle.Render("recording " + strings.Join(page.Recording, ", ")))
			}
			renderUpdates(state.printer, updates)
		})
		if err != nil {
			return err
		}
		rules = ""
	}
	return nil
}

func waitAtMost(
	ctx context.Context, state *app, opts chief.WaitOptions, every time.Duration,
) (*chief.CaptureChangesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, every)
	defer cancel()
	return state.chief.Captures.Wait(ctx, opts)
}

// emitLine is emitPiped for one of many objects, each on a line of its own even
// on a terminal.
func emitLine(p *printer, v any, human func()) error {
	if p.json || !p.tty {
		return p.writeJSONLine(v)
	}
	human()
	return nil
}

// saveScreenshots saves the screenshots page brought in dir, unless that is nil,
// named by their entry ids. One that can't be saved is reported with its cause:
// the page's cursor is past it, so no later wait brings it.
func saveScreenshots(ctx context.Context, state *app, dir *screenshotDir, page *chief.CaptureChangesResponse) []savedScreenshot {
	if dir == nil {
		return nil
	}
	var saved []savedScreenshot
	for _, c := range page.Data {
		for _, e := range c.Entries {
			if e.Deleted || e.Kind != chief.CaptureEntryScreenshot {
				continue
			}
			shot := savedScreenshot{CaptureID: c.CaptureID, EntryID: e.EntryID}
			path, err := saveScreenshot(ctx, state, dir, c.CaptureID, e.EntryID)
			if err != nil {
				shot.Error = err.Error()
			}
			shot.Path = path
			saved = append(saved, shot)
		}
	}
	return saved
}

func saveScreenshot(ctx context.Context, state *app, dir *screenshotDir, captureID, entryID string) (string, error) {
	if !fileSafeID.MatchString(entryID) {
		return "", fmt.Errorf("entry id %q can't name a file", entryID)
	}
	data, _, err := state.chief.Captures.GetScreenshot(ctx, captureID, entryID)
	if err != nil {
		return "", err
	}
	return dir.save(entryID+screenshotExt, data)
}

func renderUpdates(p *printer, u captureUpdates) {
	if u.Notice != "" {
		p.line(p.subtle.Render(u.Notice))
	}
	for _, c := range u.Data {
		p.line("")
		if c.Deleted {
			p.line(p.header.Render("Deleted capture") + " " + p.subtle.Render(c.CaptureID))
			continue
		}
		p.line(p.header.Render(oneLine(c.Title)) + " " + p.subtle.Render(c.CaptureID))
		if len(c.Entries) == 0 {
			p.line("  changed, but none of its entries did: `chief captures read " + c.CaptureID + "` has them")
		}
		for _, e := range c.Entries {
			if e.Deleted {
				p.line("")
				p.line(p.subtle.Render("["+e.At.Format(time.RFC3339)+"]") + " " + p.key.Render("Deleted") + " " + p.subtle.Render(e.EntryID))
				continue
			}
			renderCaptureEntry(p, e.CaptureEntry)
		}
	}
	for _, s := range u.Screenshots {
		if s.Error != "" {
			p.line(fmt.Sprintf("%s screenshot %s not saved: %s", p.fail.Render("✗"), s.EntryID, oneLine(s.Error)))
			continue
		}
		p.line(fmt.Sprintf("%s screenshot %s saved as %s", p.ok.Render("✓"), s.EntryID, s.Path))
	}
}
