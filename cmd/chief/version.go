package main

import "runtime/debug"

// version is the release version, injected at build time via -ldflags. Local
// builds leave it empty and fall back to the VCS revision the toolchain embeds.
var version = ""

// buildVersion reports the injected release version, or the VCS revision of a
// local build, or "dev" when neither is available.
func buildVersion() string {
	if version != "" {
		return version
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}

	var revision string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}

	if revision == "" {
		return "dev"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if dirty {
		return revision + "-dirty"
	}
	return revision
}
