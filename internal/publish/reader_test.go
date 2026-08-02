// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package publish

import (
	"context"
	"strings"
	"testing"
)

const awsSrc = "registry.terraform.io/hashicorp/aws"

func seedProvider(f *fakeBucket, src string, versions map[string][]string) {
	idx := `{"versions":{`
	first := true
	for ver, plats := range versions {
		if !first {
			idx += ","
		}
		first = false
		idx += `"` + ver + `":{}`

		doc := `{"archives":{`
		for i, p := range plats {
			if i > 0 {
				doc += ","
			}
			doc += `"` + p + `":{"url":"terraform-provider-x_` + ver + `_` + p + `.zip","hashes":["h1:x"]}`
		}
		doc += `}}`
		f.seed(versionKey(src, ver), []byte(doc))
	}
	idx += `}}`
	f.seed(indexKey(src), []byte(idx))
}

func TestReadActual(t *testing.T) {
	t.Parallel()

	t.Run("empty bucket with no declarations", func(t *testing.T) {
		t.Parallel()

		a, err := ReadActual(context.Background(), newFakeBucket(), nil)
		if err != nil {
			t.Fatalf("ReadActual() unexpected error: %v", err)
		}
		if len(a.State) != 0 || len(a.ETags) != 0 {
			t.Errorf("ReadActual() = %+v, want empty", a)
		}
	})

	t.Run("declared but unmirrored provider is first publish", func(t *testing.T) {
		t.Parallel()

		a, err := ReadActual(context.Background(), newFakeBucket(), []string{awsSrc})
		if err != nil {
			t.Fatalf("ReadActual() unexpected error: %v", err)
		}
		if _, ok := a.State[awsSrc]; ok {
			t.Error("unmirrored provider must not appear in State")
		}
		if _, ok := a.ETags[awsSrc]; ok {
			t.Error("unmirrored provider must not carry an ETag")
		}
	})

	t.Run("mirrored providers map into state", func(t *testing.T) {
		t.Parallel()

		f := newFakeBucket()
		seedProvider(f, awsSrc, map[string][]string{
			"6.2.0": {"linux_amd64", "darwin_arm64"},
			"6.3.0": {"linux_amd64"},
		})
		// Zips and signatures at provider depth must be ignored.
		f.seed(zipKey(awsSrc, "terraform-provider-aws_6.2.0_linux_amd64.zip"), []byte("zip"))
		f.seed(zipKey(awsSrc, "terraform-provider-aws_6.2.0_linux_amd64.zip.sig"), []byte("sig"))

		a, err := ReadActual(context.Background(), f, nil)
		if err != nil {
			t.Fatalf("ReadActual() unexpected error: %v", err)
		}
		ps, ok := a.State[awsSrc]
		if !ok {
			t.Fatalf("State missing %s: %+v", awsSrc, a.State)
		}
		if len(ps) != 2 {
			t.Fatalf("versions = %d, want 2", len(ps))
		}
		if _, ok := ps["6.2.0"]["darwin_arm64"]; !ok {
			t.Error("6.2.0 missing darwin_arm64")
		}
		if a.ETags[awsSrc] == "" {
			t.Error("index ETag not captured")
		}
		if got := a.Docs[awsSrc]["6.3.0"].Archives["linux_amd64"].Hashes[0]; got != "h1:x" {
			t.Errorf("doc hash = %q, want h1:x", got)
		}
	})

	t.Run("undeclared mirrored provider is discovered", func(t *testing.T) {
		t.Parallel()

		// The removal path depends on LIST discovery: a provider
		// dropped from the manifest must still surface in actual
		// state.
		f := newFakeBucket()
		seedProvider(f, "registry.opentofu.org/hashicorp/null", map[string][]string{
			"3.2.4": {"linux_amd64"},
		})
		a, err := ReadActual(context.Background(), f, []string{awsSrc})
		if err != nil {
			t.Fatalf("ReadActual() unexpected error: %v", err)
		}
		if _, ok := a.State["registry.opentofu.org/hashicorp/null"]; !ok {
			t.Error("undeclared mirrored provider not discovered via LIST")
		}
	})

	t.Run("index referencing a missing version doc fails closed", func(t *testing.T) {
		t.Parallel()

		f := newFakeBucket()
		f.seed(indexKey(awsSrc), []byte(`{"versions":{"6.2.0":{}}}`))
		_, err := ReadActual(context.Background(), f, nil)
		if err == nil || !strings.Contains(err.Error(), "modified outside sluice") {
			t.Fatalf("ReadActual() error = %v, want out-of-band corruption failure", err)
		}
	})

	t.Run("unparseable index fails closed", func(t *testing.T) {
		t.Parallel()

		f := newFakeBucket()
		f.seed(indexKey(awsSrc), []byte("{corrupt"))
		_, err := ReadActual(context.Background(), f, nil)
		if err == nil || !strings.Contains(err.Error(), "parsing") {
			t.Fatalf("ReadActual() error = %v, want parse failure", err)
		}
	})

	t.Run("unparseable version doc fails closed", func(t *testing.T) {
		t.Parallel()

		f := newFakeBucket()
		f.seed(indexKey(awsSrc), []byte(`{"versions":{"6.2.0":{}}}`))
		f.seed(versionKey(awsSrc, "6.2.0"), []byte("{corrupt"))
		_, err := ReadActual(context.Background(), f, nil)
		if err == nil || !strings.Contains(err.Error(), "parsing") {
			t.Fatalf("ReadActual() error = %v, want parse failure", err)
		}
	})
}

func TestDiscoverSources(t *testing.T) {
	t.Parallel()

	keys := []string{
		"registry.terraform.io/hashicorp/aws/index.json",
		"registry.terraform.io/hashicorp/aws/6.2.0.json",
		"registry.terraform.io/hashicorp/aws/terraform-provider-aws_6.2.0_linux_amd64.zip",
		"registry.terraform.io/hashicorp/aws/terraform-provider-aws_6.2.0_linux_amd64.zip.sig",
		"registry.opentofu.org/hashicorp/null/index.json",
		"index.json",                    // wrong depth
		"a/b/index.json",                // wrong depth
		"a/b/c/d/index.json",            // wrong depth
		"a/b/c/notindex.json",           // wrong name
		"a/b/c/index.json.bak",          // suffix noise
		"registry.example.com/x/y/data", // not an index
	}
	got := discoverSources(keys)
	want := []string{
		"registry.terraform.io/hashicorp/aws",
		"registry.opentofu.org/hashicorp/null",
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("discoverSources() = %v, want %v", got, want)
	}
}

func TestKeys(t *testing.T) {
	t.Parallel()

	if got := indexKey(awsSrc); got != awsSrc+"/index.json" {
		t.Errorf("indexKey = %q", got)
	}
	if got := versionKey(awsSrc, "6.2.0"); got != awsSrc+"/6.2.0.json" {
		t.Errorf("versionKey = %q", got)
	}
	if got := zipKey(awsSrc, "terraform-provider-aws_6.2.0_linux_amd64.zip"); got != awsSrc+"/terraform-provider-aws_6.2.0_linux_amd64.zip" {
		t.Errorf("zipKey = %q", got)
	}
}
