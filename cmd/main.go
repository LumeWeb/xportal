package xportalcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go.lumeweb.com/xportal"
	"go.lumeweb.com/xportal/internal/utils"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
)

var (
	portalVersion    = os.Getenv("PORTAL_VERSION")
	raceDetector     = os.Getenv("XPORTAL_RACE_DETECTOR") == "1"
	skipBuild        = os.Getenv("XPORTAL_SKIP_BUILD") == "1"
	skipCleanup      = os.Getenv("XPORTAL_SKIP_CLEANUP") == "1" || skipBuild
	buildDebugOutput = os.Getenv("XPORTAL_DEBUG") == "1"
	buildFlags       = os.Getenv("XPORTAL_GO_BUILD_FLAGS")
	modFlags         = os.Getenv("XPORTAL_GO_MOD_FLAGS")
)

func Main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go trapSignals(ctx, cancel)

	if len(os.Args) > 1 && os.Args[1] == "build" {
		if err := runBuild(ctx, os.Args[2:]); err != nil {
			log.Fatalf("[ERROR] %v", err)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(xportalVersion())
		return
	}

	if err := runDev(ctx, os.Args[1:]); err != nil {
		log.Fatalf("[ERROR] %v", err)
	}
}

func runBuild(ctx context.Context, args []string) error {
	// parse the command line args... rather primitively
	var argPortalVersion, output string
	var plugins []xportal.Dependency
	var replacements []xportal.Replace
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--with", "--replace":
			arg := args[i]
			if i == len(args)-1 {
				return fmt.Errorf("expected value after %s flag", arg)
			}
			i++
			mod, ver, repl, err := splitWith(args[i])
			if err != nil {
				return err
			}
			mod = strings.TrimSuffix(mod, "/") // easy to accidentally leave a trailing slash if pasting from a URL, but is invalid for Go modules
			if arg == "--with" {
				plugins = append(plugins, xportal.Dependency{
					PackagePath: mod,
					Version:     ver,
				})
			}

			if arg != "--with" && repl == "" {
				return fmt.Errorf("expected value after --replace flag")
			}
			if repl != "" {
				// adjust relative replacements in current working directory since our temporary module is in a different directory
				if strings.HasPrefix(repl, ".") {
					repl, err = filepath.Abs(repl)
					if err != nil {
						log.Fatalf("[FATAL] %v", err)
					}
					log.Printf("[INFO] Resolved relative replacement %s to %s", args[i], repl)
				}
				replacements = append(replacements, xportal.NewReplace(xportal.Dependency{PackagePath: mod, Version: ver}.String(), repl))
			}
		case "--output":
			if i == len(args)-1 {
				return fmt.Errorf("expected value after --output flag")
			}
			i++
			output = args[i]
		default:
			if argPortalVersion != "" {
				return fmt.Errorf("missing flag; portal version already set at %s", argPortalVersion)
			}
			argPortalVersion = args[i]
		}
	}

	replacements = append(replacements, defaultReplacements...)

	// prefer portal version from command line argument over env var
	if argPortalVersion != "" {
		portalVersion = argPortalVersion
	}

	// ensure an output file is always specified
	if output == "" {
		output = getPortalOutputFile()
	}

	// perform the build
	builder := xportal.Builder{
		Compile: xportal.Compile{
			Cgo: true,
		},
		PortalVersion: portalVersion,
		Plugins:       plugins,
		Replacements:  replacements,
		RaceDetector:  raceDetector,
		SkipBuild:     skipBuild,
		SkipCleanup:   skipCleanup,
		Debug:         buildDebugOutput,
		BuildFlags:    buildFlags,
		ModFlags:      modFlags,
	}
	err := builder.Build(ctx, output)
	if err != nil {
		log.Fatalf("[FATAL] %v", err)
	}

	// done if we're skipping the build
	if builder.SkipBuild {
		return nil
	}

	// if requested, run setcap to allow binding to low ports
	err = setcapIfRequested(output)
	if err != nil {
		return err
	}

	return nil
}

