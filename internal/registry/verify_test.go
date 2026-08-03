// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"errors"
	"strings"
	"testing"
)

func TestVerifySums(t *testing.T) {
	t.Parallel()

	sums := []byte(strings.Repeat("ab", 32) + "  terraform-provider-null_3.2.4_linux_amd64.zip\n")

	t.Run("valid signature by the published key", func(t *testing.T) {
		t.Parallel()

		key := newTestKey(t, "release")
		keyID, _, err := verifySums(publishedKeys(t, key), sums, signDetached(t, key, sums), nil, false)
		if err != nil {
			t.Fatalf("verifySums() unexpected error: %v", err)
		}
		if want := key.PrimaryKey.KeyIdString(); keyID != want {
			t.Errorf("keyID = %q, want %q", keyID, want)
		}
	})

	t.Run("signature by the second of N published keys", func(t *testing.T) {
		t.Parallel()

		keyA, keyB := newTestKey(t, "old"), newTestKey(t, "rotated")
		keyID, _, err := verifySums(publishedKeys(t, keyA, keyB), sums, signDetached(t, keyB, sums), nil, false)
		if err != nil {
			t.Fatalf("verifySums() unexpected error: %v", err)
		}
		if want := keyB.PrimaryKey.KeyIdString(); keyID != want {
			t.Errorf("keyID = %q, want signer %q", keyID, want)
		}
	})

	t.Run("signature over different content fails", func(t *testing.T) {
		t.Parallel()

		key := newTestKey(t, "release")
		sig := signDetached(t, key, []byte("something else entirely\n"))
		_, _, err := verifySums(publishedKeys(t, key), sums, sig, nil, false)
		if !errors.Is(err, ErrSignature) {
			t.Fatalf("verifySums() error = %v, want ErrSignature", err)
		}
	})

	t.Run("signature by an unpublished key fails", func(t *testing.T) {
		t.Parallel()

		published, rogue := newTestKey(t, "release"), newTestKey(t, "rogue")
		_, _, err := verifySums(publishedKeys(t, published), sums, signDetached(t, rogue, sums), nil, false)
		if !errors.Is(err, ErrSignature) {
			t.Fatalf("verifySums() error = %v, want ErrSignature", err)
		}
	})

	t.Run("garbage signature bytes fail", func(t *testing.T) {
		t.Parallel()

		key := newTestKey(t, "release")
		_, _, err := verifySums(publishedKeys(t, key), sums, []byte("not a signature"), nil, false)
		if !errors.Is(err, ErrSignature) {
			t.Fatalf("verifySums() error = %v, want ErrSignature", err)
		}
	})

	t.Run("unparseable published key is a hard error", func(t *testing.T) {
		t.Parallel()

		key := newTestKey(t, "release")
		keys := []gpgKey{{KeyID: "JUNK", ASCIIArmor: "not armor at all"}}
		_, _, err := verifySums(keys, sums, signDetached(t, key, sums), nil, false)
		if err == nil || !strings.Contains(err.Error(), "parsing published signing key") {
			t.Fatalf("verifySums() error = %v, want key-parse failure", err)
		}
	})

	t.Run("no published keys is a hard error", func(t *testing.T) {
		t.Parallel()

		key := newTestKey(t, "release")
		_, _, err := verifySums(nil, sums, signDetached(t, key, sums), nil, false)
		if err == nil || !strings.Contains(err.Error(), "no usable signing keys") {
			t.Fatalf("verifySums() error = %v, want empty-keyring failure", err)
		}
	})
}

func TestSumsEntry(t *testing.T) {
	t.Parallel()

	hashA := strings.Repeat("ab", 32)
	hashB := strings.Repeat("cd", 32)
	zip := "terraform-provider-aws_6.3.0_linux_amd64.zip"

	tests := []struct {
		name     string
		sums     string
		filename string
		want     string
		wantErr  error  // sentinel match, if set
		wantMsg  string // substring match, if set
	}{
		{
			name:     "entry found among multiple platforms",
			sums:     hashA + "  " + zip + "\n" + hashB + "  terraform-provider-aws_6.3.0_darwin_arm64.zip\n",
			filename: zip,
			want:     hashA,
		},
		{
			name:     "missing entry",
			sums:     hashB + "  terraform-provider-aws_6.3.0_darwin_arm64.zip\n",
			filename: zip,
			wantErr:  ErrSumsEntryMissing,
		},
		{
			name:     "empty document",
			sums:     "",
			filename: zip,
			wantErr:  ErrSumsEntryMissing,
		},
		{
			name:     "single space separator is malformed",
			sums:     hashA + " " + zip + "\n",
			filename: zip,
			wantMsg:  "malformed SHA256SUMS line 1",
		},
		{
			name:     "uppercase hex is malformed",
			sums:     strings.ToUpper(hashA) + "  " + zip + "\n",
			filename: zip,
			wantMsg:  "malformed SHA256SUMS line 1",
		},
		{
			name:     "short hash is malformed",
			sums:     hashA[:60] + "  " + zip + "\n",
			filename: zip,
			wantMsg:  "malformed SHA256SUMS line",
		},
		{
			// The \r stays glued to the parsed name, so a CRLF doc
			// never matches the real filename — fail-closed as a
			// missing entry.
			name:     "crlf document does not match",
			sums:     hashA + "  " + zip + "\r\n",
			filename: zip,
			wantErr:  ErrSumsEntryMissing,
		},
		{
			name:     "duplicate with differing hashes is tampering",
			sums:     hashA + "  " + zip + "\n" + hashB + "  " + zip + "\n",
			filename: zip,
			wantMsg:  "twice with differing hashes",
		},
		{
			name:     "identical duplicate lines are harmless",
			sums:     hashA + "  " + zip + "\n" + hashA + "  " + zip + "\n",
			filename: zip,
			want:     hashA,
		},
		{
			name:     "blank lines tolerated",
			sums:     "\n" + hashA + "  " + zip + "\n\n",
			filename: zip,
			want:     hashA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := sumsEntry([]byte(tt.sums), tt.filename)
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("sumsEntry() error = %v, want %v", err, tt.wantErr)
				}
			case tt.wantMsg != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
					t.Fatalf("sumsEntry() error = %v, want containing %q", err, tt.wantMsg)
				}
			default:
				if err != nil {
					t.Fatalf("sumsEntry() unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("sumsEntry() = %q, want %q", got, tt.want)
				}
			}
		})
	}
}
