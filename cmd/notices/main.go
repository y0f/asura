// Command notices assembles reviewed redistribution notices. It never infers
// licenses or accepts a new dependency on behalf of the reviewer.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type inventory struct {
	Toolchain string            `json:"toolchain"`
	Compilers []string          `json:"compilers"`
	Targets   []string          `json:"targets"`
	Modules   map[string]string `json:"modules"`
}

const outputName = "THIRD_PARTY_NOTICES.txt"
const heading = "Asura: third-party notices\n\nGenerated from licenses/go.txt and web/static/third-party-notices.txt.\nKeep this file with redistributed server and agent binaries. Asura's own\nlicense is supplied separately in LICENSE.\n\n"

func main() {
	check := flag.Bool("check", false, "fail if the combined notices are stale")
	verify := flag.Bool("verify", false, "compare audited module versions with all recorded runtime build targets")
	flag.Parse()
	if err := run(".", *check, *verify); err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}
}

func run(root string, check, verify bool) error {
	data, err := os.ReadFile(filepath.Join(root, "licenses/go-modules.json"))
	if err != nil {
		return err
	}
	var recorded inventory
	if err := json.Unmarshal(data, &recorded); err != nil {
		return fmt.Errorf("read audited inventory: %w", err)
	}
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return err
	}
	var toolchain string
	for line := range strings.SplitSeq(string(mod), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "toolchain" {
			toolchain = fields[1]
		}
	}
	if recorded.Toolchain == "" || recorded.Toolchain != toolchain {
		return errors.New("go.mod toolchain changed; review its licenses and update licenses/go-modules.json")
	}
	if verify {
		version, err := goOutput(root, "", "env", "GOVERSION")
		if err != nil {
			return err
		}
		if err := checkCompiler(recorded, strings.TrimSpace(string(version))); err != nil {
			return err
		}
		if err := verifyModules(root, recorded, listModules); err != nil {
			return err
		}
	}
	var combined bytes.Buffer
	combined.WriteString(heading)
	for index, name := range []string{"licenses/go.txt", "web/static/third-party-notices.txt"} {
		section, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return err
		}
		section = bytes.ReplaceAll(section, []byte("\r\n"), []byte("\n"))
		if len(bytes.TrimSpace(section)) == 0 {
			return fmt.Errorf("empty notice source: %s", name)
		}
		if index != 0 {
			combined.WriteByte('\n')
		}
		combined.Write(bytes.TrimRight(section, "\n"))
		combined.WriteByte('\n')
	}
	output := filepath.Join(root, outputName)
	if check {
		existing, err := os.ReadFile(output)
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, combined.Bytes()) {
			return errors.New("combined notices are stale; run go run ./cmd/notices and commit the result")
		}
		return nil
	}
	return os.WriteFile(output, combined.Bytes(), 0o644)
}

type moduleLister func(root, target, tags string) ([]byte, error)

func checkCompiler(recorded inventory, version string) error {
	if !slices.Contains(recorded.Compilers, version) {
		return fmt.Errorf("Go compiler %q is not audited; use %s or review its runtime source and notices", version, strings.Join(recorded.Compilers, ", "))
	}
	return nil
}

func verifyModules(root string, recorded inventory, list moduleLister) error {
	if len(recorded.Targets) == 0 || len(recorded.Modules) == 0 {
		return errors.New("audited runtime inventory must not be empty")
	}
	actual := make(map[string]string)
	for _, target := range recorded.Targets {
		if parts := strings.Split(target, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("invalid audited target %q", target)
		}
		for _, tags := range []string{"", "postgres"} {
			data, err := list(root, target, tags)
			if err != nil {
				return fmt.Errorf("runtime inventory %s tags=%q: %w", target, tags, err)
			}
			for line := range strings.SplitSeq(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				name, version, ok := strings.Cut(line, "@")
				if !ok || strings.Contains(version, "|") || version == "" {
					return fmt.Errorf("unreviewed module replacement or invalid inventory entry: %s", line)
				}
				if recorded.Modules[name] != version {
					return fmt.Errorf("review licenses for runtime dependency %s before updating licenses/go-modules.json", line)
				}
				actual[name] = version
			}
		}
	}
	var unused []string
	for name := range recorded.Modules {
		if actual[name] == "" {
			unused = append(unused, name)
		}
	}
	if len(unused) != 0 {
		slices.Sort(unused)
		return fmt.Errorf("review removed runtime dependencies: %s", strings.Join(unused, ", "))
	}
	return nil
}

func listModules(root, target, tags string) ([]byte, error) {
	return goOutput(root, target, "list", "-mod=readonly", "-deps", "-tags", tags, "-f",
		`{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}{{if .Replace}}|replacement{{end}}{{end}}{{end}}`,
		"./cmd/asura", "./cmd/asura-agent")
}

func goOutput(root, target string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off", "GOEXPERIMENT=", "GOAMD64=v1", "GOARM64=v8.0")
	if target != "" {
		parts := strings.Split(target, "/")
		cmd.Env = append(cmd.Env, "GOOS="+parts[0], "GOARCH="+parts[1])
	}
	// Download progress is stderr even when go list succeeds; it is not part
	// of the module inventory on a runner with an empty module cache.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s failed: %w\n%s", args[0], err, stderr.Bytes())
	}
	return output, nil
}
