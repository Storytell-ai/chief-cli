package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newEnvCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage configured API environments (base URLs)",
	}
	cmd.AddCommand(newEnvListCommand(state))
	cmd.AddCommand(newEnvUseCommand(state))
	return cmd
}

func newEnvListCommand(state *app) *cobra.Command {
	return &cobra.Command{
		Use:         "list",
		Short:       "List configured environments",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{annotationSkipClient: "1"},
		RunE:        func(_ *cobra.Command, _ []string) error { return state.listEnvs() },
	}
}

func newEnvUseCommand(state *app) *cobra.Command {
	return &cobra.Command{
		Use:         "use [name|url]",
		Short:       "Switch the active environment",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{annotationSkipClient: "1"},
		RunE:        func(_ *cobra.Command, args []string) error { return state.useEnv(args) },
	}
}

func (state *app) listEnvs() error {
	p := state.printer
	creds := state.creds
	keys := creds.sortedHostKeys()

	if p.json {
		out := make([]map[string]any, 0, len(keys))
		for _, u := range keys {
			h := creds.Hosts[u]
			out = append(out, map[string]any{
				"alias":    h.Alias,
				"base_url": u,
				"current":  u == creds.Current,
				"project":  h.Project,
				"insecure": h.Insecure,
			})
		}
		return p.writeJSON(out)
	}

	if len(keys) == 0 {
		p.line("no environments configured; run chief login")
		return nil
	}

	headers := []string{"", "ALIAS", "BASE URL", "PROJECT", "INSECURE"}
	rows := make([][]string, 0, len(keys))
	for _, u := range keys {
		h := creds.Hosts[u]
		marker := ""
		if u == creds.Current {
			marker = "*"
		}
		rows = append(rows, []string{marker, h.Alias, u, h.Project, strconv.FormatBool(h.Insecure)})
	}
	p.table(headers, rows)
	return nil
}

func (state *app) useEnv(args []string) error {
	p := state.printer
	creds := state.creds
	if len(creds.Hosts) == 0 {
		return errors.New("no environments configured; run `chief login`")
	}

	key, err := selectEnvKey(p, creds, args)
	if err != nil {
		return err
	}

	creds.Current = key
	if err := creds.save(); err != nil {
		return err
	}

	h := creds.Hosts[key]
	if p.json {
		return p.writeJSON(map[string]any{
			"alias":    h.Alias,
			"base_url": key,
			"project":  h.Project,
		})
	}
	p.line(fmt.Sprintf("%s switched to %s", p.ok.Render("✓"), envLabel(key, h)))
	if h.Project != "" {
		p.kv("Project", h.Project)
	}
	return nil
}

// selectEnvKey resolves the target host key from an argument (alias or URL) or,
// with no argument on a terminal, an interactive picker.
func selectEnvKey(p *printer, creds *credentials, args []string) (string, error) {
	switch {
	case len(args) == 1:
		key, _, ok := creds.resolveHost(args[0])
		if !ok {
			return "", fmt.Errorf("unknown environment %q; %s", args[0], knownEnvsHint(creds))
		}
		return key, nil
	case isInteractive():
		return pickEnv(p, creds)
	default:
		return "", fmt.Errorf("provide an environment name or base URL; %s", knownEnvsHint(creds))
	}
}

// pickEnv prompts for a configured environment by number.
func pickEnv(p *printer, creds *credentials) (string, error) {
	keys := creds.sortedHostKeys()
	for i, k := range keys {
		marker := " "
		if k == creds.Current {
			marker = "*"
		}
		p.line(fmt.Sprintf("%s %s %s", marker, p.subtle.Render(fmt.Sprintf("%d.", i+1)), envLabel(k, creds.Hosts[k])))
	}
	i, err := promptIndex(p, fmt.Sprintf("select environment [1-%d]", len(keys)), len(keys))
	if err != nil {
		return "", err
	}
	return keys[i], nil
}

// envLabel renders a host as "alias (url)" when it has an alias, else the url.
func envLabel(key string, h *hostConfig) string {
	if h.Alias != "" {
		return fmt.Sprintf("%s (%s)", h.Alias, key)
	}
	return key
}

// knownEnvsHint lists configured environments by alias or URL for error text.
func knownEnvsHint(c *credentials) string {
	keys := c.sortedHostKeys()
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		names = append(names, envLabel(k, c.Hosts[k]))
	}
	return "known: " + strings.Join(names, ", ")
}
