package main

import (
	"context"
	"fmt"

	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
)

func newLabelsCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "labels",
		Short: "Manage labels",
	}
	cmd.AddCommand(newLabelsCreateCommand(state))
	cmd.AddCommand(newLabelsListCommand(state))
	cmd.AddCommand(newLabelsGetCommand(state))
	cmd.AddCommand(newLabelsUpdateCommand(state))
	cmd.AddCommand(newDeleteCommand(state, "label", func(ctx context.Context, id string) error {
		return state.chief.Labels.Delete(ctx, id)
	}))
	cmd.AddCommand(newLabelsAttachCommand(state))
	cmd.AddCommand(newLabelsDetachCommand(state))
	return cmd
}

func newLabelsCreateCommand(state *app) *cobra.Command {
	var (
		color string
		icon  string
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a label",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			label, err := state.chief.Labels.Create(cmd.Context(), &chief.CreateLabelRequest{
				Name:  args[0],
				Color: color,
				Icon:  icon,
			})
			if err != nil {
				return err
			}
			return state.printer.emit(label, func() {
				p := state.printer
				p.kv("Label ID", label.LabelID)
				p.kv("Name", label.Name)
			})
		},
	}

	cmd.Flags().StringVar(&color, "color", "", "hex color like #6b7280")
	cmd.Flags().StringVar(&icon, "icon", "", "icon name")
	return cmd
}

func newLabelsListCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List labels in the project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			page, err := state.chief.Labels.List(cmd.Context())
			if err != nil {
				return err
			}
			return state.printer.emit(page, func() { renderLabelTable(state.printer, page) })
		},
	}
	return cmd
}

func renderLabelTable(p *printer, page *chief.LabelPage) {
	if len(page.Data) == 0 {
		p.line("no labels")
		return
	}

	headers := []string{"ID", "NAME"}
	rows := make([][]string, 0, len(page.Data))
	for _, l := range page.Data {
		rows = append(rows, []string{l.LabelID, l.Name})
	}
	p.table(headers, rows)
}

func newLabelsGetCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a single label by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			label, err := state.chief.Labels.Get(cmd.Context(), args[0])
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("label %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(label, func() { printLabelDetail(state.printer, label) })
		},
	}
	return cmd
}

func newLabelsUpdateCommand(state *app) *cobra.Command {
	var (
		name  string
		color string
		icon  string
	)

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a label",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &chief.UpdateLabelRequest{}
			if cmd.Flags().Changed("name") {
				req.Name = &name
			}
			if cmd.Flags().Changed("color") {
				req.Color = &color
			}
			if cmd.Flags().Changed("icon") {
				req.Icon = &icon
			}
			label, err := state.chief.Labels.Update(cmd.Context(), args[0], req)
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("label %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(label, func() { printLabelDetail(state.printer, label) })
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "new label name")
	cmd.Flags().StringVar(&color, "color", "", "hex color like #6b7280")
	cmd.Flags().StringVar(&icon, "icon", "", "icon name")
	return cmd
}

func printLabelDetail(p *printer, l *chief.LabelResponse) {
	p.kv("Label ID", l.LabelID)
	p.kv("Name", l.Name)
	if l.Color != "" {
		p.kv("Color", l.Color)
	}
	if l.Icon != "" {
		p.kv("Icon", l.Icon)
	}
}

func newLabelsAttachCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <asset-id> <label-name>",
		Short: "Attach a label to an asset by name",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			label, err := state.chief.Assets.AttachLabel(cmd.Context(), args[0], args[1])
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("asset %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(label, func() {
				p := state.printer
				p.line(fmt.Sprintf("%s attached %s to %s", p.ok.Render("✓"), label.Name, args[0]))
			})
		},
	}
	return cmd
}

func newLabelsDetachCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "detach <asset-id> <label-id>",
		Short: "Detach a label from an asset",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.chief.Assets.DetachLabel(cmd.Context(), args[0], args[1]); err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("asset %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(map[string]string{"asset_id": args[0], "detached": args[1]}, func() {
				p := state.printer
				p.line(fmt.Sprintf("%s detached %s from %s", p.ok.Render("✓"), args[1], args[0]))
			})
		},
	}
	return cmd
}
