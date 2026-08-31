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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
		tplCtx.Plugins = append(tplCtx.Plugins, pluginInfo{
			PackagePath: p.PackagePath,
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
		buildFlagsExtra:  b.BuildFlagsExtra,
		modFlags:         b.ModFlags,
		replacements:     b.Replacements,
		exclusions:       b.Exclusions,
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

	// specify module exclusions before pinning versions
	if len(b.Exclusions) > 0 {
		cmd := env.newGoModCommand(ctx, "edit")
		for _, e := range b.Exclusions {
			if e.Module == "" || e.Version == "" {
				return nil, fmt.Errorf("exclude directive requires both module path and version")
			}
			log.Printf("[INFO] Exclude %s", e.String())
			cmd.Args = append(cmd.Args, "-exclude", e.String())
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
		// foo/a/plugin is within module foo/a. Requiring the trailing
		// path separator avoids matching lexical submodules, e.g. a
		// replacement for "foo" must not swallow the distinct module
		// "foo-bar".
		for repl := range replaced {
			if strings.HasPrefix(p.PackagePath, repl+"/") {
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
	buildFlagsExtra  string
	modFlags         string
	replacements     []Replace
	exclusions       []Exclude
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

// newGoBuildCommand creates a new *exec.Cmd which assumes the first element
// in `args` is one of: build, clean, get, install, list, run, or test.
// The generated command will also have the value of `XPORTAL_GO_BUILD_FLAGS`
// inserted right after the go subcommand, so the flags are parsed before any
// positional arguments (e.g. `go build -o out` or `go list -m <module>`).
func (env environment) newGoBuildCommand(ctx context.Context, args ...string) *exec.Cmd {
	subcommand := ""
	if len(args) > 0 {
		subcommand = args[0]
	}
	cmd := env.newCommand(ctx, utils.GetGo(), subcommand)
	cmd = parseAndAppendFlags(cmd, env.buildFlags)
	cmd.Args = append(cmd.Args, args[1:]...)
	return cmd
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

// pluginModuleDir resolves the absolute directory of the given module within
// the build environment's module graph (e.g. a plugin dependency). It uses
// `go list -m -f '{{.Dir}}'` so the path is resolved from the resolved module
// versions, including any replacements.
func (env environment) pluginModuleDir(ctx context.Context, modulePath string) (string, error) {
	cmd := env.newGoBuildCommand(ctx, "list", "-m", "-f", "{{.Dir}}", modulePath)
	var buffer bytes.Buffer
	cmd.Stdout = &buffer
	if err := env.runCommand(ctx, cmd); err != nil {
		return "", err
	}
	dir := strings.TrimSpace(buffer.String())
	if dir == "" {
		return "", fmt.Errorf("could not resolve module directory for %s", modulePath)
	}
	return dir, nil
}

// makeWritable recursively adds the user-write bit to every entry under dir.
// Go module cache directories are read-only by default, but plugins with
// generators write generated assets into their own module directory, so it
// must be writable first.
func makeWritable(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode().Perm()|0o200)
	})
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
    _ "time/tzdata"

    portalcmd "{{.PortalPlugin}}/cmd/portal_embed"
    _ "{{.PortalPlugin}}/service"

    // plug in Portal plugins
    {{- range .Plugins}}
    _ "{{.PackagePath}}"
    {{- end}}
)

func main() {
    portalcmd.Main()
}`

func getModuleInfo(ctx context.Context, buildEnv *environment, modulePath string) (version, commit, branch string) {
	// First try vendor directory
	vendorMetaFile := filepath.Join(buildEnv.tempFolder, "vendor", "modules.txt")
	if data, err := os.ReadFile(vendorMetaFile); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			// modules.txt format: path version [=> replacement]
			parts := strings.Fields(line)
			if len(parts) >= 2 && parts[0] == modulePath {
				version = parts[1]
				break
			}
		}
		fmt.Printf("[DEBUG] Found version %s in vendor/modules.txt for %s\n", version, modulePath)
	}

	// Then try module cache
	cmd := buildEnv.newGoBuildCommand(ctx, "list", "-m", "-json", modulePath)
	var buffer bytes.Buffer
	cmd.Stdout = &buffer
	err := buildEnv.runCommand(ctx, cmd)
	if err != nil {
		fmt.Printf("[ERROR] Failed to get module info for %s: %v\n", modulePath, err)
		return version, "unknown", "unknown" // Keep version if we found it in vendor
	}

	var moduleInfo struct {
		Path    string
		Version string
	}
	if err := json.Unmarshal(buffer.Bytes(), &moduleInfo); err != nil {
		return version, "unknown", "unknown" // Keep version if we found it in vendor
	}

	if version == "" {
		version = moduleInfo.Version
	}

	// Try to get git info from module cache
	cmd = buildEnv.newGoBuildCommand(ctx, "env", "GOMODCACHE")
	buffer.Reset()
	cmd.Stdout = &buffer
	err = buildEnv.runCommand(ctx, cmd)
	if err == nil {
		modcache := strings.TrimSpace(buffer.String())
		infoPath := filepath.Join(modcache, "cache", "download", strings.ReplaceAll(modulePath, "/", string(filepath.Separator)), "@v", version+".info")

		data, err := os.ReadFile(infoPath)
		if err == nil {
			var info struct {
				Version string
				Time    string
				Origin  struct {
					VCS  string
					URL  string
					Hash string
					Ref  string
				}
			}
			if err := json.Unmarshal(data, &info); err == nil {
				commit = info.Origin.Hash
				// Try to extract branch/tag from Ref
				if info.Origin.Ref != "" {
					if strings.HasPrefix(info.Origin.Ref, "refs/heads/") {
						// Only set branch if it's actually a branch
						branch = strings.TrimPrefix(info.Origin.Ref, "refs/heads/")
					}
					// Ignore refs/tags/ - let version field handle that info
				}
			}
		}
	}

	// If vendored, also check vendor/modules.txt for any additional metadata
	if commit == "" && vendorMetaFile != "" {
		vendorModuleFile := filepath.Join(buildEnv.tempFolder, "vendor", strings.ReplaceAll(modulePath, "/", string(filepath.Separator)), "module.info")
		if data, err := os.ReadFile(vendorModuleFile); err == nil {
			var info struct {
				Hash string
				Ref  string
			}
			if err := json.Unmarshal(data, &info); err == nil {
				if info.Hash != "" {
					commit = info.Hash
				}
				if info.Ref != "" {
					branch = info.Ref
				}
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

	fmt.Printf("[INFO] Module %s: version=%s commit=%s branch=%s\n", modulePath, version, commit, branch)
	return version, commit, branch
}
