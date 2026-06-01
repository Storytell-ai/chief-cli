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

func newActionsCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "actions",
		Short: "Manage actions",
	}
	cmd.AddCommand(newActionsCreateCommand(state))
	cmd.AddCommand(newActionsListCommand(state))
	cmd.AddCommand(newActionsGetCommand(state))
	cmd.AddCommand(newActionsUpdateCommand(state))
	cmd.AddCommand(newDeleteCommand(state, "action", func(ctx context.Context, id string) error {
		return state.chief.Actions.Delete(ctx, id)
	}))
	cmd.AddCommand(newToggleCommand(state, "enable", "action", func(ctx context.Context, id string) (any, error) {
		return state.chief.Actions.Enable(ctx, id)
	}))
	cmd.AddCommand(newToggleCommand(state, "disable", "action", func(ctx context.Context, id string) (any, error) {
		return state.chief.Actions.Disable(ctx, id)
	}))
	return cmd
}

// actionFlags is the shared request shape for create and update.
type actionFlags struct {
	prompt       string
	description  string
	hour         string
	weekday      string
	monthDay     string
	timezone     string
	trigger      string
	emailSubject string
	emails       []string
	labelIDs     []string
	assetIDs     []string
}

func addActionFlags(cmd *cobra.Command, f *actionFlags) {
	cmd.Flags().StringVar(&f.prompt, "prompt", "", "instruction the action runs")
	cmd.Flags().StringVar(&f.description, "description", "", "human-readable description")
	cmd.Flags().StringVar(&f.hour, "hour", "", "cron hour field for the schedule")
	cmd.Flags().StringVar(&f.weekday, "weekday", "", "cron weekday field for the schedule")
	cmd.Flags().StringVar(&f.monthDay, "month-day", "", "cron day-of-month field for the schedule")
	cmd.Flags().StringVar(&f.timezone, "timezone", "", "IANA timezone for the schedule (default UTC)")
	cmd.Flags().StringVar(&f.trigger, "trigger", "", "event trigger: new or all")
	cmd.Flags().StringArrayVar(&f.emails, "email", nil, "email recipient (repeatable)")
	cmd.Flags().StringVar(&f.emailSubject, "email-subject", "", "subject line for the email outcome")
	cmd.Flags().StringArrayVar(&f.labelIDs, "label-id", nil, "scope the action to a label (repeatable)")
	cmd.Flags().StringArrayVar(&f.assetIDs, "asset-id", nil, "scope the action to an asset (repeatable)")
}

// build leaves Enabled unset: new actions start enabled, and pausing is done
// with the disable command.
func (f *actionFlags) build(name string) *chief.ActionRequest {
	req := &chief.ActionRequest{
		Name:        name,
		Prompt:      f.prompt,
		Description: f.description,
	}

	if f.hour != "" || f.weekday != "" || f.monthDay != "" || f.timezone != "" {
		// Cron validation rejects empty positions, so unset fields default to
		// the wildcard for partial input like --hour 7.
		schedule := &chief.ScheduleRequest{
			Hour:     f.hour,
			Weekday:  f.weekday,
			MonthDay: f.monthDay,
			Timezone: f.timezone,
		}
		if schedule.Hour == "" {
			schedule.Hour = "*"
		}
		if schedule.Weekday == "" {
			schedule.Weekday = "*"
		}
		if schedule.MonthDay == "" {
			schedule.MonthDay = "*"
		}
		if schedule.Timezone == "" {
			schedule.Timezone = "UTC"
		}
		req.Schedule = schedule
	}

	if f.trigger != "" {
		req.Trigger = &chief.TriggerRequest{Kind: f.trigger}
	}

	if len(f.emails) > 0 {
		req.Email = &chief.EmailOutcome{To: f.emails, Subject: f.emailSubject}
	}

	if len(f.labelIDs) > 0 || len(f.assetIDs) > 0 {
		req.Scope = &chief.ScopeRequest{LabelIDs: f.labelIDs, AssetIDs: f.assetIDs}
	}

	return req
}

