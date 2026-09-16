// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/sluice/internal/config"
)

// URL schemes accepted for an S3 endpoint (shared with bucket.go;
// goconst keeps the repeated literal in one place).
const (
	httpScheme  = "http"
	httpsScheme = "https"
)

// backendFlags carries the S3-backend overrides shared by every command
// that reads the manifest. Resolution precedence is explicit flags beat
// environment (SLUICE_S3_ENDPOINT / SLUICE_S3_PATH_STYLE), which beat HCL
// (DESIGN-0005); the resolved pair lands back on the manifest so bucket
// construction sees one effective config.
type backendFlags struct {
	cmd       *cobra.Command
	endpoint  string
	pathStyle bool
}

// addBackendFlags registers --s3-endpoint/--s3-path-style on cmd.
func addBackendFlags(cmd *cobra.Command) *backendFlags {
	bf := &backendFlags{cmd: cmd}
	cmd.Flags().StringVar(&bf.endpoint, "s3-endpoint", "",
		"S3 API base URL for S3-compatible backends (empty = AWS)")
	cmd.Flags().BoolVar(&bf.pathStyle, "s3-path-style", false,
		"force path-style addressing (required with --s3-endpoint)")
	return bf
}

// resolve computes the effective (endpoint, pathStyle) triple-source:
// explicit flags, then environment, then the manifest. An endpoint with
// path-style unset at every layer is an error, matching the manifest
// validation rule — addressing ambiguity fails closed.
func (bf *backendFlags) resolve(m *config.Manifest) (string, bool, error) {
	endpoint := m.Mirror.Endpoint
	pathStyle := m.Mirror.PathStyle

	if v, ok := os.LookupEnv("SLUICE_S3_ENDPOINT"); ok && v != "" {
		endpoint = v
	}
	if v, ok := os.LookupEnv("SLUICE_S3_PATH_STYLE"); ok && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return "", false, fmt.Errorf(
				"invalid SLUICE_S3_PATH_STYLE %q: want true or false", v)
		}
		pathStyle = b
	}

	if bf.cmd.Flags().Changed("s3-endpoint") {
		endpoint = bf.endpoint
	}
	if bf.cmd.Flags().Changed("s3-path-style") {
		pathStyle = bf.pathStyle
	}

	// Flag and environment values bypass HCL validation, so the shape
	// check lives here too: every command sees one valid config.
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Scheme != httpScheme && u.Scheme != httpsScheme) || u.Host == "" {
			return "", false, fmt.Errorf(
				"endpoint %q is not a valid http(s) URL; give the S3 API base URL or omit endpoint for AWS",
				endpoint)
		}
	}
	if endpoint != "" && !pathStyle {
		return "", false, fmt.Errorf(
			"path_style must be true when endpoint is set; S3-compatible " +
				"backends require path-style addressing")
	}
	return endpoint, pathStyle, nil
}

// apply writes the resolved backend back onto the manifest.
func (bf *backendFlags) apply(m *config.Manifest) error {
	endpoint, pathStyle, err := bf.resolve(m)
	if err != nil {
		return err
	}
	m.Mirror.Endpoint = endpoint
	m.Mirror.PathStyle = pathStyle
	return nil
}
