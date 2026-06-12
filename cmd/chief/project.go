package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	cmd.AddCommand(newProjectCreateCommand(state))
	cmd.AddCommand(newProjectUpdateCommand(state))
	cmd.AddCommand(newProjectMembersCommand(state))
	cmd.AddCommand(newProjectInvitationsCommand(state))
	return cmd
}

// currentProject is the project ID commands act on: the default set by
// `project switch`, or the --project flag / CHIEF_PROJECT_ID override.
func currentProject(state *app) (string, error) {
	if state.resolved.Project == "" {
		return "", errors.New("no project selected; run `chief project switch` or pass --project")
	}
	return state.resolved.Project, nil
}

func printProjectDetail(p *printer, project *chief.Project) {
	p.kv("Project ID", project.ProjectID)
	p.kv("Name", project.Name)
	if project.Description != "" {
		p.kv("Description", project.Description)
	}
}

func newProjectCreateCommand(state *app) *cobra.Command {
	var description string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := state.chief.Projects.Create(cmd.Context(), &chief.CreateProjectRequest{
				Name:        args[0],
				Description: description,
			})
			if err != nil {
				return err
			}
			return state.printer.emit(project, func() { printProjectDetail(state.printer, project) })
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "project description")
	return cmd
}

func newProjectUpdateCommand(state *app) *cobra.Command {
	var (
		name        string
		description string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the project's name and description",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			projectID, err := currentProject(state)
			if err != nil {
				return err
			}
			project, err := state.chief.Projects.Update(cmd.Context(), projectID, &chief.UpdateProjectRequest{
				Name:        name,
				Description: description,
			})
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("project %q not found", projectID)
				}
				return err
			}
			return state.printer.emit(project, func() { printProjectDetail(state.printer, project) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new project name")
	cmd.Flags().StringVar(&description, "description", "", "project description (empty clears it)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newProjectMembersCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "members",
		Short: "List the project's members",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			projectID, err := currentProject(state)
			if err != nil {
				return err
			}
			list, err := state.chief.Projects.ListMembers(cmd.Context(), projectID)
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("project %q not found", projectID)
				}
				return err
			}
			return state.printer.emit(list, func() {
				if len(list.Data) == 0 {
					state.printer.line("no members")
					return
				}
				rows := make([][]string, 0, len(list.Data))
				for _, m := range list.Data {
					rows = append(rows, []string{m.UserID, m.Email, m.Name, m.Role, m.AddedAt.Format(time.RFC3339)})
				}
				state.printer.table([]string{"USER ID", "EMAIL", "NAME", "ROLE", "JOINED"}, rows)
			})
		},
	}
	return cmd
}

func newProjectInvitationsCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invitations",
		Short: "Manage project invitations",
	}
	cmd.AddCommand(newProjectInvitationsCreateCommand(state))
	cmd.AddCommand(newProjectInvitationsDeleteCommand(state))
	return cmd
}

func newProjectInvitationsCreateCommand(state *app) *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "create <email>",
		Short: "Invite a user to the project by email",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch role {
			case "owner", "collaborator", "reader":
			default:
				return fmt.Errorf("invalid role %q: must be owner, collaborator, or reader", role)
			}
			projectID, err := currentProject(state)
			if err != nil {
				return err
			}
			invitation, err := state.chief.Projects.CreateInvitation(cmd.Context(), projectID, &chief.CreateProjectInvitationRequest{
				Email: args[0],
				Role:  role,
			})
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("project %q not found", projectID)
				}
				return err
			}
			return state.printer.emit(invitation, func() {
				p := state.printer
				p.kv("Invitation ID", invitation.InvitationID)
				p.kv("Email", invitation.Email)
				p.kv("Role", invitation.Role)
				if invitation.URL != "" {
					p.kv("Invite URL", invitation.URL)
				}
				p.kv("Created", invitation.CreatedAt.Format(time.RFC3339))
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", "collaborator", "role to grant: owner, collaborator, or reader")
	return cmd
}

func newProjectInvitationsDeleteCommand(state *app) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <invitation-id>",
		Short: "Revoke a pending invitation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := currentProject(state)
			if err != nil {
				return err
			}
			return confirmAndDelete(cmd.Context(), state, force, "invitation", args[0], func(ctx context.Context, id string) error {
				return state.chief.Projects.DeleteInvitation(ctx, projectID, id)
			})
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip the confirmation prompt")
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
