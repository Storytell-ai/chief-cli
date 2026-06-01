package main

import (
	"context"
	"errors"
	"fmt"

	"charm.land/huh/v2"
	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
)

func newLoginCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "login",
		Short:       "Authenticate to a Chief API host and save credentials",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{annotationSkipClient: "1"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			creds := state.creds

			in, err := state.gatherLoginInputs(cmd, creds)
			if err != nil {
				return err
			}
			if owner, conflict := creds.aliasConflict(in.alias, in.baseURL); conflict {
				return fmt.Errorf("alias %q already names %s; choose another", in.alias, owner)
			}
			if in.apiKey == "" {
				return errors.New("api key is required")
			}
			if err := state.verifyLogin(cmd.Context(), in); err != nil {
				return err
			}

			in.applyTo(creds)
			if err := creds.save(); err != nil {
				return err
			}
			return state.reportLogin(in)
		},
	}
	cmd.Flags().String("alias", "", "friendly name for this host, usable with `chief env use`")
	return cmd
}

// loginInputs is the resolved set of host settings a login persists.
type loginInputs struct {
	baseURL  string
	project  string
	apiKey   string
	alias    string
	insecure bool
}

// gatherLoginInputs resolves each setting from its flag, an interactive prompt,
// or the existing host entry, in that order. The base URL additionally falls
// back to the active host so a bare `login` re-authenticates it.
func (state *app) gatherLoginInputs(cmd *cobra.Command, creds *credentials) (loginInputs, error) {
	p := state.printer
	interactive := isInteractive()

	var baseURL string
	switch {
	case cmd.Flags().Changed("base-url"):
		baseURL, _ = cmd.Flags().GetString("base-url")
	case creds.Current != "":
		baseURL = creds.Current
	case interactive:
		v, err := promptLine(p, "API base URL", chief.DefaultBaseURL)
		if err != nil {
			return loginInputs{}, err
		}
		baseURL = v
	default:
		baseURL = chief.DefaultBaseURL
	}
	baseURL = normalizeBaseURL(baseURL)

	prev := &hostConfig{}
	if existing := creds.host(baseURL); existing != nil {
		prev = existing
	}

	apiKey, err := state.resolveLoginKey(cmd, interactive, prev.APIKey)
	if err != nil {
		return loginInputs{}, err
	}

	insecure := prev.Insecure
	if cmd.Flags().Changed("insecure") {
		insecure, _ = cmd.Flags().GetBool("insecure")
	}

	project, err := state.resolveLoginProject(cmd, interactive, baseURL, apiKey, insecure, prev.Project)
	if err != nil {
		return loginInputs{}, err
	}

	alias, err := promptOptional(p, cmd, interactive, "alias", "Alias (optional)", prev.Alias)
	if err != nil {
		return loginInputs{}, err
	}

	return loginInputs{baseURL: baseURL, project: project, apiKey: apiKey, alias: alias, insecure: insecure}, nil
}

// resolveLoginKey picks the API key from the flag, a hidden prompt, or the
// existing key. An empty interactive entry keeps the existing key.
func (state *app) resolveLoginKey(cmd *cobra.Command, interactive bool, existingKey string) (string, error) {
	switch {
	case cmd.Flags().Changed("api-key"):
		v, _ := cmd.Flags().GetString("api-key")
		return v, nil
	case interactive:
		label := "API key"
		if existingKey != "" {
			label = "API key (enter to keep existing)"
		}
		v, err := promptSecret(state.printer, label)
		if err != nil {
			return "", err
		}
		if v == "" {
			return existingKey, nil
		}
		return v, nil
	case existingKey != "":
		return existingKey, nil
	default:
		return "", errors.New("provide --api-key (non-interactive)")
	}
}

// resolveLoginProject picks the project from the flag, an interactive picker of
// the account's projects, or the existing project. A failed picker falls back
// to a free-text prompt seeded with prev.
func (state *app) resolveLoginProject(cmd *cobra.Command, interactive bool, baseURL, apiKey string, insecure bool, prev string) (string, error) {
	switch {
	case cmd.Flags().Changed("project"):
		v, _ := cmd.Flags().GetString("project")
		return v, nil
	case interactive:
		if id, ok := state.selectProject(cmd.Context(), baseURL, apiKey, insecure, prev); ok {
			return id, nil
		}
		return promptLine(state.printer, "Project ID", prev)
	default:
		return prev, nil
	}
}

