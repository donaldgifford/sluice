// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package mirror

import (
	"encoding/json"
	"testing"
)

// The literal protocol bytes from the spec's "Mirror layout" section:
// the contract Terraform actually reads.
const (
	specIndex      = `{"versions":{"6.2.0":{},"6.3.0":{}}}`
	specVersionDoc = `{"archives":{"linux_amd64":{"url":"terraform-provider-aws_6.3.0_linux_amd64.zip","hashes":["h1:abc123"]}}}`
)

func TestIndexRoundTrip(t *testing.T) {
	t.Parallel()

	var idx Index
	if err := json.Unmarshal([]byte(specIndex), &idx); err != nil {
		t.Fatalf("unmarshal spec index: %v", err)
	}
	if len(idx.Versions) != 2 {
		t.Fatalf("Versions = %v, want 2 entries", idx.Versions)
	}
	if _, ok := idx.Versions["6.3.0"]; !ok {
		t.Fatalf("Versions = %v, want a 6.3.0 key", idx.Versions)
	}

	out, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// encoding/json sorts map keys, so the round trip is
	// byte-identical to the spec literal.
	if string(out) != specIndex {
		t.Fatalf("round trip = %s, want %s", out, specIndex)
	}
}

func TestVersionDocRoundTrip(t *testing.T) {
	t.Parallel()

	var doc VersionDoc
	if err := json.Unmarshal([]byte(specVersionDoc), &doc); err != nil {
		t.Fatalf("unmarshal spec version doc: %v", err)
	}
	a, ok := doc.Archives["linux_amd64"]
	if !ok {
		t.Fatalf("Archives = %v, want a linux_amd64 key", doc.Archives)
	}
	if a.URL != "terraform-provider-aws_6.3.0_linux_amd64.zip" {
		t.Fatalf("URL = %q, want the spec zip name", a.URL)
	}
	if len(a.Hashes) != 1 || a.Hashes[0] != "h1:abc123" {
		t.Fatalf("Hashes = %v, want [h1:abc123]", a.Hashes)
	}

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != specVersionDoc {
		t.Fatalf("round trip = %s, want %s", out, specVersionDoc)
	}
}
