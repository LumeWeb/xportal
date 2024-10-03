package xportalcmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use: "xportal <args...>",
	Long: "xportal is a custom Portal builder for advanced users and plugin developers.\n" +
		"The xportal command has two primary uses:\n" +
		"- Compile custom portal binaries\n" +
		"- A replacement for `go run` while developing Portal plugins\n" +
		"xportal accepts any Portal command (except help and version) to pass through to the custom-built Portal.\n" +
		"The command pass-through allows for an iterative development process.\n\n" +
		"Report bugs on https://github.com/LumeWeb/xportal\n",
	Short:        "Portal module development helper",
	SilenceUsage: true,
	Version:      xportalVersion(),
	Args:         cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDev(cmd.Context(), args)
	},
}

const fullDocsFooter = `Full documentation is available at:
https://github.com/LumeWeb/xportal`

func init() {
	rootCmd.SetVersionTemplate("{{.Version}}\n")
	rootCmd.SetHelpTemplate(rootCmd.HelpTemplate() + "\n" + fullDocsFooter + "\n")
	rootCmd.AddCommand(buildCommand)
	rootCmd.AddCommand(scratchCommand)
	rootCmd.AddCommand(versionCommand)
}
