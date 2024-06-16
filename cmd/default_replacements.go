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
		{"github.com/go-co-op/gocron/v2", "v2.5.0", "github.com/LumeWeb/gocron/v2", "v2.0.0-20240617005936-d493ed747a81"},
		{"github.com/go-viper/mapstructure/v2", "v2.0.0", "github.com/LumeWeb/mapstructure/v2", "v2.0.0-20240603224933-c63fee0297e6"},
		{"github.com/gorilla/mux", "v1.8.1", "github.com/cornejong/gormux", "v0.0.0-20240526072501-ce1c97b033ec"},
		{"github.com/tus/tusd-etcd3-locker", "v0.0.0-20200405122323-74aeca810256", "github.com/LumeWeb/tusd-etcd3-locker", "v0.0.0-20240510103936-0d66760cf053"},
		{"github.com/tus/tusd/v2", "v2.4.0", "github.com/LumeWeb/tusd/v2", "v2.2.3-0.20240617010021-713280c42722"},
	}

	// Loop through the list and create replacement rules
	for _, repl := range replList {
		defaultReplacements = append(defaultReplacements, xportal.NewReplace(
			xportal.Dependency{PackagePath: repl.oldMod, Version: repl.oldVer}.String(),
			xportal.Dependency{PackagePath: repl.newMod, Version: repl.newVer}.String(),
		))
	}

}
