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
	"go.lumeweb.com/xportal/internal/utils"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/google/shlex"
	_ "go.lumeweb.com/xportal/internal/utils"
)

func (b Builder) newEnvironment(ctx context.Context) (*environment, error) {
	portalModulePath := defaultPortalModulePath
	portalModulePath, err := versionedModulePath(portalModulePath, b.PortalVersion)
	if err != nil {
		return nil, err
	}

	// clean up any SIV-incompatible module paths real quick
	for i, p := range b.Plugins {
		b.Plugins[i].PackagePath, err = versionedModulePath(p.PackagePath, p.Version)
		if err != nil {
			return nil, err
		}
	}

	// create the context for the main module template
	tplCtx := goModTemplateContext{
		PortalPlugin: portalModulePath,
	}

	// Convert plugin paths to pluginInfo structs
	for _, p := range b.Plugins {
		// Create a sanitized package variable name
		packageVar := sanitizePackagePath(p.PackagePath)

		tplCtx.Plugins = append(tplCtx.Plugins, pluginInfo{
			PackagePath: p.PackagePath,
			PackageVar:  packageVar,
		})
	}

	// evaluate the template for the main module
	var buf bytes.Buffer
	tpl, err := template.New("main").Parse(mainModuleTemplate)
	if err != nil {
		return nil, err
	}
	err = tpl.Execute(&buf, tplCtx)
	if err != nil {
		return nil, err
	}

	var tempFolder string

	if b.ScratchMode {
		tempFolder = b.ScratchPath
		err = os.MkdirAll(tempFolder, 0755)
		if err != nil {
			return nil, err
		}
	} else {
		// create the folder in which the build environment will operate
		tempFolder, err = newTempFolder()
		if err != nil {
			return nil, err
		}
		defer func() {
			if err != nil {
				err2 := os.RemoveAll(tempFolder)
				if err2 != nil {
					err = fmt.Errorf("%w; additionally, cleaning up folder: %v", err, err2)
				}
			}
		}()
		log.Printf("[INFO] Temporary folder: %s", tempFolder)
	}

	// write the main module file to temporary folder
	mainPath := filepath.Join(tempFolder, "main.go")
	log.Printf("[INFO] Writing main module: %s\n%s", mainPath, buf.Bytes())
	err = os.WriteFile(mainPath, buf.Bytes(), 0o644)
	if err != nil {
		return nil, err
	}

	env := &environment{
		portalVersion:    b.PortalVersion,
		plugins:          b.Plugins,
		portalModulePath: portalModulePath,
		tempFolder:       tempFolder,
		timeoutGoGet:     b.TimeoutGet,
		skipCleanup:      b.SkipCleanup || b.ScratchMode,
		buildFlags:       b.BuildFlags,
		modFlags:         b.ModFlags,
		replacements:     b.Replacements,
	}

	// initialize the go module
	log.Println("[INFO] Initializing Go module")
	cmd := env.newGoModCommand(ctx, "init")
	cmd.Args = append(cmd.Args, "portal")
	err = env.runCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	// specify module replacements before pinning versions
	replaced := make(map[string]string)
	for _, r := range b.Replacements {
		log.Printf("[INFO] Replace %s => %s", r.Old.String(), r.New.String())
		replaced[r.Old.String()] = r.New.String()
	}
	if len(replaced) > 0 {
		cmd := env.newGoModCommand(ctx, "edit")
		for o, n := range replaced {
			cmd.Args = append(cmd.Args, "-replace", fmt.Sprintf("%s=%s", o, n))
		}
		err := env.runCommand(ctx, cmd)
		if err != nil {
			return nil, err
		}
	}

	// check for early abort
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// The timeout for the `go get` command may be different than `go build`,
	// so create a new context with the timeout for `go get`
	if env.timeoutGoGet > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), env.timeoutGoGet)
		defer cancel()
	}

	// pin versions by populating go.mod, first for the Portal itself and then plugins
	log.Println("[INFO] Pinning versions")
	err = env.execGoGet(ctx, portalModulePath, env.portalVersion, "", "")
	if err != nil {
		return nil, err
	}
