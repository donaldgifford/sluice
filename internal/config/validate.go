// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package config

import "strings"

// ValidationError reports semantic rule violations found after a
// clean decode. It renders one line per issue, each self-contained
// and actionable.
type ValidationError struct {
	Issues []Issue
}

// Issue is a single rule violation, addressed by the block it
// occurred in.
type Issue struct {
	// Subject names the offending block: `mirror` or
	// `provider "hostname/namespace/type"`.
	Subject string

	Message string
}

func (e *ValidationError) Error() string {
	lines := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		lines = append(lines, issue.Subject+": "+issue.Message)
	}
	return strings.Join(lines, "\n")
}

// issues accumulates rule violations across the semantic pass so CI
// users see every problem in one run.
type issues struct {
	list []Issue
}

func (is *issues) add(subject, message string) {
	is.list = append(is.list, Issue{Subject: subject, Message: message})
}

func (is *issues) err() error {
	if len(is.list) == 0 {
		return nil
	}
	return &ValidationError{Issues: is.list}
}

// buildManifest converts the decoded HCL into the validated public
// model, running every semantic rule and collecting all violations
// before reporting.
func buildManifest(raw *manifestHCL) (*Manifest, error) {
	var is issues

	mirror := buildMirror(raw.Mirror, &is)

	providers := make([]Provider, 0, len(raw.Providers))
	seen := make(map[string]struct{}, len(raw.Providers))
	for _, p := range raw.Providers {
		// MergeAppend erases file boundaries, so a label declared in
		// two files arrives here as two blocks. The label is the
		// identity; a repeat is always an error.
		if _, dup := seen[p.Source]; dup {
			is.add(`provider "`+p.Source+`"`,
				"declared more than once across the config; merge is explicit, "+
					"never a silent union — consolidate into one block")
			continue
		}
		seen[p.Source] = struct{}{}
		providers = append(providers, buildProvider(p, mirror.Platforms, &is))
	}

	if err := is.err(); err != nil {
		return nil, err
	}
	return &Manifest{Mirror: mirror, Providers: providers}, nil
}

func buildMirror(raw mirrorHCL, is *issues) Mirror {
	return Mirror{
		Bucket:    raw.Bucket,
		Region:    raw.Region,
		Platforms: parsePlatforms("mirror", raw.Platforms, is),
	}
}

func buildProvider(raw providerHCL, inherited []Platform, is *issues) Provider {
	subject := `provider "` + raw.Source + `"`

	platforms := inherited
	if raw.Platforms != nil {
		platforms = parsePlatforms(subject, raw.Platforms, is)
	}

	return Provider{
		Source:    raw.Source,
		Versions:  raw.Versions,
		Platforms: platforms,
	}
}

// parsePlatforms maps os_arch strings through the curated matrix,
// reporting each unknown entry as its own issue.
func parsePlatforms(subject string, names []string, is *issues) []Platform {
	platforms := make([]Platform, 0, len(names))
	for _, name := range names {
		p, err := ParsePlatform(name)
		if err != nil {
			is.add(subject, err.Error())
			continue
		}
		platforms = append(platforms, p)
	}
	return platforms
}
