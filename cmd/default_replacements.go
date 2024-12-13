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
		{"github.com/go-viper/mapstructure/v2", "v2.0.0", "github.com/LumeWeb/mapstructure/v2", "v2.0.0-20241213212524-92525f5828be"},
		{"github.com/go-co-op/gocron/v2", "v2.9.0", "github.com/LumeWeb/gocron/v2", "v2.0.0-20240814201336-2d361739e9be"},
		{"github.com/go-co-op/gocron-redis-lock/v2", "v2.0.1", "github.com/LumeWeb/gocron-redis-lock/v2", "v2.0.0-20240722104549-387206078839"},
		{"github.com/gorilla/mux", "v1.8.1", "github.com/gorilla/mux", "v0.0.0-20240619235004-db9d1d0073d2"},
		{"github.com/tus/tusd/v2", "v2.4.0", "github.com/LumeWeb/tusd/v2", "v2.2.3-0.20241020013555-e29b4c6c01b7"},
		{"github.com/ugorji/go/codec", "v1.1.4", "github.com/ugorji/go/codec", "v1.2.7"},
		{"gorm.io/plugin/dbresolver", "v1.3.0", "gorm.io/plugin/dbresolver", "v1.5.3"},
	}
	// Loop through the list and create replacement rules
	for _, repl := range replList {
		defaultReplacements = append(defaultReplacements, xportal.NewReplace(
			xportal.Dependency{PackagePath: repl.oldMod, Version: repl.oldVer}.String(),
			xportal.Dependency{PackagePath: repl.newMod, Version: repl.newVer}.String(),
		))
	}

}
