package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
)

func newSessionsCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Manage sessions",
	}
	cmd.AddCommand(newSessionsListCommand(state))
	cmd.AddCommand(newSessionsGetCommand(state))
	cmd.AddCommand(newSessionsUpdateCommand(state))
	cmd.AddCommand(newDeleteCommand(state, "session", func(ctx context.Context, id string) error {
		return state.chief.Sessions.Delete(ctx, id)
	}))
	return cmd
}

func newSessionsListCommand(state *app) *cobra.Command {
	f := &pagingFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sessions in the project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := state.chief.Sessions.List(cmd.Context(), f.options()...)
			if err != nil {
				return err
			}
			return state.printer.emit(list, func() { renderSessionTable(state.printer, list) })
		},
	}

	f.register(cmd, "session", "sessions")
	return cmd
}

func renderSessionTable(p *printer, list *chief.SessionPage) {
	if len(list.Data) == 0 {
		p.line("no sessions")
		return
	}

	headers := []string{"ID", "NAME", "STATE", "MODIFIED"}
	rows := make([][]string, 0, len(list.Data))
	for _, s := range list.Data {
		rows = append(rows, []string{
			s.SessionID,
			s.Name,
			strings.TrimPrefix(s.State.State, "session."),
			s.ModifiedAt.Format(time.RFC3339),
		})
	}
	p.table(headers, rows)
}

func newSessionsGetCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a single session by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			session, err := state.chief.Sessions.Get(cmd.Context(), args[0])
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("session %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(session, func() { printSessionSummary(state.printer, session) })
		},
	}
	return cmd
}

func newSessionsUpdateCommand(state *app) *cobra.Command {
	var (
		name        string
		description string
	)

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &chief.UpdateSessionRequest{}
			if cmd.Flags().Changed("name") {
				req.Name = &name
			}
			if cmd.Flags().Changed("description") {
				req.Description = &description
			}
			session, err := state.chief.Sessions.Update(cmd.Context(), args[0], req)
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("session %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(session, func() { printSessionSummary(state.printer, session) })
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "new session name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	return cmd
}

func printSessionSummary(p *printer, s *chief.SessionResponse) {
	p.kv("Session ID", s.SessionID)
	p.kv("Name", s.Name)
	if s.Description != "" {
		p.kv("Description", s.Description)
	}
	if s.Language != "" {
		p.kv("Language", s.Language)
	}
	printSessionState(p, s.State)
	p.kv("Created", s.CreatedAt.Format(time.RFC3339))
	p.kv("Modified", s.ModifiedAt.Format(time.RFC3339))
	printLiveSummary(p, s.LiveSummary)
	printWriteup(p, s.Summary, s.ActionItems)
}

// printSessionState pairs the lifecycle state with the timestamp of that same
// transition, which is the call's own clock rather than the row's Created.
func printSessionState(p *printer, st chief.SessionState) {
	if st.State != "" {
		line := strings.TrimPrefix(st.State, "session.")
		var at *time.Time
		switch st.State {
		case chief.SessionStateScheduled:
			at = st.ScheduledAt
		case chief.SessionStateStarted:
			at = st.StartedAt
		case chief.SessionStateEnded:
			at = st.EndedAt
		}
		if at != nil {
			line += p.subtle.Render(" " + at.Format(time.RFC3339))
		}
		p.kv("State", line)
	}
	if st.MeetingURL != "" {
		p.kv("Meeting", st.MeetingURL)
	}
}

// printWriteup renders the post-session summary, which stays empty until the
// session ends and is separate from the running live summary above it.
func printWriteup(p *printer, summary string, actionItems []string) {
	if summary != "" {
		p.line("")
		p.line(p.header.Render("Writeup"))
		p.markdown(summary)
	}
	if len(actionItems) > 0 {
		p.line("")
		p.line(p.header.Render("Action items"))
		for _, item := range actionItems {
			p.line("  • " + item)
		}
	}
}

// printLiveSummary renders the reconciled bullets of a session, grouped by topic
// with sub-points nested under their parent. A nil summary means the server
// predates the field, so it prints nothing at all rather than an empty section.
func printLiveSummary(p *printer, ls *chief.SessionLiveSummary) {
	if ls == nil {
		return
	}
	if len(ls.Items) == 0 {
		p.kv("Summary", p.subtle.Render("none yet"))
		return
	}

	if ls.Headline != "" {
		p.kv("Summary", ls.Headline)
	} else {
		p.line(p.key.Render("Summary:"))
	}

	// An item nests only under a parent that is itself top-level, so a dangling
	// or deeper reference falls back to its own topic instead of vanishing.
	topLevel := make(map[string]bool, len(ls.Items))
	for _, it := range ls.Items {
		// The empty id is excluded deliberately: registering it would make every
		// root item read as a child of it, and they would render nowhere.
		if it.ParentID == "" && it.ID != "" {
			topLevel[it.ID] = true
		}
	}

	var topics []string
	roots := make(map[string][]chief.SessionLiveSummaryItem)
	children := make(map[string][]chief.SessionLiveSummaryItem)
	for _, it := range ls.Items {
		if topLevel[it.ParentID] {
			children[it.ParentID] = append(children[it.ParentID], it)
			continue
		}
		if _, seen := roots[it.Topic]; !seen {
			topics = append(topics, it.Topic)
		}
		roots[it.Topic] = append(roots[it.Topic], it)
	}

	for _, topic := range topics {
		p.line("")
		if topic != "" {
			p.line("  " + p.header.Render(topic))
		}
		for _, it := range roots[topic] {
			p.line(summaryItemLine(p, it, 4))
			for _, child := range children[it.ID] {
				p.line(summaryItemLine(p, child, 8))
			}
		}
	}
}

// summaryItemLine formats one bullet. Todos carry their state in a checkbox;
// every other kind names itself, since a decision and a stray note read the same
// once the output is piped and loses its color.
func summaryItemLine(p *printer, it chief.SessionLiveSummaryItem, indent int) string {
	marker, state := "•", it.State
	var notes []string
	if it.Kind == chief.SummaryKindTodo {
		marker = "[ ]"
		if it.State == chief.SummaryStateDone {
			marker, state = "[x]", ""
		}
	} else {
		notes = append(notes, it.Kind)
	}
	if state != "" && state != chief.SummaryStateOpen {
		notes = append(notes, state)
	}
	if !it.Active {
		notes = append(notes, "set aside")
	}

	text := it.Text
	if !it.Active || it.State == chief.SummaryStateDismissed {
		text = p.subtle.Render(text)
	}

	line := fmt.Sprintf("%s%-3s %s", strings.Repeat(" ", indent), marker, text)
	if it.Owner != "" {
		line += p.subtle.Render(" — " + it.Owner)
	}
	if len(notes) > 0 {
		line += p.subtle.Render(" (" + strings.Join(notes, ", ") + ")")
	}
	return line
}
