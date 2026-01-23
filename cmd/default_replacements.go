package xportalcmd

import (
	"go.lumeweb.com/xportal"
)

var (
	defaultReplacements = []xportal.Replace{}
)

func init() {
	// Define the starting list of replacements
	replList := []struct {
		oldMod string
		oldVer string
		newMod string
		newVer string
	}{
		{"git.apache.org/thrift.git", "v0.0.0-20180902110319-2566ecd5d999", "github.com/apache/thrift", "v0.0.0-20180902110319-2566ecd5d999"},
	}
	// Loop through the list and create replacement rules
	for _, repl := range replList {
		defaultReplacements = append(defaultReplacements, xportal.NewReplace(
			xportal.Dependency{PackagePath: repl.oldMod, Version: repl.oldVer}.String(),
			xportal.Dependency{PackagePath: repl.newMod, Version: repl.newVer}.String(),
		))
	}

}
