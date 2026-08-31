package xportal

import (
	"encoding/json"
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
