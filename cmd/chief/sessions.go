package main

import (
	"context"
	"fmt"
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

	headers := []string{"ID", "NAME", "MODIFIED"}
	rows := make([][]string, 0, len(list.Data))
	for _, s := range list.Data {
		rows = append(rows, []string{
			s.SessionID,
			s.Name,
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
	p.kv("Created", s.CreatedAt.Format(time.RFC3339))
	p.kv("Modified", s.ModifiedAt.Format(time.RFC3339))
}
