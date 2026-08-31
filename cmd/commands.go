package xportalcmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"go.lumeweb.com/xportal"
	"go.lumeweb.com/xportal/internal/utils"
)

func init() {
	buildCommand.Flags().StringArray("with", []string{}, "portal modules package path to include in the build")
	buildCommand.Flags().String("output", "", "change the output file name")
	buildCommand.Flags().StringArray("replace", []string{}, "like --with but for Go modules")
}

var buildCommand = &cobra.Command{
	Use: `build [<portal_version>]
    [--output <file>]
    [--with <module[@version][=replacement]>...]
    [--replace <module[@version]=replacement>...]`,
	Long: `
<portal_version> is the core Portal version to build; defaults to PORTAL_VERSION env variable or latest.
This can be the keyword latest, which will use the latest stable tag, or any git ref such as:

A tag like v1.0.0
A branch like master
A commit like a58f240d3ecbb59285303746406cab50217f8d24

Flags: 
 --output changes the output file.
 --with can be used multiple times to add plugins by specifying the Go module name and optionally its version, similar to go get. Module name is required, but specific version and/or local replacement are optional.
 --replace is like --with, but does not add a blank import to the code; it only writes a replace directive to go.mod, which is useful when developing on Portal's dependencies (ones that are not Portal modules). Try this if you got an error when using --with, like cannot find module providing package.
`,
	Short: "Compile custom portal binaries",
	Args:  cobra.MinimumNArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBuild(cmd, args, false)
	},
}

var scratchCommand = &cobra.Command{
	Use:   "scratch <scratch_path> [<portal_version>]",
	Short: "Build Portal from scratch with plugins",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBuild(cmd, args, true)
	},
}

var versionCommand = &cobra.Command{
	Use:   "version",
	Short: "Print the version of xportal",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(xportalVersion())
	},
}

func runBuild(cmd *cobra.Command, args []string, scratchMode bool) error {
	var argPortalVersion, output string
	var scratchPath string

	if scratchMode {
		if len(args) < 1 {
			return fmt.Errorf("scratch path is required for scratch mode")
		}
		scratchPath = args[0]
		args = args[1:]
	}

	if len(args) > 0 {
		argPortalVersion = args[0]
	}

	plugins, replacements, err := parsePluginsAndReplacements(cmd)
	if err != nil {
		return err
	}

	output, err = cmd.Flags().GetString("output")
	if err != nil {
		return fmt.Errorf("unable to parse --output arguments: %s", err.Error())
	}

	if argPortalVersion != "" {
		portalVersion = argPortalVersion
	}

	if output == "" && !scratchMode {
		output = getPortalOutputFile()
	}

	builder := createBuilder(portalVersion, plugins, replacements, scratchMode, scratchPath)
	err = builder.Build(cmd.Context(), output)
	if err != nil {
		log.Fatalf("[FATAL] %v", err)
	}

	if builder.SkipBuild || scratchMode {
		return nil
	}

	return finalizeBuild(output)
}

func runDev(ctx context.Context, args []string) error {
	binOutput := getPortalOutputFile()

	currentModule, moduleDir, replacements, err := getCurrentModuleInfo()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("unable to determine current directory: %v", err)
	}
	importPath := normalizeImportPath(currentModule, cwd, moduleDir)

	replacements = append(replacements, defaultReplacements...)

	builder := createBuilder(portalVersion, []xportal.Dependency{{PackagePath: importPath}}, replacements, false, "")
	err = builder.Build(ctx, binOutput)
	if err != nil {
		return err
	}

	err = setcapIfRequested(binOutput)
	if err != nil {
		return err
	}

	return runPortal(binOutput, args)
}

func parsePluginsAndReplacements(cmd *cobra.Command) ([]xportal.Dependency, []xportal.Replace, error) {
	var plugins []xportal.Dependency
	var replacements []xportal.Replace

	withArgs, err := cmd.Flags().GetStringArray("with")
	if err != nil {
		return nil, nil, fmt.Errorf("unable to parse --with arguments: %s", err.Error())
	}

	replaceArgs, err := cmd.Flags().GetStringArray("replace")
	if err != nil {
		return nil, nil, fmt.Errorf("unable to parse --replace arguments: %s", err.Error())
	}

	for _, withArg := range withArgs {
		mod, ver, repl, err := splitWith(withArg)
		if err != nil {
			return nil, nil, err
		}
		mod = strings.TrimSuffix(mod, "/")
		plugins = append(plugins, xportal.Dependency{
			PackagePath: mod,
			Version:     ver,
		})
		handleReplace(withArg, mod, ver, repl, &replacements)
	}

	for _, withArg := range replaceArgs {
		mod, ver, repl, err := splitWith(withArg)
		if err != nil {
			return nil, nil, err
		}
		handleReplace(withArg, mod, ver, repl, &replacements)
	}

	return plugins, replacements, nil
}

func createBuilder(portalVersion string, plugins []xportal.Dependency, replacements []xportal.Replace, scratchMode bool, scratchPath string) xportal.Builder {
	builder := xportal.Builder{
		Compile: xportal.Compile{
			Cgo: true,
		},
		PortalVersion:   portalVersion,
		Plugins:         plugins,
		Replacements:    append(replacements, defaultReplacements...),
		RaceDetector:    raceDetector,
		SkipBuild:       skipBuild,
		SkipCleanup:     skipCleanup || scratchMode,
		Debug:           buildDebugOutput,
		BuildFlags:      buildFlags,
		BuildFlagsExtra: buildFlagsExtra,
		ModFlags:        modFlags,
		ScratchMode:     scratchMode,
		ScratchPath:     scratchPath,
	}

	if disableCgo {
		builder.Compile.Cgo = false
	}

	return builder
}

func finalizeBuild(output string) error {
	err := setcapIfRequested(output)
	if err != nil {
		return err
	}

	if runtime.GOOS == utils.GetGOOS() && runtime.GOARCH == utils.GetGOARCH() {
		if !filepath.IsAbs(output) {
			output = "." + string(filepath.Separator) + output
		}
		fmt.Println()
		fmt.Printf("%s version\n", output)
		execCmd := exec.Command(output, "version")
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr
		err = execCmd.Run()
		if err != nil {
			log.Fatalf("[FATAL] %v", err)
		}
	}

	return nil
}

func getCurrentModuleInfo() (string, string, []xportal.Replace, error) {
	execCmd := exec.Command(utils.GetGo(), "list", "-mod=readonly", "-m", "-json", "all")
	execCmd.Stderr = os.Stderr
	out, err := execCmd.Output()
	if err != nil {
		return "", "", nil, fmt.Errorf("exec %v: %v: %s", execCmd.Args, err, string(out))
	}
	return parseGoListJson(out)
}

func runPortal(binOutput string, args []string) error {
	log.Printf("[INFO] Running %v\n\n", append([]string{binOutput}, args...))

	execCmd := exec.Command(binOutput, args...)
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	err := execCmd.Start()
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

	return execCmd.Wait()
}

func handleReplace(orig, mod, ver, repl string, replacements *[]xportal.Replace) {
	if repl != "" {
		if strings.HasPrefix(repl, ".") {
			var err error
			repl, err = filepath.Abs(repl)
			if err != nil {
				log.Fatalf("[FATAL] %v", err)
			}
			log.Printf("[INFO] Resolved relative replacement %s to %s", orig, repl)
		}
		*replacements = append(*replacements, xportal.NewReplace(xportal.Dependency{PackagePath: mod, Version: ver}.String(), repl))
	}
}
