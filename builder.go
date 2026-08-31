// Copyright 2020 Matthew Holt
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package xportal

import (
	"bytes"
	"context"
	"fmt"
	"github.com/Masterminds/semver/v3"
	"github.com/google/shlex"
	"go.lumeweb.com/xportal/internal/utils"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "go.lumeweb.com/xportal/internal/utils"
)

// Builder can produce a custom Port build with the
// configuration it represents.
type Builder struct {
	Compile
	PortalVersion   string        `json:"portal_version,omitempty"`
	Plugins         []Dependency  `json:"plugins,omitempty"`
	Replacements    []Replace     `json:"replacements,omitempty"`
	TimeoutGet      time.Duration `json:"timeout_get,omitempty"`
	TimeoutBuild    time.Duration `json:"timeout_build,omitempty"`
	RaceDetector    bool          `json:"race_detector,omitempty"`
	SkipCleanup     bool          `json:"skip_cleanup,omitempty"`
	SkipBuild       bool          `json:"skip_build,omitempty"`
	Debug           bool          `json:"debug,omitempty"`
	BuildFlags      string        `json:"build_flags,omitempty"`
	BuildFlagsExtra string        `json:"build_flags_extra,omitempty"`
	ModFlags        string        `json:"mod_flags,omitempty"`
	ScratchMode     bool          `json:"scratch_mode,omitempty"`
	ScratchPath     string        `json:"scratch_path,omitempty"`
}