// selectProject lists the account's projects and prompts the user to pick one,
// defaulting to prev when it is among them. It reports false on any failure so
// the caller can fall back to free-text entry.
func (state *app) selectProject(ctx context.Context, baseURL, apiKey string, insecure bool, prev string) (string, bool) {
	c, err := newChiefClient(baseURL, apiKey, "", insecure, false)
	if err != nil {
		return "", false
	}
	page, err := c.Projects.List(ctx)
	if err != nil || page == nil || len(page.Data) == 0 {
		return "", false
	}

	opts := make([]huh.Option[string], 0, len(page.Data))
	for _, pr := range page.Data {
		label := pr.ProjectID
		if pr.Name != "" {
			label = fmt.Sprintf("%s (%s)", pr.Name, pr.ProjectID)
		}
		opts = append(opts, huh.NewOption(label, pr.ProjectID))
	}

	selected := prev
	if !projectInPage(page, prev) {
		selected = page.Data[0].ProjectID
	}
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Project").Options(opts...).Value(&selected),
	)).WithShowHelp(false).WithAccessible(accessibleMode()).Run(); err != nil {
		return "", false
	}
	return selected, true
}

// promptOptional resolves an optional string from its flag, an interactive
// prompt seeded with prev, or prev when neither applies.
func promptOptional(p *printer, cmd *cobra.Command, interactive bool, flag, label, prev string) (string, error) {
	switch {
	case cmd.Flags().Changed(flag):
		v, _ := cmd.Flags().GetString(flag)
		return v, nil
	case interactive:
		return promptLine(p, label, prev)
	default:
		return prev, nil
	}
}

// verifyLogin checks the credentials against the API. An invalid key is fatal;
// a transport failure or missing project is only a warning, so login still
// works offline or against a host that predates the projects endpoint.
func (state *app) verifyLogin(ctx context.Context, in loginInputs) error {
	c, err := newChiefClient(in.baseURL, in.apiKey, in.project, in.insecure, false)
	if err != nil {
		return err
	}

	p := state.printer
	page, err := c.Projects.List(ctx)
	switch {
	case chief.IsUnauthorized(err):
		return fmt.Errorf("invalid API key for %s", in.baseURL)
	case err != nil:
		p.errline(fmt.Sprintf("%s could not verify credentials: %v", p.skip.Render("⚠"), err))
	default:
		if in.project != "" && !projectInPage(page, in.project) {
			p.errline(fmt.Sprintf("%s project %s not found among your projects", p.skip.Render("⚠"), in.project))
		}
	}
	return nil
}

// applyTo writes the inputs onto the store and makes the host current.
func (in loginInputs) applyTo(creds *credentials) {
	h := creds.ensureHost(in.baseURL)
	h.Alias = in.alias
	h.APIKey = in.apiKey
	h.Project = in.project
	h.Insecure = in.insecure
	creds.Current = in.baseURL
}

func (state *app) reportLogin(in loginInputs) error {
	p := state.printer
	path, _ := credentialsPath()

	if p.json {
		return p.writeJSON(map[string]any{
			"alias":    in.alias,
			"base_url": in.baseURL,
			"project":  in.project,
			"api_key":  maskSecret(in.apiKey),
			"insecure": in.insecure,
			"path":     path,
		})
	}

	p.line(fmt.Sprintf("%s logged in to %s", p.ok.Render("✓"), in.baseURL))
	if in.alias != "" {
		p.kv("Alias", in.alias)
	}
	if in.project != "" {
		p.kv("Project", in.project)
	}
	p.kv("Saved to", path)
	return nil
}

func projectInPage(page *chief.ProjectPage, id string) bool {
	for _, pr := range page.Data {
		if pr.ProjectID == id {
			return true
		}
	}
	return false
}
