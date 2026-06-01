package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
)

func newMCPCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Generate MCP server configuration for coding agents",
	}
	cmd.AddCommand(newMCPConfigCommand(state))
	return cmd
}

func newMCPConfigCommand(state *app) *cobra.Command {
	return &cobra.Command{
		Use:         "config <harness>",
		Short:       "Print an MCP server config snippet for a coding agent (claude, cursor, codex)",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{annotationSkipClient: "1"},
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			snippet, hint, placeholder, err := buildHarnessConfig(name, *state.resolved)
			if err != nil {
				return err
			}

			p := state.printer
			if p.json {
				return p.writeJSON(map[string]any{
					"harness":     name,
					"snippet":     snippet,
					"hint":        hint,
					"placeholder": placeholder,
				})
			}

			p.errline(hint)
			if placeholder {
				p.errline("Replace the placeholder credentials above, or pass --api-key and --project.")
			}
			p.line(snippet)
			return nil
		},
	}
}

// mcpServerEnv builds the snippet env block. Missing credentials become
// placeholders so the snippet stays pasteable; baseURL is included only when it
// differs from the production default.
func mcpServerEnv(rc resolvedConfig) (env map[string]string, placeholder bool) {
	env = map[string]string{}

	apiKey := rc.APIKey
	if apiKey == "" {
		apiKey = "<your-api-key>"
		placeholder = true
	}
	env[chief.EnvAPIKey] = apiKey

	project := rc.Project
	if project == "" {
		project = "<your-project-id>"
		placeholder = true
	}
	env[chief.EnvProjectID] = project

	if rc.BaseURL != "" && rc.BaseURL != chief.DefaultBaseURL {
		env[chief.EnvBaseURL] = rc.BaseURL
	}

	return env, placeholder
}

// mcpServerBinary resolves the chief-mcp server binary the snippet must point
// at, not the chief CLI that emits it.
func mcpServerBinary() string {
	if path, err := exec.LookPath("chief-mcp"); err == nil {
		return path
	}
	return "chief-mcp"
}

func buildHarnessConfig(name string, rc resolvedConfig) (snippet, hint string, placeholder bool, err error) {
	bin := mcpServerBinary()

	args := []string{"stdio"}
	if rc.Insecure {
		args = append(args, "--insecure")
	}

	var env map[string]string
	env, placeholder = mcpServerEnv(rc)

	switch strings.ToLower(name) {
	case "claude", "claude-code":
		snippet, err = jsonMCPConfig("mcpServers", bin, args, env)
		if err != nil {
			return
		}
		hint = fmt.Sprintf("Add to .mcp.json (project) or ~/.claude.json (user). Or run: claude mcp add chief -- %s stdio", bin)
		return
	case "cursor":
		snippet, err = jsonMCPConfig("mcpServers", bin, args, env)
		if err != nil {
			return
		}
		hint = "Add to .cursor/mcp.json (project) or ~/.cursor/mcp.json (global)."
		return
	case "codex":
		snippet = codexConfig(bin, args, env)
		hint = "Add to ~/.codex/config.toml"
		return
	default:
		err = fmt.Errorf("unknown harness: %s (supported: claude, cursor, codex)", name)
		return
	}
}

func jsonMCPConfig(serversKey, bin string, args []string, env map[string]string) (string, error) {
	root := map[string]any{
		serversKey: map[string]any{
			"chief": map[string]any{
				"command": bin,
				"args":    args,
				"env":     env,
			},
		},
	}

	// Placeholder credentials use angle brackets; HTML-escaping them to &lt;
	// would leave an unreadable snippet to edit.
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func codexConfig(bin string, args []string, env map[string]string) string {
	var b strings.Builder

	b.WriteString("[mcp_servers.chief]\n")
	fmt.Fprintf(&b, "command = %s\n", strconv.Quote(bin))

	quotedArgs := make([]string, len(args))
	for i, a := range args {
		quotedArgs[i] = strconv.Quote(a)
	}
	fmt.Fprintf(&b, "args = [%s]\n", strings.Join(quotedArgs, ", "))

	if len(env) > 0 {
		var pairs []string
		for _, k := range []string{chief.EnvAPIKey, chief.EnvProjectID, chief.EnvBaseURL} {
			if v, ok := env[k]; ok {
				pairs = append(pairs, fmt.Sprintf("%s = %s", k, strconv.Quote(v)))
			}
		}
		fmt.Fprintf(&b, "env = { %s }\n", strings.Join(pairs, ", "))
	}

	return b.String()
}
