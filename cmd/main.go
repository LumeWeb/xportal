package xportalcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	"go.lumeweb.com/xportal"
)

var (
	portalVersion    = os.Getenv("PORTAL_VERSION")
	raceDetector     = os.Getenv("XPORTAL_RACE_DETECTOR") == "1"
	skipBuild        = os.Getenv("XPORTAL_SKIP_BUILD") == "1"
	skipCleanup      = os.Getenv("XPORTAL_SKIP_CLEANUP") == "1" || skipBuild
	buildDebugOutput = os.Getenv("XPORTAL_DEBUG") == "1"
	buildFlags       = os.Getenv("XPORTAL_GO_BUILD_FLAGS")
	buildFlagsExtra  = os.Getenv("XPORTAL_GO_BUILD_FLAGS_EXTRA")
	modFlags         = os.Getenv("XPORTAL_GO_MOD_FLAGS")
	disableCgo       = os.Getenv("XPORTAL_DISABLE_CGO") == "1"
)

func Main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go trapSignals(ctx, cancel)

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func getPortalOutputFile() string {
	output := "portal"
	if utils.GetGOOS() == "windows" {
		output += ".exe"
	}

	// Clean the path and ensure it starts with current directory
	output = filepath.Clean(output)
	if !filepath.IsAbs(output) {
		output = "." + string(filepath.Separator) + output
	}

	return output
}

func setcapIfRequested(output string) error {
	if os.Getenv("XPORTAL_SETCAP") != "1" {
		return nil
	}

	args := []string{"setcap", "cap_net_bind_service=+ep", output}

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
			currentModule = mod.Path
			moduleDir = mod.Dir
			replacements = append(replacements, xportal.NewReplace(currentModule, moduleDir))
			continue
		}

		if mod.Replace == nil {
			continue
		}

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
		for _, dep := range bi.Deps {
			if dep.Path == "go.lumeweb.com/xportal" {
				return dep
			}
		}
		return &bi.Main
	}
	return mod
}
