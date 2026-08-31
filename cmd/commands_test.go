package xportalcmd

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
	"go.lumeweb.com/xportal"
)

// newTestBuildCommand returns a fresh command with the same flags that
// buildCommand registers, so tests don't depend on shared global flag state.
func newTestBuildCommand(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "build"}
	cmd.Flags().StringArray("with", []string{}, "")
	cmd.Flags().StringArray("replace", []string{}, "")
	cmd.Flags().StringArray("exclude", []string{}, "")
	return cmd
}

func TestParseExcludeArgs(t *testing.T) {
	cmd := newTestBuildCommand(t)
	if err := cmd.Flags().Set("exclude", "foo/bar@v1.2.3"); err != nil {
		t.Fatalf("Set exclude: %v", err)
	}
	if err := cmd.Flags().Set("exclude", "baz/qux@v2.0.0"); err != nil {
		t.Fatalf("Set exclude: %v", err)
	}

	plugins, replacements, exclusions, err := parsePluginsAndReplacements(cmd)
	if err != nil {
		t.Fatalf("parsePluginsAndReplacements: %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("plugins = %v, want none", plugins)
	}
	if len(replacements) != 0 {
		t.Errorf("replacements = %v, want none", replacements)
	}

	want := []xportal.Exclude{
		xportal.NewExclude("foo/bar", "v1.2.3"),
		xportal.NewExclude("baz/qux", "v2.0.0"),
	}
	if !reflect.DeepEqual(exclusions, want) {
		t.Errorf("exclusions = %+v, want %+v", exclusions, want)
	}
}

func TestParseExcludeTrimsTrailingSlash(t *testing.T) {
	cmd := newTestBuildCommand(t)
	if err := cmd.Flags().Set("exclude", "foo/bar/@v1.2.3"); err != nil {
		t.Fatalf("Set exclude: %v", err)
	}

	_, _, exclusions, err := parsePluginsAndReplacements(cmd)
	if err != nil {
		t.Fatalf("parsePluginsAndReplacements: %v", err)
	}
	want := []xportal.Exclude{xportal.NewExclude("foo/bar", "v1.2.3")}
	if !reflect.DeepEqual(exclusions, want) {
		t.Errorf("exclusions = %+v, want %+v", exclusions, want)
	}
}

func TestParseExcludeWithoutVersion(t *testing.T) {
	cmd := newTestBuildCommand(t)
	if err := cmd.Flags().Set("exclude", "foo/bar"); err != nil {
		t.Fatalf("Set exclude: %v", err)
	}

	_, _, _, err := parsePluginsAndReplacements(cmd)
	if err == nil {
		t.Fatal("expected error for exclude without version, got nil")
	}
}

func TestParseExclusionAlongsideWithReplace(t *testing.T) {
	cmd := newTestBuildCommand(t)
	if err := cmd.Flags().Set("with", "go.lumeweb.com/portal-plugin-dashboard@v1.0.0"); err != nil {
		t.Fatalf("Set with: %v", err)
	}
	if err := cmd.Flags().Set("replace", "github.com/foo/bar=v1.3.0"); err != nil {
		t.Fatalf("Set replace: %v", err)
	}
	if err := cmd.Flags().Set("exclude", "github.com/foo/bar@v1.2.0"); err != nil {
		t.Fatalf("Set exclude: %v", err)
	}

	plugins, replacements, exclusions, err := parsePluginsAndReplacements(cmd)
	if err != nil {
		t.Fatalf("parsePluginsAndReplacements: %v", err)
	}

	if len(plugins) != 1 || plugins[0].PackagePath != "go.lumeweb.com/portal-plugin-dashboard" {
		t.Errorf("plugins = %+v, want 1 dashboard plugin", plugins)
	}
	if len(replacements) != 1 || replacements[0].Old.String() != "github.com/foo/bar" {
		t.Errorf("replacements = %+v, want 1 foo/bar replace", replacements)
	}
	if len(exclusions) != 1 || exclusions[0] != (xportal.Exclude{Module: "github.com/foo/bar", Version: "v1.2.0"}) {
		t.Errorf("exclusions = %+v, want foo/bar@v1.2.0", exclusions)
	}
}