// Build builds Port at the configured version with the
// configured plugins and plops down a binary at outputFile.
func (b Builder) Build(ctx context.Context, outputFile string) error {
	var cancel context.CancelFunc
	if b.TimeoutBuild > 0 {
		ctx, cancel = context.WithTimeout(ctx, b.TimeoutBuild)
		defer cancel()
	}
	if outputFile == "" && !b.ScratchMode {
		return fmt.Errorf("output file path is required")
	}
	// the user's specified output file might be relative, and
	// because the `go build` command is executed in a different,
	// temporary folder, we convert the user's input to an
	// absolute path so it goes the expected place
	absOutputFile, err := filepath.Abs(outputFile)
	if err != nil {
		return err
	}
	log.Printf("[INFO] absolute output file path: %s", absOutputFile)

	if b.ScratchMode {
		b.ScratchPath, err = filepath.Abs(b.ScratchPath)
		if err != nil {
			return err
		}
	}

	// Set default environment values if not specified
	if b.OS == "" {
		b.OS = utils.GetGOOS()
	}
	if b.Arch == "" {
		b.Arch = utils.GetGOARCH()
	}
	if b.ARM == "" {
		b.ARM = os.Getenv("GOARM")
	}

	// Prepare build environment
	buildEnv, err := b.newEnvironment(ctx)
	if err != nil {
		return err
	}
	defer func(buildEnv *environment) {
		err := buildEnv.Close()
		if err != nil {
			log.Printf("[ERROR] closing build environment: %v", err)
		}
	}(buildEnv)

	// Handle Windows-specific resource embedding
	if b.OS == "windows" {
		cmd := buildEnv.newGoBuildCommand(ctx, "list", "-m", buildEnv.portalModulePath)
		var buffer bytes.Buffer
		cmd.Stdout = &buffer
		err = buildEnv.runCommand(ctx, cmd)
		if err != nil {
			return err
		}

		version := strings.TrimSpace(strings.TrimPrefix(buffer.String(), buildEnv.portalModulePath))
		// if the core is being replaced (e.g. via --with), `go list -m`
		// appends "=> <replacement>" to the output; drop it so we keep
		// just the actual version for the embedded Windows metadata.
		if i := strings.Index(version, "=>"); i != -1 {
			version = strings.TrimSpace(version[:i])
		}
		err = utils.WindowsResource(version, outputFile, buildEnv.tempFolder)
		if err != nil {
			return err
		}
	}

	if b.SkipBuild {
		log.Printf("[INFO] Skipping build as requested")
		return nil
	}

	// prepare the environment for the go command; for
	// the most part we want it to inherit our current
	// environment, with a few customizations
	env := os.Environ()
	env = setEnv(env, "GOOS="+b.OS)
	env = setEnv(env, "GOARCH="+b.Arch)
	env = setEnv(env, "GOARM="+b.ARM)
	if b.RaceDetector && !b.Compile.Cgo {
		log.Println("[WARNING] Enabling cgo because it is required by the race detector")
		b.Compile.Cgo = true
	}
	env = setEnv(env, fmt.Sprintf("CGO_ENABLED=%s", b.Compile.CgoEnabled()))

	log.Println("[INFO] Building Portal")

	// Ensure module consistency
	tidyCmd := buildEnv.newGoModCommand(ctx, "tidy", "-e")
	if err := buildEnv.runCommand(ctx, tidyCmd); err != nil {
		return err
	}

	// Run go generate
	generateCmd := buildEnv.newGoGenerateCommand(ctx, "./...")
	if err := buildEnv.runCommand(ctx, generateCmd); err != nil {
		return err
	}

	if b.Debug {
		vendorCmd := buildEnv.newGoModCommand(ctx, "vendor")
		if err := buildEnv.runCommand(ctx, vendorCmd); err != nil {
			return err
		}
	}

	if b.ScratchMode {
		return nil
	}

	version, coreCommit, coreBranch := getModuleInfo(ctx, buildEnv, buildEnv.portalModulePath)
	buildInfoFlags := []string{
		fmt.Sprintf("-X %s/build.Version=%s", defaultPortalModulePath, version),
		fmt.Sprintf("-X %s/build.GitCommit=%s", defaultPortalModulePath, coreCommit),
		fmt.Sprintf("-X %s/build.GitBranch=%s", defaultPortalModulePath, coreBranch),
		fmt.Sprintf("-X %s/build.BuildTime=%s", defaultPortalModulePath, time.Now().UTC().Format(time.RFC3339)),
		fmt.Sprintf("-X %s/build.GoVersion=%s", defaultPortalModulePath, runtime.Version()),
		fmt.Sprintf("-X %s/build.Platform=%s", defaultPortalModulePath, b.OS),
		fmt.Sprintf("-X %s/build.Architecture=%s", defaultPortalModulePath, b.Arch),
		// Prevent panic when two packages register the same proto file name
		// (e.g. go-libp2p-pubsub and go.etcd.io/etcd both register "rpc.proto").
		// See https://protobuf.dev/reference/go/faq#namespace-conflict
		"-X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn",
	}

	// Add build info for each plugin using resolved versions from Go
	for _, plugin := range b.Plugins {
		pluginVersion, pluginCommit, pluginBranch := getModuleInfo(ctx, buildEnv, plugin.PackagePath)
		buildInfoFlags = append(buildInfoFlags,
			fmt.Sprintf("-X %s/build.Version=%s", plugin.PackagePath, pluginVersion),
			fmt.Sprintf("-X %s/build.GitCommit=%s", plugin.PackagePath, pluginCommit),
			fmt.Sprintf("-X %s/build.GitBranch=%s", plugin.PackagePath, pluginBranch),
			fmt.Sprintf("-X %s/build.BuildTime=%s", plugin.PackagePath, time.Now().UTC().Format(time.RFC3339)),
			fmt.Sprintf("-X %s/build.GoVersion=%s", plugin.PackagePath, runtime.Version()),
			fmt.Sprintf("-X %s/build.Platform=%s", plugin.PackagePath, b.OS),
			fmt.Sprintf("-X %s/build.Architecture=%s", plugin.PackagePath, b.Arch),
		)
	}

	// Start building the compile command
	cmd := buildEnv.newGoBuildCommand(ctx, "build", "-o", absOutputFile)

	if b.Debug {
		// Debug mode: include source info for debugger
		cmd.Args = append(cmd.Args,
			"-gcflags", "all=-N -l",
			"-ldflags", strings.Join(buildInfoFlags, " "),
		)
	} else {
		if buildEnv.buildFlags == "" {
			// No custom flags: strip debug symbols and add build info
			cmd.Args = append(cmd.Args,
				"-ldflags", fmt.Sprintf("-w -s %s", strings.Join(buildInfoFlags, " ")),
				"-trimpath",
				"-tags", "nobadger",
			)
		} else {
			// Custom flags: only add build info
			cmd.Args = append(cmd.Args, "-ldflags", strings.Join(buildInfoFlags, " "))
		}
	}

	if b.RaceDetector {
		cmd.Args = append(cmd.Args, "-race")
	}

	// Extra build flags are always applied, on top of both the
	// default flags and any custom XPORTAL_GO_BUILD_FLAGS.
	if buildEnv.buildFlagsExtra != "" {
		extraFlags, err := shlex.Split(buildEnv.buildFlagsExtra)
		if err != nil {
			return fmt.Errorf("parsing XPORTAL_GO_BUILD_FLAGS_EXTRA: %w", err)
		}
		cmd.Args = append(cmd.Args, extraFlags...)
	}

	cmd.Env = env
	err = buildEnv.runCommand(ctx, cmd)
	if err != nil {
		return err
	}

	log.Printf("[INFO] Build complete: %s", outputFile)
	return nil
}

// setEnv sets an environment variable-value pair in
// env, overriding an existing variable if it already
// exists. The env slice is one such as is returned
// by os.Environ(), and set must also have the form
// of key=value.
func setEnv(env []string, set string) []string {
	parts := strings.SplitN(set, "=", 2)
	key := parts[0]
	for i := 0; i < len(env); i++ {
		if strings.HasPrefix(env[i], key+"=") {
			env[i] = set
			return env
		}
	}
	return append(env, set)
}

