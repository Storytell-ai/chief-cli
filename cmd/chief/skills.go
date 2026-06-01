package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
)

func newSkillsCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage skills",
	}
	cmd.AddCommand(newSkillsCreateCommand(state))
	cmd.AddCommand(newSkillsListCommand(state))
	cmd.AddCommand(newSkillsGetCommand(state))
	cmd.AddCommand(newSkillsUpdateCommand(state))
	cmd.AddCommand(newDeleteCommand(state, "skill", func(ctx context.Context, id string) error {
		return state.chief.Skills.Delete(ctx, id)
	}))
	cmd.AddCommand(newToggleCommand(state, "enable", "skill", func(ctx context.Context, id string) (any, error) {
		return state.chief.Skills.Enable(ctx, id)
	}))
	cmd.AddCommand(newToggleCommand(state, "disable", "skill", func(ctx context.Context, id string) (any, error) {
		return state.chief.Skills.Disable(ctx, id)
	}))
	return cmd
}

func newSkillsCreateCommand(state *app) *cobra.Command {
	var (
		displayName string
		description string
		content     string
		icon        string
		scope       string
		category    string
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a skill",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if content == "" {
				return errors.New("--content is required")
			}
			skill, err := state.chief.Skills.Create(cmd.Context(), &chief.CreateSkillRequest{
				Name:        args[0],
				DisplayName: displayName,
				Description: description,
				Content:     content,
				Icon:        icon,
				Scope:       scope,
				Category:    category,
			})
			if err != nil {
				return err
			}
			return state.printer.emit(skill, func() { printSkillSummary(state.printer, skill) })
		},
	}

	cmd.Flags().StringVar(&displayName, "display-name", "", "human-readable name")
	cmd.Flags().StringVar(&description, "description", "", "what the skill does")
	cmd.Flags().StringVar(&content, "content", "", "skill body (required)")
	cmd.Flags().StringVar(&icon, "icon", "", "icon name")
	cmd.Flags().StringVar(&scope, "scope", "", "scope: project or user")
	cmd.Flags().StringVar(&category, "category", "", "category: skill or persona")
	return cmd
}

func newSkillsListCommand(state *app) *cobra.Command {
	f := &pagingFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List skills in the project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			page, err := state.chief.Skills.List(cmd.Context(), f.options()...)
			if err != nil {
				return err
			}
			return state.printer.emit(page, func() { renderSkillTable(state.printer, page) })
		},
	}

	f.register(cmd, "skill", "skills")
	return cmd
}

func renderSkillTable(p *printer, page *chief.SkillPage) {
	if len(page.Data) == 0 {
		p.line("no skills")
		return
	}

	headers := []string{"ID", "NAME", "SCOPE", "CATEGORY", "ENABLED"}
	rows := make([][]string, 0, len(page.Data))
	for _, s := range page.Data {
		rows = append(rows, []string{
			s.SkillID,
			s.Name,
			s.Scope,
			s.Category,
			yesNo(s.Enabled),
		})
	}
	p.table(headers, rows)
}

func newSkillsGetCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a single skill by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			skill, err := state.chief.Skills.Get(cmd.Context(), args[0])
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("skill %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(skill, func() { printSkillDetail(state.printer, skill) })
		},
	}
	return cmd
}

func newSkillsUpdateCommand(state *app) *cobra.Command {
	var (
		name        string
		displayName string
		description string
		content     string
		icon        string
		category    string
	)

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a skill",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &chief.UpdateSkillRequest{}
			if cmd.Flags().Changed("name") {
				req.Name = &name
			}
			if cmd.Flags().Changed("display-name") {
				req.DisplayName = &displayName
			}
			if cmd.Flags().Changed("description") {
				req.Description = &description
			}
			if cmd.Flags().Changed("content") {
				req.Content = &content
			}
			if cmd.Flags().Changed("icon") {
				req.Icon = &icon
			}
			if cmd.Flags().Changed("category") {
				req.Category = &category
			}
			skill, err := state.chief.Skills.Update(cmd.Context(), args[0], req)
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("skill %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(skill, func() { printSkillSummary(state.printer, skill) })
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "new skill name")
	cmd.Flags().StringVar(&displayName, "display-name", "", "human-readable name")
	cmd.Flags().StringVar(&description, "description", "", "what the skill does")
	cmd.Flags().StringVar(&content, "content", "", "skill body")
	cmd.Flags().StringVar(&icon, "icon", "", "icon name")
	cmd.Flags().StringVar(&category, "category", "", "category: skill or persona")
	return cmd
}

func printSkillSummary(p *printer, s *chief.SkillResponse) {
	p.kv("Skill ID", s.SkillID)
	p.kv("Name", s.Name)
	p.kv("Scope", s.Scope)
	p.kv("Category", s.Category)
	p.kv("Enabled", yesNo(s.Enabled))
}

func printSkillDetail(p *printer, s *chief.SkillResponse) {
	p.kv("Skill ID", s.SkillID)
	p.kv("Name", s.Name)
	if s.DisplayName != "" {
		p.kv("Display name", s.DisplayName)
	}
	if s.Description != "" {
		p.kv("Description", s.Description)
	}
	p.kv("Scope", s.Scope)
	p.kv("Category", s.Category)
	if s.Icon != "" {
		p.kv("Icon", s.Icon)
	}
	p.kv("Enabled", yesNo(s.Enabled))
	p.kv("Content", s.Content)
}