func newActionsCreateCommand(state *app) *cobra.Command {
	f := &actionFlags{}
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an action",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.prompt == "" {
				return errors.New("--prompt is required")
			}
			action, err := state.chief.Actions.Create(cmd.Context(), f.build(args[0]))
			if err != nil {
				return err
			}
			return state.printer.emit(action, func() { printActionSummary(state.printer, action) })
		},
	}
	addActionFlags(cmd, f)
	return cmd
}

func newActionsListCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List actions in the project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			page, err := state.chief.Actions.List(cmd.Context())
			if err != nil {
				return err
			}
			return state.printer.emit(page, func() { renderActionTable(state.printer, page) })
		},
	}
	return cmd
}

func renderActionTable(p *printer, page *chief.ActionPage) {
	if len(page.Data) == 0 {
		p.line("no actions")
		return
	}

	headers := []string{"ID", "NAME", "ENABLED", "CREATED"}
	rows := make([][]string, 0, len(page.Data))
	for _, a := range page.Data {
		rows = append(rows, []string{
			a.ActionID,
			a.Name,
			yesNo(a.Enabled),
			a.CreatedAt.Format(time.RFC3339),
		})
	}
	p.table(headers, rows)
}

func newActionsGetCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a single action by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			action, err := state.chief.Actions.Get(cmd.Context(), args[0])
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("action %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(action, func() { printActionDetail(state.printer, action) })
		},
	}
	return cmd
}

func newActionsUpdateCommand(state *app) *cobra.Command {
	f := &actionFlags{}
	var name string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an action",
		Long:  "update replaces an action wholesale. Any schedule, trigger, scope, or email not passed as flags is cleared. When --name is omitted the existing name is kept.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				existing, err := state.chief.Actions.Get(cmd.Context(), args[0])
				if err != nil {
					if chief.IsNotFound(err) {
						return fmt.Errorf("action %q not found", args[0])
					}
					return err
				}
				name = existing.Name
			}
			action, err := state.chief.Actions.Update(cmd.Context(), args[0], f.build(name))
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("action %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(action, func() { printActionSummary(state.printer, action) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name (keeps the current name when omitted)")
	addActionFlags(cmd, f)
	return cmd
}

func printActionSummary(p *printer, a *chief.ActionResponse) {
	p.kv("Action ID", a.ActionID)
	p.kv("Name", a.Name)
	p.kv("Enabled", yesNo(a.Enabled))
	if line := scheduleTriggerLine(a); line != "" {
		p.kv("Runs", line)
	}
}

func printActionDetail(p *printer, a *chief.ActionResponse) {
	p.kv("Action ID", a.ActionID)
	p.kv("Name", a.Name)
	if a.Description != "" {
		p.kv("Description", a.Description)
	}
	p.kv("Prompt", a.Prompt)
	p.kv("Enabled", yesNo(a.Enabled))
	if a.Schedule != nil {
		s := a.Schedule
		p.kv("Schedule", fmt.Sprintf("hour=%s weekday=%s month_day=%s tz=%s",
			s.Hour, s.Weekday, s.MonthDay, s.Timezone))
	}
	if a.Trigger != nil {
		p.kv("Trigger", a.Trigger.Kind)
	}
	if a.Email != nil {
		p.kv("Email", strings.Join(a.Email.To, ", "))
	}
	p.kv("Created", a.CreatedAt.Format(time.RFC3339))
	p.kv("Modified", a.ModifiedAt.Format(time.RFC3339))
}

func scheduleTriggerLine(a *chief.ActionResponse) string {
	switch {
	case a.Schedule != nil:
		s := a.Schedule
		return fmt.Sprintf("schedule hour=%s weekday=%s month_day=%s tz=%s",
			s.Hour, s.Weekday, s.MonthDay, s.Timezone)
	case a.Trigger != nil:
		return fmt.Sprintf("trigger %s", a.Trigger.Kind)
	default:
		return ""
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
