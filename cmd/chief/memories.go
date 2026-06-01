package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
)

func newMemoriesCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memories",
		Short: "Manage memories",
	}
	cmd.AddCommand(newMemoriesCreateCommand(state))
	cmd.AddCommand(newMemoriesListCommand(state))
	cmd.AddCommand(newMemoriesGetCommand(state))
	cmd.AddCommand(newMemoriesUpdateCommand(state))
	cmd.AddCommand(newDeleteCommand(state, "memory", func(ctx context.Context, id string) error {
		return state.chief.Memories.Delete(ctx, id)
	}))
	return cmd
}

func newMemoriesCreateCommand(state *app) *cobra.Command {
	var (
		content    string
		category   string
		importance int
		scope      string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a memory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if content == "" {
				return errors.New("--content is required")
			}
			memory, err := state.chief.Memories.Create(cmd.Context(), &chief.CreateMemoryRequest{
				Content:    content,
				Category:   category,
				Importance: importance,
				Scope:      scope,
			})
			if err != nil {
				return err
			}
			return state.printer.emit(memory, func() { printMemorySummary(state.printer, memory) })
		},
	}

	cmd.Flags().StringVar(&content, "content", "", "memory text (required)")
	cmd.Flags().StringVar(&category, "category", "", "category: identity, preference, fact, context, or instruction")
	cmd.Flags().IntVar(&importance, "importance", 0, "importance score")
	cmd.Flags().StringVar(&scope, "scope", "", "scope: empty or project")
	return cmd
}

func newMemoriesListCommand(state *app) *cobra.Command {
	f := &pagingFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List memories in the project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			page, err := state.chief.Memories.List(cmd.Context(), f.options()...)
			if err != nil {
				return err
			}
			return state.printer.emit(page, func() { renderMemoryTable(state.printer, page) })
		},
	}

	f.register(cmd, "memory", "memories")
	return cmd
}

func renderMemoryTable(p *printer, page *chief.MemoryPage) {
	if len(page.Data) == 0 {
		p.line("no memories")
		return
	}

	headers := []string{"ID", "CATEGORY", "IMPORTANCE", "CONTENT"}
	rows := make([][]string, 0, len(page.Data))
	for _, m := range page.Data {
		rows = append(rows, []string{
			m.MemoryID,
			m.Category,
			strconv.Itoa(m.Importance),
			truncateName(m.Content, 60),
		})
	}
	p.table(headers, rows)
}

func newMemoriesGetCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a single memory by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			memory, err := state.chief.Memories.Get(cmd.Context(), args[0])
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("memory %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(memory, func() { printMemoryDetail(state.printer, memory) })
		},
	}
	return cmd
}

func newMemoriesUpdateCommand(state *app) *cobra.Command {
	var (
		content    string
		category   string
		importance int
	)

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a memory",
		Long:  "update replaces a memory's content. Category and importance are patched only when their flags are set.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if content == "" {
				return errors.New("--content is required")
			}
			req := &chief.UpdateMemoryRequest{Content: content}
			if cmd.Flags().Changed("category") {
				req.Category = &category
			}
			if cmd.Flags().Changed("importance") {
				req.Importance = &importance
			}
			memory, err := state.chief.Memories.Update(cmd.Context(), args[0], req)
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("memory %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(memory, func() { printMemorySummary(state.printer, memory) })
		},
	}

	cmd.Flags().StringVar(&content, "content", "", "new memory text (required)")
	cmd.Flags().StringVar(&category, "category", "", "category: identity, preference, fact, context, or instruction")
	cmd.Flags().IntVar(&importance, "importance", 0, "importance score")
	return cmd
}

func printMemorySummary(p *printer, m *chief.MemoryResponse) {
	p.kv("Memory ID", m.MemoryID)
	p.kv("Category", m.Category)
	p.kv("Importance", strconv.Itoa(m.Importance))
	p.kv("Content", m.Content)
}

func printMemoryDetail(p *printer, m *chief.MemoryResponse) {
	p.kv("Memory ID", m.MemoryID)
	if m.Scope != "" {
		p.kv("Scope", m.Scope)
	}
	p.kv("Category", m.Category)
	p.kv("Importance", strconv.Itoa(m.Importance))
	p.kv("Content", m.Content)
	p.kv("Created", m.CreatedAt.Format(time.RFC3339))
	p.kv("Modified", m.ModifiedAt.Format(time.RFC3339))
}
