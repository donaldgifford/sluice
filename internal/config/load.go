// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package config

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"

	"github.com/donaldgifford/hclkit/pkg/hclkit"
)

// LoadDir loads and validates every top-level *.hcl file in dir,
// merged as HCL bodies (the spec's config-directory semantics).
func LoadDir(dir string) (*Manifest, error) {
	return load(func(l *hclkit.Loader, target any) hclkit.Diagnostics {
		return l.LoadDir(dir, target)
	})
}

// LoadFile loads and validates a single manifest file.
func LoadFile(path string) (*Manifest, error) {
	return load(func(l *hclkit.Loader, target any) hclkit.Diagnostics {
		return l.LoadFile(path, target)
	})
}

// loadBytes is the test seam: identical pipeline, in-memory source.
func loadBytes(filename string, src []byte) (*Manifest, error) {
	return load(func(l *hclkit.Loader, target any) hclkit.Diagnostics {
		return l.LoadBytes(filename, src, target)
	})
}

// manifestHCL is the gohcl decode target. Mirror is a value field on
// purpose: gohcl then enforces the exactly-one rule itself, with real
// source positions on both the missing and the duplicate case — even
// when the duplicate lives in another merged file.
type manifestHCL struct {
	Mirror    mirrorHCL     `hcl:"mirror,block"`
	Providers []providerHCL `hcl:"provider,block"`
}

type mirrorHCL struct {
	Bucket    string   `hcl:"bucket"`
	Region    string   `hcl:"region"`
	Platforms []string `hcl:"platforms"`
}

type providerHCL struct {
	Source   string   `hcl:"source,label"`
	Versions []string `hcl:"versions"`

	// Platforms distinguishes absent (nil: inherit the mirror matrix)
	// from explicitly empty (non-nil zero length: a validation error).
	Platforms []string `hcl:"platforms,optional"`
}

// load runs the one pipeline every entry point shares: decode with
// merged-body semantics, then the semantic pass. There is no way to
// obtain a decoded-but-unvalidated manifest.
func load(loadInto func(*hclkit.Loader, any) hclkit.Diagnostics) (*Manifest, error) {
	loader := hclkit.New(hclkit.WithMergeMode(hclkit.MergeAppend))

	var raw manifestHCL
	if diags := loadInto(loader, &raw); diags.HasErrors() {
		return nil, &DiagnosticsError{Diags: diags}
	}
	return buildManifest(&raw)
}

// DiagnosticsError reports parse and decode failures. It renders one
// position-prefixed line per diagnostic; the wrapped Diags retain
// hcl's full source-snippet rendering via WriteTo for any future
// verbose mode.
type DiagnosticsError struct {
	Diags hclkit.Diagnostics
}

func (e *DiagnosticsError) Error() string {
	lines := make([]string, 0, len(e.Diags.Diagnostics))
	for _, d := range e.Diags.Diagnostics {
		lines = append(lines, renderDiagnostic(d))
	}
	return strings.Join(lines, "\n")
}

// renderDiagnostic formats one diagnostic as a single actionable line:
// "file:line,col: Summary: Detail" (position omitted when hcl has
// none).
func renderDiagnostic(d *hcl.Diagnostic) string {
	msg := d.Summary
	if d.Detail != "" {
		msg = d.Summary + ": " + d.Detail
	}
	if d.Subject == nil {
		return msg
	}
	s := d.Subject.Start
	return fmt.Sprintf("%s:%d,%d: %s", d.Subject.Filename, s.Line, s.Column, msg)
}