func getPortalOutputFile() string {
	f := "." + string(filepath.Separator) + "portal"
	// compiling for Windows or compiling on windows without setting GOOS, use .exe extension
	if utils.GetGOOS() == "windows" {
		f += ".exe"
	}
	return f
}

func runDev(ctx context.Context, args []string) error {
	binOutput := getPortalOutputFile()

	// get current/main module name and the root directory of the main module
	//
	// make sure the module being developed is replaced
	// so that the local copy is used
	//
	// replace directives only apply to the top-level/main go.mod,
	// and since this tool is a carry-through for the user's actual
	// go.mod, we need to transfer their replace directives through
	// to the one we're making
	cmd := exec.Command(utils.GetGo(), "list", "-mod=readonly", "-m", "-json", "all")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("exec %v: %v: %s", cmd.Args, err, string(out))
	}
	currentModule, moduleDir, replacements, err := parseGoListJson(out)
	if err != nil {
		return fmt.Errorf("json parse error: %v", err)
	}

	// reconcile remaining path segments; for example if a module foo/a
	// is rooted at directory path /home/foo/a, but the current directory
	// is /home/foo/a/b, then the package to import should be foo/a/b
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("unable to determine current directory: %v", err)
	}
	importPath := normalizeImportPath(currentModule, cwd, moduleDir)

	replacements = append(replacements, defaultReplacements...)

	// build portal with this module plugged in
	builder := xportal.Builder{
		Compile: xportal.Compile{
			Cgo: true,
		},
		PortalVersion: portalVersion,
		Plugins: []xportal.Dependency{
			{PackagePath: importPath},
		},
		Replacements: replacements,
		RaceDetector: raceDetector,
		SkipBuild:    skipBuild,
		SkipCleanup:  skipCleanup,
		Debug:        buildDebugOutput,
	}
	err = builder.Build(ctx, binOutput)
	if err != nil {
		return err
	}

	// if requested, run setcap to allow binding to low ports
	err = setcapIfRequested(binOutput)
	if err != nil {
		return err
	}

	log.Printf("[INFO] Running %v\n\n", append([]string{binOutput}, args...))

	cmd = exec.Command(binOutput, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Start()
	if err != nil {
		return err
	}
	defer func() {
		if skipCleanup {
			log.Printf("[INFO] Skipping cleanup as requested; leaving artifact: %s", binOutput)
			return
		}
		err = os.Remove(binOutput)
		if err != nil && !os.IsNotExist(err) {
			log.Printf("[ERROR] Deleting temporary binary %s: %v", binOutput, err)
		}
	}()

	return cmd.Wait()
}

func setcapIfRequested(output string) error {
	if os.Getenv("XPORTAL_SETCAP") != "1" {
		return nil
	}

	args := []string{"setcap", "cap_net_bind_service=+ep", output}

	// check if sudo isn't available, or we were instructed not to use it
	_, sudoNotFound := exec.LookPath("sudo")
	skipSudo := sudoNotFound != nil || os.Getenv("XPORTAL_SUDO") == "0"

	var cmd *exec.Cmd
	if skipSudo {
		cmd = exec.Command(args[0], args[1:]...)
	} else {
		cmd = exec.Command("sudo", args...)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("[INFO] Setting capabilities (requires admin privileges): %v", cmd.Args)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to setcap on the binary: %v", err)
	}

	return nil
}

type module struct {
	Path    string  // module path
	Version string  // module version
	Replace *module // replaced by this module
	Main    bool    // is this the main module?
	Dir     string  // directory holding files for this module, if any
}