nextPlugin:
	for _, p := range b.Plugins {
		// if module is locally available, do not "go get" it;
		// also note that we iterate and check prefixes, because
		// a plugin package may be a subfolder of a module, i.e.
		// foo/a/plugin is within module foo/a.
		for repl := range replaced {
			if strings.HasPrefix(p.PackagePath, repl) {
				continue nextPlugin
			}
		}
		// also pass the Portal version to prevent it from being upgraded
		err = env.execGoGet(ctx, p.PackagePath, p.Version, portalModulePath, env.portalVersion)
		if err != nil {
			return nil, err
		}
		// check for early abort
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}

	// doing an empty "go get -d" can potentially resolve some
	// ambiguities introduced by one of the plugins;
	// see https://github.com/caddyserver/xcaddy/pull/92
	err = env.execGoGet(ctx, "", "", "", "")
	if err != nil {
		return nil, err
	}

	log.Println("[INFO] Build environment ready")
	return env, nil
}

type environment struct {
	portalVersion    string
	plugins          []Dependency
	portalModulePath string
	tempFolder       string
	timeoutGoGet     time.Duration
	skipCleanup      bool
	buildFlags       string
	modFlags         string
	replacements     []Replace
}

// Close cleans up the build environment, including deleting
// the temporary folder from the disk.
func (env environment) Close() error {
	if env.skipCleanup {
		log.Printf("[INFO] Skipping cleanup as requested; leaving folder intact: %s", env.tempFolder)
		return nil
	}
	log.Printf("[INFO] Cleaning up temporary folder: %s", env.tempFolder)
	return os.RemoveAll(env.tempFolder)
}

func (env environment) newCommand(ctx context.Context, command string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = env.tempFolder
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// newGoBuildCommand creates a new *exec.Cmd which assumes the first element in `args` is one of: build, clean, get, install, list, run, or test. The
// created command will also have the value of `XPORTAL_GO_BUILD_FLAGS` appended to its arguments, if set.
func (env environment) newGoBuildCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := env.newCommand(ctx, utils.GetGo(), args...)
	return parseAndAppendFlags(cmd, env.buildFlags)
}

// newGoModCommand creates a new *exec.Cmd which assumes `args` are the args for `go mod` command. The
// created command will also have the value of `XPORTAL_GO_MOD_FLAGS` appended to its arguments, if set.
func (env environment) newGoModCommand(ctx context.Context, args ...string) *exec.Cmd {
	args = append([]string{"mod"}, args...)
	cmd := env.newCommand(ctx, utils.GetGo(), args...)
	return parseAndAppendFlags(cmd, env.modFlags)
}

// newGoGenerateCommand creates a new *exec.Cmd which assumes `args` are the args for `go generate` command. The
// created command will also have the value of `XPORTAL_GO_MOD_FLAGS` appended to its arguments, if set.
func (env environment) newGoGenerateCommand(ctx context.Context, args ...string) *exec.Cmd {
	args = append([]string{"generate"}, args...)
	cmd := env.newCommand(ctx, utils.GetGo(), args...)
	return parseAndAppendFlags(cmd, env.modFlags)
}

func parseAndAppendFlags(cmd *exec.Cmd, flags string) *exec.Cmd {
	if strings.TrimSpace(flags) == "" {
		return cmd
	}

	fs, err := shlex.Split(flags)
	if err != nil {
		log.Printf("[ERROR] Splitting arguments failed: %s", flags)
		return cmd
	}
	cmd.Args = append(cmd.Args, fs...)

	return cmd
}

func (env environment) runCommand(ctx context.Context, cmd *exec.Cmd) error {
	deadline, ok := ctx.Deadline()
	var timeout time.Duration
	// context doesn't necessarily have a deadline
	if ok {
		timeout = time.Until(deadline)
	}
	log.Printf("[INFO] exec (timeout=%s): %+v ", timeout, cmd)

	// start the command; if it fails to start, report error immediately
	err := cmd.Start()
	if err != nil {
		return err
	}

	// wait for the command in a goroutine; the reason for this is
	// very subtle: if, in our select, we do `case cmdErr := <-cmd.Wait()`,
	// then that case would be chosen immediately, because cmd.Wait() is
	// immediately available (even though it blocks for potentially a long
	// time, it can be evaluated immediately). So we have to remove that
	// evaluation from the `case` statement.
	cmdErrChan := make(chan error)
	go func() {
		cmdErrChan <- cmd.Wait()
	}()

	// unblock either when the command finishes, or when the done
	// channel is closed -- whichever comes first
	select {
	case cmdErr := <-cmdErrChan:
		// process ended; report any error immediately
		return cmdErr
	case <-ctx.Done():
		// context was canceled, either due to timeout or
		// maybe a signal from higher up canceled the parent
		// context; presumably, the OS also sent the signal
		// to the child process, so wait for it to die
		select {
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
		case <-cmdErrChan:
		}
		return ctx.Err()
	}
}

