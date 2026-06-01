package main

import (
	"context"
	"fmt"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
)

// annotationSkipClient marks commands that must run without an authenticated
// client (login, env ops, doctor), so the persistent pre-run skips building one.
const annotationSkipClient = "chief/skip-client"

// app is the shared state for subcommands.
type app struct {
	chief    *chief.Client
	printer  *printer
	resolved *resolvedConfig
	creds    *credentials
}

// Execute builds the command tree and runs it.
func Execute(ctx context.Context) error {
	var (
		apiKey   string
		project  string
		baseURL  string
		insecure bool
		asJSON   bool
		debug    bool
		noColor  bool
	)
	state := &app{}
	ver := buildVersion()

	root := &cobra.Command{
		Use:           "chief",
		Short:         "Chief CLI for the Chief public API",
		Long:          "chief is the command-line client for the Chief public API. It renders styled output on a terminal and machine-readable JSON when piped or run with --json.",
		Version:       ver,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			state.printer = newPrinter(asJSON, noColor)

			creds, err := loadCredentials()
			if err != nil {
				return err
			}
			state.creds = creds
			rc := resolveConfig(creds, configFlags{
				BaseURL: baseURL, BaseURLSet: cmd.Flags().Changed("base-url"),
				APIKey: apiKey, APIKeySet: cmd.Flags().Changed("api-key"),
				Project: project, ProjectSet: cmd.Flags().Changed("project"),
				Insecure: insecure, InsecureSet: cmd.Flags().Changed("insecure"),
			})
			state.resolved = &rc

			if cmd.Annotations[annotationSkipClient] == "1" {
				return nil
			}

			if rc.APIKey == "" {
				return fmt.Errorf("not authenticated: run `chief login`, set %s, or pass --api-key", chief.EnvAPIKey)
			}

			c, err := newChiefClient(rc.BaseURL, rc.APIKey, rc.Project, rc.Insecure, debug)
			if err != nil {
				return err
			}
			state.chief = c
			return nil
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&apiKey, "api-key", "", "Chief API key (env CHIEF_API_KEY)")
	pf.StringVar(&project, "project", "", "project ID (env CHIEF_PROJECT_ID)")
	pf.StringVar(&baseURL, "base-url", "", "API base URL (env CHIEF_BASE_URL; default https://api.storytell.ai)")
	pf.BoolVar(&insecure, "insecure", false, "skip TLS certificate verification (local dev only)")
	pf.BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	pf.BoolVar(&debug, "debug", false, "dump HTTP requests and responses")
	pf.BoolVar(&noColor, "no-color", false, "disable colored output")

	root.AddCommand(newLoginCommand(state))
	root.AddCommand(newProjectCommand(state))
	root.AddCommand(newEnvCommand(state))
	root.AddCommand(newDoctorCommand(state))
	root.AddCommand(newActionsCommand(state))
	root.AddCommand(newAssetsCommand(state))
	root.AddCommand(newChatsCommand(state))
	root.AddCommand(newLabelsCommand(state))
	root.AddCommand(newSessionsCommand(state))
	root.AddCommand(newSkillsCommand(state))
	root.AddCommand(newMemoriesCommand(state))
	root.AddCommand(newAPICommand(state))
	root.AddCommand(newMCPCommand(state))

	return fang.Execute(ctx, root,
		fang.WithVersion(ver),
		fang.WithColorSchemeFunc(chiefColorScheme),
	)
}

// newChiefClient builds an API client from resolved settings.
func newChiefClient(baseURL, apiKey, project string, insecure, debug bool) (*chief.Client, error) {
	return chief.New(
		chief.WithAPIKey(apiKey),
		chief.WithProjectID(project),
		chief.WithBaseURL(baseURL),
		chief.WithInsecureSkipTLSVerify(insecure),
		chief.WithDebug(debug),
	)
}

// article returns the indefinite article for noun, for help text like "Delete
// an action" / "Delete a label".
func article(noun string) string {
	switch noun[0] {
	case 'a', 'e', 'i', 'o', 'u':
		return "an"
	}
	return "a"
}

