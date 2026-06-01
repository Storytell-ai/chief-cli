package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

func newChatsCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chats",
		Short: "Manage chats",
	}
	cmd.AddCommand(newChatsCreateCommand(state))
	cmd.AddCommand(newChatsSendCommand(state))
	cmd.AddCommand(newChatsGetCommand(state))
	cmd.AddCommand(newChatsListCommand(state))
	cmd.AddCommand(newChatsUpdateCommand(state))
	cmd.AddCommand(newChatsDeleteCommand(state))
	return cmd
}

// chatTuning is the shared turn-tuning shape for chat creation and follow-up
// messages. The two requests are distinct types, so the build is inlined at
// each call site from these helpers.
type chatTuning struct {
	intelligence string
	provider     string
	skills       []string
	publicData   bool
	labelIDs     []string
	assetIDs     []string
	chatIDs      []string
	projectIDs   []string
	conceptIDs   []string
}

func (f *chatTuning) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.intelligence, "intelligence", "", "mode preset: auto, fast, expert, or research")
	cmd.Flags().StringVar(&f.provider, "provider", "", "provider bias: automatic, anthropic, openai, or google")
	cmd.Flags().StringArrayVar(&f.skills, "skill", nil, "skill to enable for the turn (repeatable)")
	cmd.Flags().BoolVar(&f.publicData, "public-data", false, "allow public-web search (defaults to the mode preset)")
	cmd.Flags().StringArrayVar(&f.labelIDs, "label-id", nil, "scope the chat to a label (repeatable)")
	cmd.Flags().StringArrayVar(&f.assetIDs, "asset-id", nil, "scope the chat to an asset (repeatable)")
	cmd.Flags().StringArrayVar(&f.chatIDs, "chat-id", nil, "include a past chat as context (repeatable)")
	cmd.Flags().StringArrayVar(&f.projectIDs, "project-id", nil, "scope the chat to a project (repeatable)")
	cmd.Flags().StringArrayVar(&f.conceptIDs, "concept-id", nil, "scope the chat to a concept (repeatable)")
}

func (f *chatTuning) scope() *chief.ScopeRequest {
	if len(f.labelIDs) == 0 && len(f.assetIDs) == 0 && len(f.chatIDs) == 0 &&
		len(f.projectIDs) == 0 && len(f.conceptIDs) == 0 {
		return nil
	}
	return &chief.ScopeRequest{
		LabelIDs:   f.labelIDs,
		AssetIDs:   f.assetIDs,
		ChatIDs:    f.chatIDs,
		ProjectIDs: f.projectIDs,
		ConceptIDs: f.conceptIDs,
	}
}

// publicDataPtr returns nil unless --public-data was set, so an untouched flag
// follows the mode default instead of forcing false.
func (f *chatTuning) publicDataPtr(cmd *cobra.Command) *bool {
	if !cmd.Flags().Changed("public-data") {
		return nil
	}
	return &f.publicData
}

// chatTurnResult is the --json shape for a waited chat creation: the new chat's
// id alongside the completed turn, since Message alone carries no chat id.
type chatTurnResult struct {
	ChatID  string         `json:"chat_id"`
	Message *chief.Message `json:"message"`
}

// chatTranscript is the --json shape for a chat's full conversation.
type chatTranscript struct {
	ChatID   string           `json:"chat_id"`
	Messages []*chief.Message `json:"messages"`
}

// awaitChatResponse blocks until the turn's response populates, painting a live
// status row on a TTY. preview labels the in-flight row.
func awaitChatResponse(ctx context.Context, state *app, chatID, messageID, preview string, timeout time.Duration) (*chief.Message, error) {
	state.printer.startLive(1)
	row := state.printer.addRow(preview)
	state.printer.setRowState(row, "running")
	msg, err := state.chief.Chats.WaitForResponse(ctx, chatID, messageID, timeout)
	state.printer.stopLive()
	return msg, err
}

func pollHint(p *printer, chatID string) {
	p.line(fmt.Sprintf("turn is processing; check `chief chats get %s`", chatID))
}

func newChatsCreateCommand(state *app) *cobra.Command {
	f := &chatTuning{}
	var (
		noWait  bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "create <prompt>",
		Short: "Start a chat with its first message",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := args[0]
			resp, err := state.chief.Chats.Create(cmd.Context(), &chief.CreateChatRequest{
				Prompt:       prompt,
				Intelligence: f.intelligence,
				Provider:     f.provider,
				Skills:       f.skills,
				PublicData:   f.publicDataPtr(cmd),
				Scope:        f.scope(),
			})
			if err != nil {
				return err
			}
			if noWait {
				return state.printer.emit(resp, func() {
					p := state.printer
					p.kv("Chat ID", resp.ChatID)
					p.kv("Message ID", resp.MessageID)
					p.kv("Created", resp.CreatedAt.Format(time.RFC3339))
					pollHint(p, resp.ChatID)
				})
			}
			msg, err := awaitChatResponse(cmd.Context(), state, resp.ChatID, resp.MessageID, prompt, timeout)
			if err != nil {
				if !state.printer.json {
					p := state.printer
					p.kv("Chat ID", resp.ChatID)
					p.kv("Message ID", resp.MessageID)
					pollHint(p, resp.ChatID)
				}
				return err
			}
			return state.printer.emit(chatTurnResult{ChatID: resp.ChatID, Message: msg}, func() {
				p := state.printer
				printAnswer(p, msg)
				p.line("")
				p.line(p.subtle.Render("chat: " + resp.ChatID))
			})
		},
	}
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return once the turn is accepted instead of waiting for the response")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for the response")
	f.register(cmd)
	return cmd
}