func parseGoListJson(out []byte) (currentModule, moduleDir string, replacements []xportal.Replace, err error) {
	var unjoinedReplaces []int

	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var mod module
		if err = decoder.Decode(&mod); err == io.EOF {
			err = nil
			break
		} else if err != nil {
			return
		}

		if mod.Main {
			// Current module is main module, retrieve the main module name and
			// root directory path of the main module
			currentModule = mod.Path
			moduleDir = mod.Dir
			replacements = append(replacements, xportal.NewReplace(currentModule, moduleDir))
			continue
		}

		// Skip if current module is not replacement
		if mod.Replace == nil {
			continue
		}

		// 1. Target is module, version is required in this case
		// 2A. Target is absolute path
		// 2B. Target is relative path, proper handling is required in this case
		dstPath := mod.Replace.Path
		dstVersion := mod.Replace.Version
		var dst string
		if dstVersion != "" {
			dst = dstPath + "@" + dstVersion
		} else if filepath.IsAbs(dstPath) {
			dst = dstPath
		} else {
			if moduleDir != "" {
				dst = filepath.Join(moduleDir, dstPath)
				log.Printf("[INFO] Resolved relative replacement %s to %s", dstPath, dst)
			} else {
				// moduleDir is not parsed yet, defer to later
				dst = dstPath
				unjoinedReplaces = append(unjoinedReplaces, len(replacements))
			}
		}

		replacements = append(replacements, xportal.NewReplace(mod.Path, dst))
	}
	for _, idx := range unjoinedReplaces {
		unresolved := string(replacements[idx].New)
		resolved := filepath.Join(moduleDir, unresolved)
		log.Printf("[INFO] Resolved previously-unjoined relative replacement %s to %s", unresolved, resolved)
		replacements[idx].New = xportal.ReplacementPath(resolved)
	}
	return
}

func normalizeImportPath(currentModule, cwd, moduleDir string) string {
	return path.Join(currentModule, filepath.ToSlash(strings.TrimPrefix(cwd, moduleDir)))
}

func trapSignals(ctx context.Context, cancel context.CancelFunc) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	select {
	case <-sig:
		log.Printf("[INFO] SIGINT: Shutting down")
		cancel()
	case <-ctx.Done():
		return
	}
}

func splitWith(arg string) (module, version, replace string, err error) {
	const versionSplit, replaceSplit = "@", "="

	parts := strings.SplitN(arg, replaceSplit, 2)
	if len(parts) > 1 {
		replace = parts[1]
	}
	module = parts[0]

	// accommodate module paths that have @ in them, but we can only tolerate that if there's also
	// a version, otherwise we don't know if it's a version separator or part of the file path (see #109)
	lastVersionSplit := strings.LastIndex(module, versionSplit)
	if lastVersionSplit < 0 {
		if replaceIdx := strings.Index(module, replaceSplit); replaceIdx >= 0 {
			module, replace = module[:replaceIdx], module[replaceIdx+1:]
		}
	} else {
		module, version = module[:lastVersionSplit], module[lastVersionSplit+1:]
		if replaceIdx := strings.Index(version, replaceSplit); replaceIdx >= 0 {
			version, replace = module[:replaceIdx], module[replaceIdx+1:]
		}
	}

	if module == "" {
		err = fmt.Errorf("module name is required")
	}

	return
}

// xportalVersion returns a detailed version string, if available.
func xportalVersion() string {
	mod := goModule()
	ver := mod.Version
	if mod.Sum != "" {
		ver += " " + mod.Sum
	}
	if mod.Replace != nil {
		ver += " => " + mod.Replace.Path
		if mod.Replace.Version != "" {
			ver += "@" + mod.Replace.Version
		}
		if mod.Replace.Sum != "" {
			ver += " " + mod.Replace.Sum
		}
	}
	return ver
}

func goModule() *debug.Module {
	mod := &debug.Module{}
	mod.Version = "unknown"
	bi, ok := debug.ReadBuildInfo()
	if ok {
		mod.Path = bi.Main.Path
		// The recommended way to build xportal involves
		// creating a separate main module, which
		// TODO: track related Go issue: https://github.com/golang/go/issues/29228
		// once that issue is fixed, we should just be able to use bi.Main... hopefully.
		for _, dep := range bi.Deps {
			if dep.Path == "go.lumeweb.com/xportal" {
				return dep
			}
		}
		return &bi.Main
	}
	return mod
}
