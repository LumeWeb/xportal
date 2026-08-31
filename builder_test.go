package xportal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExcludeString(t *testing.T) {
	e := NewExclude("foo/bar", "v1.2.3")
	if got, want := e.String(), "foo/bar@v1.2.3"; got != want {
		t.Fatalf("Exclude.String() = %q, want %q", got, want)
	}
}

func TestNewExclude(t *testing.T) {
	e := NewExclude("foo/bar", "v2.0.0")
	if e.Module != "foo/bar" {
		t.Errorf("Module = %q, want %q", e.Module, "foo/bar")
	}
	if e.Version != "v2.0.0" {
		t.Errorf("Version = %q, want %q", e.Version, "v2.0.0")
	}
}

func TestBuilderExclusionsJSON(t *testing.T) {
	b := Builder{Exclusions: []Exclude{NewExclude("foo/bar", "v1.2.3")}}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Builder
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got.Exclusions) != 1 {
		t.Fatalf("Exclusions length = %d, want 1", len(got.Exclusions))
	}
	if got.Exclusions[0] != b.Exclusions[0] {
		t.Errorf("Exclusions[0] = %+v, want %+v", got.Exclusions[0], b.Exclusions[0])
	}
}

// buildTempPlugin creates a dependency plugin module at a sibling of tempRoot
// and returns a main module (tempRoot) that requires it via a local replace,
// so resolution works without network access.
func buildTempPlugin(t *testing.T) (tempRoot, pluginDir string) {
	t.Helper()
	tempRoot = t.TempDir()
	pluginDir = filepath.Join(filepath.Dir(tempRoot), "plugin-module")
	if err := os.RemoveAll(pluginDir); err != nil {
		t.Fatalf("cleaning plugin dir: %v", err)
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("creating plugin dir: %v", err)
	}
	pluginMod := filepath.Join(pluginDir, "go.mod")
	if err := os.WriteFile(pluginMod, []byte("module example.com/plugin\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("writing plugin go.mod: %v", err)
	}

	mainMod := `module mainportal

go 1.22

require example.com/plugin v0.0.0

replace example.com/plugin => ` + filepath.ToSlash(pluginDir) + `
`
	if err := os.WriteFile(filepath.Join(tempRoot, "go.mod"), []byte(mainMod), 0o644); err != nil {
		t.Fatalf("writing main go.mod: %v", err)
	}

	t.Cleanup(func() { os.RemoveAll(pluginDir) })
	return tempRoot, pluginDir
}

func TestMakeWritable(t *testing.T) {
	dir := t.TempDir()

	// nested structure including a file and a directory
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o555); err != nil {
		t.Fatalf("creating subdir: %v", err)
	}
	file := filepath.Join(sub, "asset.txt")
	if err := os.WriteFile(file, []byte("data"), 0o444); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	if err := makeWritable(dir); err != nil {
		t.Fatalf("makeWritable returned error: %v", err)
	}

	for _, p := range []string{dir, sub, file} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if info.Mode()&0o200 == 0 {
			t.Errorf("expected user-write bit on %s, got mode %v", p, info.Mode())
		}
	}
}

func TestPluginModuleDir(t *testing.T) {
	tempRoot, pluginDir := buildTempPlugin(t)
	env := environment{tempFolder: tempRoot}

	got, err := env.pluginModuleDir(context.Background(), "example.com/plugin")
	if err != nil {
		t.Fatalf("pluginModuleDir returned error: %v", err)
	}

	// both paths are on the same filesystem, so compare resolved forms
	if !strings.EqualFold(filepath.Clean(got), filepath.Clean(pluginDir)) {
		t.Errorf("pluginModuleDir = %q, want %q", got, pluginDir)
	}
}

func TestPluginModuleDirUnknownModule(t *testing.T) {
	tempRoot, _ := buildTempPlugin(t)
	env := environment{tempFolder: tempRoot}

	if _, err := env.pluginModuleDir(context.Background(), "example.com/does-not-exist"); err == nil {
		t.Errorf("expected error for unknown module, got nil")
	}
}
