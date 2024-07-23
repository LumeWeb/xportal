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
		{"github.com/go-viper/mapstructure/v2", "v2.0.0", "github.com/LumeWeb/mapstructure/v2", "v2.0.0-20240722104549-387206078839"},
		{"github.com/go-co-op/gocron/v2", "v2.9.0", "github.com/LumeWeb/gocron/v2", "v2.0.0-20240722160415-5b7bf7125d3a"},
		{"github.com/go-co-op/gocron-redis-lock/v2", "v2.9.0", "github.com/LumeWeb/gocron-redis-lock/v2", "v2.0.0-20240722160415-5b7bf7125d3a"},
		{"github.com/gorilla/mux", "v1.8.1", "github.com/cornejong/gormux", "v0.0.0-20240526072501-ce1c97b033ec"},
		{"github.com/tus/tusd-etcd3-locker", "v0.0.0-20200405122323-74aeca810256", "github.com/LumeWeb/tusd-etcd3-locker", "v0.0.0-20240510103936-0d66760cf053"},
		{"github.com/tus/tusd/v2", "v2.4.0", "github.com/LumeWeb/tusd/v2", "v2.2.3-0.20240617010021-713280c42722"},
		{"github.com/ugorji/go/codec", "v1.1.4", "github.com/ugorji/go/codec", "v1.2.7"},
	}

	// Loop through the list and create replacement rules
	for _, repl := range replList {
		defaultReplacements = append(defaultReplacements, xportal.NewReplace(
			xportal.Dependency{PackagePath: repl.oldMod, Version: repl.oldVer}.String(),
			xportal.Dependency{PackagePath: repl.newMod, Version: repl.newVer}.String(),
		))
	}

}
