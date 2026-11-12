package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoticeInputsAndToolchainCannotDriftSilently(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module fixture\ntoolchain go1.27.4\n")
	write("licenses/go-modules.json", `{"toolchain":"go1.27.4"}`)
	write("licenses/go.txt", "Go notice\r\n")
	write("web/static/third-party-notices.txt", "Frontend notice\n")
	if err := run(root, false, false); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true, false); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(root, outputName))
	if err != nil || strings.Contains(string(first), "\r") {
		t.Fatalf("notice output must use LF: %v", err)
	}
	write("web/static/third-party-notices.txt", "Updated frontend notice\n")
	if err := run(root, true, false); err == nil {
		t.Fatal("stale notices passed check")
	}
	after, _ := os.ReadFile(filepath.Join(root, outputName))
	if string(after) != string(first) {
		t.Fatal("check changed the output")
	}
	write("go.mod", "module fixture\ntoolchain go1.28.1\n")
	if err := run(root, false, false); err == nil {
		t.Fatal("toolchain drift did not require a new audit")
	}
}

func TestRuntimeInventoryRequiresReviewedUnion(t *testing.T) {
	const dependency = "example.org/library"
	recorded := inventory{Targets: []string{"linux/amd64", "windows/amd64"}, Modules: map[string]string{dependency: "v1.0.0"}}
	for _, tc := range []struct {
		name, listing string
		fail          bool
	}{
		{"same", "\n" + dependency + "@v1.0.0\n", false},
		{"upgrade", dependency + "@v2.0.0\n", true},
		{"new", dependency + "@v1.0.0\nexample.org/new@v1.0.0\n", true},
		{"removed", "\n", true},
		{"replacement", dependency + "@v1.0.0|replacement\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := verifyModules(".", recorded, func(_, target, tags string) ([]byte, error) {
				calls++
				// A dependency can exist only in one platform/tag combination.
				if target == "windows/amd64" && tags == "postgres" {
					return []byte(tc.listing), nil
				}
				return nil, nil
			})
			if (err != nil) != tc.fail || calls != 4 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
	err := verifyModules(".", recorded, func(_, _, _ string) ([]byte, error) { return nil, errors.New("go list failed") })
	if err == nil {
		t.Fatal("failed inventory command accepted")
	}
}

func TestCompilerMustMatchAuditedRuntimeSource(t *testing.T) {
	recorded := inventory{Compilers: []string{"go1.26.11", "go1.27.4"}}
	for _, version := range []string{"go1.26.11", "go1.27.4", "go1.26.0", "go1.28.0", "devel go1.27-abcdef", ""} {
		want := version == "go1.26.11" || version == "go1.27.4"
		if err := checkCompiler(recorded, version); (err == nil) != want {
			t.Fatalf("compiler %q: %v", version, err)
		}
	}
}