func newChatsSendCommand(state *app) *cobra.Command {
	f := &chatTuning{}
	var (
		noWait  bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "send <chat-id> <prompt>",
		Short: "Send a follow-up message to a chat",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			chatID, prompt := args[0], args[1]
			resp, err := state.chief.Chats.SendMessage(cmd.Context(), chatID, &chief.SendMessageRequest{
				Prompt:       prompt,
				Intelligence: f.intelligence,
				Provider:     f.provider,
				Skills:       f.skills,
				PublicData:   f.publicDataPtr(cmd),
				Scope:        f.scope(),
			})
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("chat %q not found", chatID)
				}
				return err
			}
			if noWait {
				return state.printer.emit(resp, func() {
					p := state.printer
					p.kv("Message ID", resp.MessageID)
					p.kv("Created", resp.CreatedAt.Format(time.RFC3339))
					pollHint(p, chatID)
				})
			}
			msg, err := awaitChatResponse(cmd.Context(), state, chatID, resp.MessageID, prompt, timeout)
			if err != nil {
				if !state.printer.json {
					p := state.printer
					p.kv("Message ID", resp.MessageID)
					pollHint(p, chatID)
				}
				return err
			}
			return state.printer.emit(msg, func() { printAnswer(state.printer, msg) })
		},
	}
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return once the turn is accepted instead of waiting for the response")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for the response")
	f.register(cmd)
	return cmd
}

func newChatsGetCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <chat-id>",
		Short: "Show a chat's full conversation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := state.chief.Chats.ListMessages(cmd.Context(), args[0])
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("chat %q not found", args[0])
				}
				return err
			}
			// The listing carries only ids, so each message's content is a
			// separate fetch; run them concurrently and write each into its own
			// slot to avoid N serial round-trips without losing transcript order.
			msgs := make([]*chief.Message, len(list.Messages))
			g, ctx := errgroup.WithContext(cmd.Context())
			g.SetLimit(8)
			for i, summary := range list.Messages {
				g.Go(func() error {
					m, err := state.chief.Chats.GetMessage(ctx, args[0], summary.ID)
					if err != nil {
						return err
					}
					msgs[i] = m
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				return err
			}
			return state.printer.emit(chatTranscript{ChatID: args[0], Messages: msgs}, func() {
				if len(msgs) == 0 {
					state.printer.line("no messages")
					return
				}
				for i, m := range msgs {
					if i > 0 {
						state.printer.line("")
					}
					printTurn(state.printer, m)
				}
			})
		},
	}
	return cmd
}

func newChatsListCommand(state *app) *cobra.Command {
	f := &pagingFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List chats in the project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			page, err := state.chief.Chats.List(cmd.Context(), f.options()...)
			if err != nil {
				return err
			}
			return state.printer.emit(page, func() {
				if len(page.Data) == 0 {
					state.printer.line("no chats")
					return
				}
				rows := make([][]string, 0, len(page.Data))
				for _, c := range page.Data {
					rows = append(rows, []string{c.ChatID, c.CreatedAt.Format(time.RFC3339)})
				}
				state.printer.table([]string{"ID", "CREATED"}, rows)
			})
		},
	}

	f.register(cmd, "chat", "chats")
	return cmd
}

func newChatsUpdateCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <chat-id> <title>",
		Short: "Rename a chat",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			chat, err := state.chief.Chats.Update(cmd.Context(), args[0], &chief.UpdateChatRequest{Title: args[1]})
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("chat %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(chat, func() {
				state.printer.kv("Chat ID", chat.ChatID)
				if chat.ModifiedAt != nil {
					state.printer.kv("Modified", chat.ModifiedAt.Format(time.RFC3339))
				}
			})
		},
	}
	return cmd
}

func newChatsDeleteCommand(state *app) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <chat-id> [message-id]",
		Short: "Delete a chat, or a single message when a message ID is given",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 {
				return confirmAndDelete(cmd.Context(), state, force, "message", args[1], func(ctx context.Context, id string) error {
					return state.chief.Chats.DeleteMessage(ctx, args[0], id)
				})
			}
			return confirmAndDelete(cmd.Context(), state, force, "chat", args[0], state.chief.Chats.Delete)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip the confirmation prompt")
	return cmd
}

// printAnswer renders a turn's assistant response as markdown, or a pending
// notice while the async turn is still being written.
func printAnswer(p *printer, m *chief.Message) {
	if m.Response != "" {
		p.markdown(m.Response)
		return
	}
	p.line("(pending — turn has not finished)")
}

// printTurn renders one transcript turn: the user's prompt as a lead-in above
// its answer.
func printTurn(p *printer, m *chief.Message) {
	if m.Prompt != "" {
		p.line(p.key.Render("› ") + m.Prompt)
	}
	printAnswer(p, m)
}
