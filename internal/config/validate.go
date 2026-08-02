// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package config

import (
	"fmt"
	"strings"

	goversion "github.com/hashicorp/go-version"
)

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
// before reporting. Independent checks all run; only checks that
// depend on an earlier failure (dedupe of an unparseable label,
// uniqueness of an invalid version) are skipped per element.
func buildManifest(raw *manifestHCL) (*Manifest, error) {
	var is issues

	mirror := buildMirror(raw.Mirror, &is)

	providers := make([]Provider, 0, len(raw.Providers))
	seen := make(map[string]struct{}, len(raw.Providers))
	for _, p := range raw.Providers {
		subject := `provider "` + p.Source + `"`

		source, ok := normalizeSource(p.Source)
		if !ok {
			is.add(subject, fmt.Sprintf(
				"invalid source address %q: want %q, e.g. %q",
				p.Source, "hostname/namespace/type", "registry.terraform.io/hashicorp/aws"))
		} else {
			// MergeAppend erases file boundaries, so a label declared
			// in two files arrives here as two blocks. The normalized
			// label is the identity; a repeat is always an error.
			if _, dup := seen[source]; dup {
				is.add(subject,
					"declared more than once across the config; merge is explicit, "+
						"never a silent union — consolidate into one block")
				continue
			}
			seen[source] = struct{}{}
		}

		providers = append(providers, buildProvider(p, source, mirror.Platforms, &is))
	}

	if err := is.err(); err != nil {
		return nil, err
	}
	return &Manifest{Mirror: mirror, Providers: providers}, nil
}

func buildMirror(raw mirrorHCL, is *issues) Mirror {
	if len(raw.Platforms) == 0 {
		is.add("mirror", "platforms must not be empty; declare the default os_arch matrix")
	}
	return Mirror{
		Bucket:    raw.Bucket,
		Region:    raw.Region,
		Platforms: parsePlatforms("mirror", raw.Platforms, is),
	}
}

func buildProvider(raw providerHCL, source string, inherited []Platform, is *issues) Provider {
	subject := `provider "` + raw.Source + `"`
	if source == "" {
		source = raw.Source
	}

	platforms := inherited
	if raw.Platforms != nil {
		if len(raw.Platforms) == 0 {
			is.add(subject, "platforms override must not be empty; omit the attribute to inherit mirror.platforms")
		}
		platforms = parsePlatforms(subject, raw.Platforms, is)
	}

	return Provider{
		Source:    source,
		Versions:  normalizeVersions(subject, raw.Versions, is),
		Platforms: platforms,
	}
}

// normalizeSource validates a provider label as a registry source
// address and returns its normalized (lowercase) form. Source
// addresses are case-insensitive; normalizing on intake makes the
// duplicate check and every downstream path computation
// case-stable.
func normalizeSource(s string) (string, bool) {
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", false
		}
	}
	host := strings.ToLower(parts[0])
	if !validHostname(host) {
		return "", false
	}
	return host + "/" + strings.ToLower(parts[1]) + "/" + strings.ToLower(parts[2]), true
}

// validHostname checks a lowercased registry hostname: dot-separated
// DNS labels of [a-z0-9-] with no leading or trailing hyphen, and at
// least one dot (a bare word is a namespace, not a hostname).
func validHostname(host string) bool {
	if !strings.Contains(host, ".") {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			default:
				return false
			}
		}
	}
	return true
}

// normalizeVersions validates each entry as an exact version and
// returns the normalized strings ("6.3" becomes "6.3.0") in
// declaration order. The normalized form is what the mirror protocol
// and registry paths consume, so uniqueness is checked on it.
func normalizeVersions(subject string, raws []string, is *issues) []string {
	if len(raws) == 0 {
		is.add(subject, "versions must not be empty; to remove a provider, delete its block")
		return nil
	}

	out := make([]string, 0, len(raws))
	seen := make(map[string]string, len(raws)) // normalized -> raw as first declared
	for i, s := range raws {
		if strings.HasPrefix(s, "v") || strings.HasPrefix(s, "V") {
			is.add(subject, fmt.Sprintf(
				"versions[%d] %q: registry versions are unprefixed; drop the leading %q",
				i, s, s[:1]))
			continue
		}

		v, err := goversion.NewVersion(s)
		if err != nil {
			if _, cerr := goversion.NewConstraint(s); cerr == nil {
				is.add(subject, fmt.Sprintf(
					"versions[%d] %q: version constraints are not supported — sluice "+
						"publishes exact version sets and never resolves; pin the exact version",
					i, s))
			} else {
				is.add(subject, fmt.Sprintf("versions[%d] %q is not a valid version", i, s))
			}
			continue
		}

		norm := v.String()
		if first, dup := seen[norm]; dup {
			if s == first {
				is.add(subject, fmt.Sprintf("duplicate version %q", s))
			} else {
				is.add(subject, fmt.Sprintf(
					"duplicate version %q (%q normalizes to it)", first, s))
			}
			continue
		}
		seen[norm] = s
		out = append(out, norm)
	}
	return out
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