// execGoGet runs "go get -d -v" with the given module/version as an argument.
// Also allows passing in a second module/version pair, meant to be the main
// Portal module/version we're building against; this will prevent the
// plugin module from causing the Portal version to upgrade, if the plugin
// version requires a newer version of the Portal.
// See https://github.com/caddyserver/xcaddy/issues/54
func (env environment) execGoGet(ctx context.Context, modulePath, moduleVersion, portalModulePath, portalVersion string) error {
	mod := modulePath
	if moduleVersion != "" {
		mod += "@" + moduleVersion
	}
	portal := portalModulePath
	if portalVersion != "" {
		portal += "@" + portalVersion
	}

	cmd := env.newGoBuildCommand(ctx, "get", "-v")
	// using an empty string as an additional argument to "go get"
	// breaks the command since it treats the empty string as a
	// distinct argument, so we're using an if statement to avoid it.
	if portal != "" {
		cmd.Args = append(cmd.Args, mod, portal)
	} else {
		cmd.Args = append(cmd.Args, mod)
	}

	return env.runCommand(ctx, cmd)
}

type goModTemplateContext struct {
	PortalPlugin string
	Plugins      []pluginInfo
}

type pluginInfo struct {
	PackagePath string
	PackageVar  string
}

const mainModuleTemplate = `package main

import (
    portalcmd "{{.PortalPlugin}}/cmd"
    _ "{{.PortalPlugin}}/service"
    portalBuild "{{.PortalPlugin}}/build"

    // plug in Portal plugins here and their build info
    {{- range .Plugins}}
    _ "{{.}}"
    _ "{{.}}/build"
    {{- end}}
)

// Ensure build info is registered
var (
    _ = portalBuild.Default
    {{- range .Plugins}}
    _ = {{.PackageVar}}build.Default
    {{- end}}
)

func main() {
    portalcmd.Main()
}
`

func sanitizePackagePath(path string) string {
	// Remove common prefixes like github.com/
	parts := strings.Split(path, "/")
	if len(parts) > 2 {
		parts = parts[2:] // Skip the first two parts (e.g., "github.com")
	}

	// Join remaining parts and sanitize
	name := strings.Join(parts, "_")
	name = strings.Map(func(r rune) rune {
		if r == '-' || r == '.' {
			return '_'
		}
		return r
	}, name)

	return name + "_"
}

func getModuleInfo(ctx context.Context, buildEnv *environment, modulePath string) (version, commit, branch string) {
	// Get the full module info including version
	cmd := buildEnv.newGoBuildCommand(ctx, "list", "-m", "-f", "{{.Version}}", modulePath)
	var buffer bytes.Buffer
	cmd.Stdout = &buffer
	err := buildEnv.runCommand(ctx, cmd)
	if err != nil {
		return "unknown", "unknown", "unknown"
	}

	version = strings.TrimSpace(buffer.String())

	// Parse version string:
	// v0.0.0-20240305120012-abcdef123456 (pseudo-version with commit)
	// v0.0.0-20240305120012-branch.name-abcdef123456 (pseudo-version with branch)
	// v1.2.3 (release version)
	if strings.Contains(version, "-") {
		parts := strings.Split(version, "-")
		if len(parts) >= 3 {
			commit = parts[len(parts)-1]
			// If we have 4 parts, the branch name is embedded
			if len(parts) >= 4 {
				// Reconstruct branch name which might contain hyphens
				branch = strings.Join(parts[2:len(parts)-1], "-")
			}
		}
	}

	// For replaced modules, try to get branch from git if it's a local replacement
	if branch == "" {
		for _, repl := range buildEnv.replacements {
			if strings.HasPrefix(repl.Old.String(), modulePath) {
				if strings.HasPrefix(repl.New.String(), "file://") || strings.HasPrefix(repl.New.String(), ".") || strings.HasPrefix(repl.New.String(), "/") {
					gitPath := strings.TrimPrefix(repl.New.String(), "file://")
					cmd := exec.Command("git", "-C", gitPath, "rev-parse", "--abbrev-ref", "HEAD")
					out, err := cmd.Output()
					if err == nil {
						branch = strings.TrimSpace(string(out))
					}
				}
				break
			}
		}
	}

	if version == "" {
		version = "unknown"
	}
	if commit == "" {
		commit = "unknown"
	}
	if branch == "" {
		branch = "unknown"
	}

	return version, commit, branch
}
