package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"github.com/Storytell-ai/chief-go/chief"
	"golang.org/x/term"
)

// credentials is the on-disk store of hosts keyed by base URL, holding the PAT
// and per-host defaults so commands don't need the auth flags every call.
type credentials struct {
	Current string                 `json:"current,omitempty"`
	Hosts   map[string]*hostConfig `json:"hosts"`
}

type hostConfig struct {
	Alias    string `json:"alias,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	Project  string `json:"project,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
}

// credentialsPath resolves to $XDG_CONFIG_HOME/chief/credentials.json, or
// ~/.config/chief/credentials.json when XDG_CONFIG_HOME is unset.
func credentialsPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "chief", "credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", "chief", "credentials.json"), nil
}

// loadCredentials reads the store, returning an empty one when the file is
// absent so first-run commands work without a prior login.
func loadCredentials() (*credentials, error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &credentials{Hosts: map[string]*hostConfig{}}, nil
		}
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	var c credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse credentials %s: %w", path, err)
	}
	if c.Hosts == nil {
		c.Hosts = map[string]*hostConfig{}
	}
	return &c, nil
}

// save writes the store atomically. The file holds a PAT, so the directory is
// 0700 and the file 0600.
func (c *credentials) save() error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "credentials-*.json")
	if err != nil {
		return fmt.Errorf("create temp credentials: %w", err)
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("set credentials permissions: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace credentials: %w", err)
	}
	return nil
}

// normalizeBaseURL is the canonical map-key form for a host.
func normalizeBaseURL(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

func (c *credentials) host(baseURL string) *hostConfig {
	return c.Hosts[normalizeBaseURL(baseURL)]
}

func (c *credentials) ensureHost(baseURL string) *hostConfig {
	key := normalizeBaseURL(baseURL)
	h := c.Hosts[key]
	if h == nil {
		h = &hostConfig{}
		c.Hosts[key] = h
	}
	return h
}

// resolveHost maps a name or base URL to a host, matching an alias exactly
// before falling back to a normalized base URL.
func (c *credentials) resolveHost(nameOrURL string) (string, *hostConfig, bool) {
	for key, h := range c.Hosts {
		if h.Alias != "" && h.Alias == nameOrURL {
			return key, h, true
		}
	}
	key := normalizeBaseURL(nameOrURL)
	if h, ok := c.Hosts[key]; ok {
		return key, h, true
	}
	return "", nil, false
}

// aliasConflict reports the host key already using alias, ignoring selfKey.
func (c *credentials) aliasConflict(alias, selfKey string) (string, bool) {
	if alias == "" {
		return "", false
	}
	for key, h := range c.Hosts {
		if key != selfKey && h.Alias == alias {
			return key, true
		}
	}
	return "", false
}

func (c *credentials) sortedHostKeys() []string {
	keys := make([]string, 0, len(c.Hosts))
	for k := range c.Hosts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type configFlags struct {
	BaseURL     string
	BaseURLSet  bool
	APIKey      string
	APIKeySet   bool
	Project     string
	ProjectSet  bool
	Insecure    bool
	InsecureSet bool
}

// resolvedConfig is the effective configuration after applying the precedence
// flag > env > file > default. The Source fields record where each value came
// from, for doctor's diagnostics.
type resolvedConfig struct {
	BaseURL  string
	APIKey   string
	Project  string
	Insecure bool

	BaseURLSource  string
	APIKeySource   string
	ProjectSource  string
	InsecureSource string
}

// resolveConfig applies the precedence per field. The flag*Set booleans
// distinguish an explicit empty flag from an unset one.
func resolveConfig(c *credentials, f configFlags) resolvedConfig {
	rc := resolvedConfig{}

	switch {
	case f.BaseURLSet:
		rc.BaseURL, rc.BaseURLSource = f.BaseURL, "flag"
	case os.Getenv(chief.EnvBaseURL) != "":
		rc.BaseURL, rc.BaseURLSource = os.Getenv(chief.EnvBaseURL), "env"
	case c.Current != "":
		rc.BaseURL, rc.BaseURLSource = c.Current, "file"
	default:
		rc.BaseURL, rc.BaseURLSource = chief.DefaultBaseURL, "default"
	}
	rc.BaseURL = normalizeBaseURL(rc.BaseURL)

	host := c.host(rc.BaseURL)

	switch {
	case f.APIKeySet:
		rc.APIKey, rc.APIKeySource = f.APIKey, "flag"
	case os.Getenv(chief.EnvAPIKey) != "":
		rc.APIKey, rc.APIKeySource = os.Getenv(chief.EnvAPIKey), "env"
	case host != nil && host.APIKey != "":
		rc.APIKey, rc.APIKeySource = host.APIKey, "file"
	default:
		rc.APIKey, rc.APIKeySource = "", "default"
	}

	switch {
	case f.ProjectSet:
		rc.Project, rc.ProjectSource = f.Project, "flag"
	case os.Getenv(chief.EnvProjectID) != "":
		rc.Project, rc.ProjectSource = os.Getenv(chief.EnvProjectID), "env"
	case host != nil && host.Project != "":
		rc.Project, rc.ProjectSource = host.Project, "file"
	default:
		rc.Project, rc.ProjectSource = "", "default"
	}

	switch {
	case f.InsecureSet:
		rc.Insecure, rc.InsecureSource = f.Insecure, "flag"
	case host != nil:
		rc.Insecure, rc.InsecureSource = host.Insecure, "file"
	default:
		rc.Insecure, rc.InsecureSource = false, "default"
	}

	return rc
}

// maskSecret renders a secret for display, exposing only its last four
// characters.
func maskSecret(s string) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) <= 4 {
		return "••••"
	}
	return "••••" + s[len(s)-4:]
}

