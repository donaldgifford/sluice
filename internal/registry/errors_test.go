// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"errors"
	"fmt"
	"testing"
)

// The tamper suite's contract: every failure surfaces as *Error
// carrying the exact tuple, matches its sentinel via errors.Is, and
// keeps the %w chain intact underneath.
func TestErrorContract(t *testing.T) {
	t.Parallel()

	addr := providerAddr{"registry.terraform.io", "hashicorp", "aws"}
	inner := fmt.Errorf("openpgp: %w", ErrSignature)
	err := fail(addr, "6.3.0", "linux_amd64", "signature", inner)

	want := "verify registry.terraform.io/hashicorp/aws 6.3.0 linux_amd64 (signature): openpgp: sums signature verification failed"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, ErrSignature) {
		t.Error("errors.Is(err, ErrSignature) = false, want true")
	}

	var re *Error
	if !errors.As(err, &re) {
		t.Fatal("errors.As(*Error) = false, want true")
	}
	if re.Source != "registry.terraform.io/hashicorp/aws" || re.Version != "6.3.0" ||
		re.Platform != "linux_amd64" || re.Step != "signature" {
		t.Errorf("tuple = %+v, want (registry.terraform.io/hashicorp/aws, 6.3.0, linux_amd64, signature)", re)
	}
}