// Dependency pairs a Go module path with a version.
type Dependency struct {
	// The name (import path) of the Go package. If at a version > 1,
	// it should contain semantic import version (i.e. "/v2").
	// Used with `go get`.
	PackagePath string `json:"module_path,omitempty"`

	// The version of the Go module, as used with `go get`.
	Version string `json:"version,omitempty"`
}

func (d Dependency) String() string {
	if d.Version != "" {
		return d.PackagePath + "@" + d.Version
	}
	return d.PackagePath
}

// ReplacementPath represents an old or new path component
// within a Go module replacement directive.
type ReplacementPath string

// Param reformats a go.mod replace directive to be
// compatible with the `go mod edit` command.
func (r ReplacementPath) Param() string {
	return strings.Replace(string(r), " ", "@", 1)
}

func (r ReplacementPath) String() string { return string(r) }

// Replace represents a Go module replacement.
type Replace struct {
	// The import path of the module being replaced.
	Old ReplacementPath `json:"old,omitempty"`

	// The path to the replacement module.
	New ReplacementPath `json:"new,omitempty"`
}

// NewReplace creates a new instance of Replace provided old and
// new Go module paths
func NewReplace(old, new string) Replace {
	return Replace{
		Old: ReplacementPath(old),
		New: ReplacementPath(new),
	}
}

// newTempFolder creates a new folder in a temporary location.
// It is the caller's responsibility to remove the folder when finished.
func newTempFolder() (string, error) {
	var parentDir string
	if runtime.GOOS == "darwin" {
		// After upgrading to macOS High Sierra, Port builds mysteriously
		// started missing the embedded version information that -ldflags
		// was supposed to produce. But it only happened on macOS after
		// upgrading to High Sierra, and it didn't happen with the usual
		// `go run build.go` -- only when using a buildenv. Bug in git?
		// Nope. Not a bug in Go 1.10 either. Turns out it's a breaking
		// change in macOS High Sierra. When the GOPATH of the buildenv
		// was set to some other folder, like in the $HOME dir, it worked
		// fine. Only within $TMPDIR it broke. The $TMPDIR folder is inside
		// /var, which is symlinked to /private/var, which is mounted
		// with noexec. I don't understand why, but evidently that
		// makes -ldflags of `go build` not work. Bizarre.
		// The solution, I guess, is to just use our own "temp" dir
		// outside of /var. Sigh... as long as it still gets cleaned up,
		// I guess it doesn't matter too much.
		// See: https://github.com/caddyserver/caddy/issues/2036
		// and https://twitter.com/mholt6/status/978345803365273600 (thread)
		// (using an absolute path prevents problems later when removing this
		// folder if the CWD changes)
		var err error
		parentDir, err = filepath.Abs(".")
		if err != nil {
			return "", err
		}
	}
	ts := time.Now().Format(yearMonthDayHourMin)
	return os.MkdirTemp(parentDir, fmt.Sprintf("buildenv_%s.", ts))
}

// versionedModulePath helps enforce Go Module's Semantic Import Versioning (SIV) by
// returning the form of modulePath with the major component of moduleVersion added,
// if > 1. For example, inputs of "foo" and "v1.0.0" will return "foo", but inputs
// of "foo" and "v2.0.0" will return "foo/v2", for use in Go imports and go commands.
// Inputs that conflict, like "foo/v2" and "v3.1.0" are an error. This function
// returns the input if the moduleVersion is not a valid semantic version string.
// If moduleVersion is empty string, the input modulePath is returned without error.
func versionedModulePath(modulePath, moduleVersion string) (string, error) {
	if moduleVersion == "" {
		return modulePath, nil
	}
	ver, err := semver.StrictNewVersion(strings.TrimPrefix(moduleVersion, "v"))
	if err != nil {
		// only return the error if we know they were trying to use a semantic version
		// (could have been a commit SHA or something)
		if strings.HasPrefix(moduleVersion, "v") {
			return "", fmt.Errorf("%s: %v", moduleVersion, err)
		}
		return modulePath, nil
	}
	major := ver.Major()

	// see if the module path has a major version at the end (SIV)
	matches := moduleVersionRegexp.FindStringSubmatch(modulePath)
	if len(matches) == 2 {
		modPathVer, err := strconv.Atoi(matches[1])
		if err != nil {
			return "", fmt.Errorf("this error should be impossible, but module path %s has bad version: %v", modulePath, err)
		}
		if modPathVer != int(major) {
			return "", fmt.Errorf("versioned module path (%s) and requested module major version (%d) diverge", modulePath, major)
		}
	} else if major > 1 {
		modulePath += fmt.Sprintf("/v%d", major)
	}

	return path.Clean(modulePath), nil
}

var moduleVersionRegexp = regexp.MustCompile(`.+/v(\d+)$`)

const (
	// yearMonthDayHourMin is the date format
	// used for temporary folder paths.
	yearMonthDayHourMin = "2006-01-02-1504"

	defaultPortalModulePath = "go.lumeweb.com/portal"
)