// isInteractive reports whether both stdin and stdout are terminals, the
// gate for prompting instead of failing on missing input.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// accessibleMode reports huh's ACCESSIBLE env var, which degrades prompts to
// plain text for screen readers.
func accessibleMode() bool {
	return os.Getenv("ACCESSIBLE") != ""
}

// promptLine reads one line interactively, returning def when the input is empty.
func promptLine(_ *printer, label, def string) (string, error) {
	var v string
	field := huh.NewInput().Title(label).Value(&v)
	if def != "" {
		field.Placeholder(def)
	}
	if err := huh.NewForm(huh.NewGroup(field)).
		WithShowHelp(false).
		WithAccessible(accessibleMode()).
		Run(); err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	if v = strings.TrimSpace(v); v == "" {
		return def, nil
	}
	return v, nil
}

// promptIndex prompts for a 1-based choice in [1,n] and returns its 0-based
// index, erroring on input that doesn't name a row.
func promptIndex(p *printer, label string, n int) (int, error) {
	choice, err := promptLine(p, label, "")
	if err != nil {
		return 0, err
	}
	i, err := strconv.Atoi(strings.TrimSpace(choice))
	if err != nil || i < 1 || i > n {
		return 0, fmt.Errorf("invalid selection %q", choice)
	}
	return i - 1, nil
}

// promptSecret reads a secret interactively without echoing it.
func promptSecret(_ *printer, label string) (string, error) {
	var v string
	field := huh.NewInput().Title(label).EchoMode(huh.EchoModePassword).Value(&v)
	if err := huh.NewForm(huh.NewGroup(field)).
		WithShowHelp(false).
		WithAccessible(accessibleMode()).
		Run(); err != nil {
		return "", fmt.Errorf("read secret: %w", err)
	}
	return strings.TrimSpace(v), nil
}

// confirmDelete reports whether a delete should proceed. It returns true without
// prompting when force is set, output is JSON, or stdout is not a terminal. On
// an interactive terminal it shows a confirmation defaulting to No.
func confirmDelete(p *printer, force bool, kind, id string) (bool, error) {
	if force || p.json || !isInteractive() {
		return true, nil
	}
	var ok bool
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(fmt.Sprintf("Delete %s %s?", kind, id)).Value(&ok),
	)).WithShowHelp(false).WithAccessible(accessibleMode()).Run(); err != nil {
		return false, fmt.Errorf("confirm delete: %w", err)
	}
	return ok, nil
}