// newDeleteCommand builds a `delete <id>` command that confirms, runs del, and
// reports the outcome. kind names the resource for the prompt, the not-found
// message, and the help text.
func newDeleteCommand(state *app, kind string, del func(ctx context.Context, id string) error) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete " + article(kind) + " " + kind,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return confirmAndDelete(cmd.Context(), state, force, kind, args[0], del)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip the confirmation prompt")
	return cmd
}

// confirmAndDelete prompts (unless force), runs del, maps a not-found error to a
// friendly message, and reports the deletion. kind names the resource.
func confirmAndDelete(ctx context.Context, state *app, force bool, kind, id string, del func(ctx context.Context, id string) error) error {
	ok, err := confirmDelete(state.printer, force, kind, id)
	if err != nil {
		return err
	}
	if !ok {
		state.printer.errline("aborted")
		return nil
	}
	if err := del(ctx, id); err != nil {
		if chief.IsNotFound(err) {
			return fmt.Errorf("%s %q not found", kind, id)
		}
		return err
	}
	return state.printer.emit(map[string]string{"deleted": id}, func() {
		state.printer.line(fmt.Sprintf("%s deleted %s", state.printer.ok.Render("✓"), id))
	})
}

// newToggleCommand builds an enable/disable command. verb is "enable" or
// "disable"; toggle runs the call and returns the affected resource for JSON.
func newToggleCommand(state *app, verb, kind string, toggle func(ctx context.Context, id string) (any, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   verb + " <id>",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " " + article(kind) + " " + kind,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			obj, err := toggle(cmd.Context(), args[0])
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("%s %q not found", kind, args[0])
				}
				return err
			}
			return state.printer.emit(obj, func() {
				state.printer.line(fmt.Sprintf("%s %sd %s", state.printer.ok.Render("✓"), verb, args[0]))
			})
		},
	}
	return cmd
}

// pagingFlags carries the shared --limit/--after-id/--before-id list options.
type pagingFlags struct {
	limit    int
	afterID  string
	beforeID string
}

func (f *pagingFlags) register(cmd *cobra.Command, singular, plural string) {
	cmd.Flags().IntVar(&f.limit, "limit", 0, "maximum number of "+plural+" to return")
	cmd.Flags().StringVar(&f.afterID, "after-id", "", "page forward from this "+singular+" ID")
	cmd.Flags().StringVar(&f.beforeID, "before-id", "", "page backward from this "+singular+" ID")
}

func (f *pagingFlags) options() []chief.ListOption {
	var opts []chief.ListOption
	if f.limit > 0 {
		opts = append(opts, chief.WithLimit(f.limit))
	}
	if f.afterID != "" {
		opts = append(opts, chief.WithAfterID(f.afterID))
	}
	if f.beforeID != "" {
		opts = append(opts, chief.WithBeforeID(f.beforeID))
	}
	return opts
}

// chiefColorScheme tints fang's help and error output with the Chief brand
// palette. The hexes mirror the desktop app's chief.css; keep them in sync.
// Clearing the codeblock background also removes the usage banner: fang drops
// the block's fill and vertical padding when its background is NoColor, so the
// usage line renders inline. Neutral text and argument colors stay on fang's
// adaptive defaults so light terminals stay readable.
func chiefColorScheme(c lipgloss.LightDarkFunc) fang.ColorScheme {
	cs := fang.DefaultColorScheme(c)
	cs.Codeblock = lipgloss.NoColor{}

	gold := lipgloss.Color("#d4a017")
	cs.Title = gold
	cs.Program = gold
	cs.Command = lipgloss.Color("#7088e8")
	cs.Flag = lipgloss.Color("#1d9e75")
	cs.QuotedString = lipgloss.Color("#e5b84c")

	cs.ErrorHeader[0] = lipgloss.Color("#0c0c11")
	cs.ErrorHeader[1] = lipgloss.Color("#e24b4a")
	return cs
}
