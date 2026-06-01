package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
)

func newProjectCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage the default project",
	}
	cmd.AddCommand(newProjectListCommand(state))
	cmd.AddCommand(newProjectSwitchCommand(state))
	return cmd
}

func newProjectListCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects you can access",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			page, err := state.chief.Projects.List(cmd.Context())
			if err != nil {
				return err
			}
			return state.printer.emit(page, func() {
				renderProjectTable(state.printer, page, state.resolved.Project)
			})
		},
	}
	return cmd
}

func renderProjectTable(p *printer, page *chief.ProjectPage, current string) {
	if len(page.Data) == 0 {
		p.line("no projects")
		return
	}
	headers := []string{"", "PROJECT ID", "NAME"}
	rows := make([][]string, 0, len(page.Data))
	for _, pr := range page.Data {
		marker := ""
		if pr.ProjectID == current {
			marker = "*"
		}
		rows = append(rows, []string{marker, pr.ProjectID, pr.Name})
	}
	p.table(headers, rows)
}

func newProjectSwitchCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "switch [id]",
		Short: "Set the default project for the active host",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := state.printer
			creds := state.creds
			baseURL := state.resolved.BaseURL

			page, err := state.chief.Projects.List(cmd.Context())
			if err != nil {
				return err
			}

			var id string
			switch {
			case len(args) == 1:
				id = args[0]
			case isInteractive():
				if len(page.Data) == 0 {
					return errors.New("no projects available")
				}
				for i, pr := range page.Data {
					p.line(fmt.Sprintf("%s %s  %s", p.subtle.Render(fmt.Sprintf("%d.", i+1)), pr.ProjectID, pr.Name))
				}
				idx, err := promptIndex(p, fmt.Sprintf("select project [1-%d]", len(page.Data)), len(page.Data))
				if err != nil {
					return err
				}
				id = page.Data[idx].ProjectID
			default:
				return errors.New("provide a project id (non-interactive)")
			}

			var name string
			var found bool
			for _, pr := range page.Data {
				if pr.ProjectID == id {
					name, found = pr.Name, true
					break
				}
			}
			if !found {
				ids := make([]string, 0, len(page.Data))
				for _, pr := range page.Data {
					ids = append(ids, pr.ProjectID)
				}
				return fmt.Errorf("project %q not found; valid ids: %s", id, strings.Join(ids, ", "))
			}

			h := creds.ensureHost(baseURL)
			h.Project = id
			creds.Current = baseURL
			if err := creds.save(); err != nil {
				return err
			}

			if p.json {
				return p.writeJSON(map[string]any{
					"base_url": baseURL,
					"project":  id,
					"name":     name,
				})
			}
			p.line(fmt.Sprintf("%s default project set to %s (%s) for %s", p.ok.Render("✓"), id, name, baseURL))
			return nil
		},
	}
	return cmd
}
