package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
)

func newDoctorCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "doctor",
		Short:       "Diagnose local chief configuration",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{annotationSkipClient: "1"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep := state.diagnose(cmd.Context())
			return state.printer.emit(rep, func() { state.renderDoctorReport(rep) })
		},
	}
	return cmd
}

// doctorReport holds the diagnosis. keyPresent and projectFound drive the
// rendered status but stay out of the JSON wire shape.
type doctorReport struct {
	CredentialsPath          string          `json:"credentials_path"`
	CredentialsExists        bool            `json:"credentials_exists"`
	CredentialsWorldReadable bool            `json:"credentials_world_readable"`
	CredentialsParseOK       bool            `json:"credentials_parse_ok"`
	CredentialsParseError    string          `json:"credentials_parse_error,omitempty"`
	HostCount                int             `json:"host_count"`
	CurrentHost              string          `json:"current_host"`
	Env                      map[string]bool `json:"env"`
	BaseURL                  string          `json:"base_url"`
	BaseURLSource            string          `json:"base_url_source"`
	APIKey                   string          `json:"api_key"`
	APIKeySource             string          `json:"api_key_source"`
	Project                  string          `json:"project"`
	ProjectSource            string          `json:"project_source"`
	Insecure                 bool            `json:"insecure"`
	InsecureSource           string          `json:"insecure_source"`
	Reachable                bool            `json:"reachable"`
	ProjectCount             int             `json:"project_count"`
	ConnectivityError        string          `json:"connectivity_error,omitempty"`

	keyPresent   bool
	projectFound bool
}

// diagnose reads the credential file and effective config, then probes
// connectivity if an API key is set.
func (state *app) diagnose(ctx context.Context) doctorReport {
	rc := *state.resolved
	rep := doctorReport{
		BaseURL:        rc.BaseURL,
		BaseURLSource:  rc.BaseURLSource,
		APIKey:         maskSecret(rc.APIKey),
		APIKeySource:   rc.APIKeySource,
		Project:        rc.Project,
		ProjectSource:  rc.ProjectSource,
		Insecure:       rc.Insecure,
		InsecureSource: rc.InsecureSource,
		keyPresent:     rc.APIKey != "",
		Env: map[string]bool{
			chief.EnvBaseURL:   os.Getenv(chief.EnvBaseURL) != "",
			chief.EnvAPIKey:    os.Getenv(chief.EnvAPIKey) != "",
			chief.EnvProjectID: os.Getenv(chief.EnvProjectID) != "",
		},
	}

	if path, err := credentialsPath(); err == nil {
		rep.CredentialsPath = path
		if info, statErr := os.Stat(path); statErr == nil {
			rep.CredentialsExists = true
			rep.CredentialsWorldReadable = info.Mode().Perm()&0o077 != 0
		}
	}

	creds, loadErr := loadCredentials()
	rep.CredentialsParseOK = loadErr == nil
	if loadErr != nil {
		rep.CredentialsParseError = loadErr.Error()
		creds = &credentials{Hosts: map[string]*hostConfig{}}
	}
	rep.HostCount = len(creds.Hosts)
	rep.CurrentHost = creds.Current

	if rc.APIKey != "" {
		rep.probe(ctx, rc)
	}
	return rep
}

// probe lists projects to fill the connectivity fields.
func (rep *doctorReport) probe(ctx context.Context, rc resolvedConfig) {
	c, err := newChiefClient(rc.BaseURL, rc.APIKey, rc.Project, rc.Insecure, false)
	if err != nil {
		rep.ConnectivityError = err.Error()
		return
	}

	page, err := c.Projects.List(ctx)
	switch {
	case chief.IsUnauthorized(err):
		rep.ConnectivityError = "invalid API key"
	case err != nil:
		rep.ConnectivityError = err.Error()
	default:
		rep.Reachable = true
		rep.ProjectCount = len(page.Data)
		rep.projectFound = rc.Project == "" || projectInPage(page, rc.Project)
	}
}

func (state *app) renderDoctorReport(rep doctorReport) {
	p := state.printer

	p.kv("Credentials", rep.CredentialsPath)
	switch {
	case !rep.CredentialsExists:
		p.line(fmt.Sprintf("%s file does not exist yet", p.skip.Render("⚠")))
	case rep.CredentialsWorldReadable:
		p.line(fmt.Sprintf("%s credentials file is group/world-readable; run chmod 600 %s", p.skip.Render("⚠"), rep.CredentialsPath))
	}

	if !rep.CredentialsParseOK {
		p.line(fmt.Sprintf("%s parse failed: %s", p.fail.Render("✗"), rep.CredentialsParseError))
	} else {
		p.kv("Hosts", fmt.Sprintf("%d configured", rep.HostCount))
		if rep.CurrentHost != "" {
			p.kv("Current host", rep.CurrentHost)
		}
	}

	p.kv(chief.EnvBaseURL, setUnset(rep.Env[chief.EnvBaseURL]))
	p.kv(chief.EnvAPIKey, setUnset(rep.Env[chief.EnvAPIKey]))
	p.kv(chief.EnvProjectID, setUnset(rep.Env[chief.EnvProjectID]))

	p.kv("Base URL", fmt.Sprintf("%s (%s)", rep.BaseURL, rep.BaseURLSource))
	p.kv("API key", fmt.Sprintf("%s (%s)", rep.APIKey, rep.APIKeySource))
	p.kv("Project", fmt.Sprintf("%s (%s)", rep.Project, rep.ProjectSource))
	p.kv("Insecure", fmt.Sprintf("%t (%s)", rep.Insecure, rep.InsecureSource))

	switch {
	case !rep.keyPresent:
		p.line(fmt.Sprintf("%s not authenticated; run chief login", p.skip.Render("⚠")))
	case rep.ConnectivityError == "invalid API key":
		p.line(fmt.Sprintf("%s invalid API key", p.fail.Render("✗")))
	case rep.ConnectivityError != "":
		p.line(fmt.Sprintf("%s %s", p.fail.Render("✗"), rep.ConnectivityError))
	default:
		p.line(fmt.Sprintf("%s reachable — %d projects", p.ok.Render("✓"), rep.ProjectCount))
		if rep.Project != "" && !rep.projectFound {
			p.line(fmt.Sprintf("%s project %s not found among your projects", p.skip.Render("⚠"), rep.Project))
		}
	}
}

func setUnset(b bool) string {
	if b {
		return "set"
	}
	return "unset"
}
