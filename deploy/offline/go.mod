module github.com/Passion-Flow/ownsolid/deploy/offline

go 1.27.0

require (
	github.com/Passion-Flow/ownsolid/api v0.0.0
	github.com/Passion-Flow/ownsolid/tools/release/offline v0.0.0
)

require (
	github.com/Passion-Flow/ownsolid/pkg/license v0.0.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)

replace github.com/Passion-Flow/ownsolid/api => ../../api

replace github.com/Passion-Flow/ownsolid/pkg/license => ../../pkg/license

replace github.com/Passion-Flow/ownsolid/tools/release/offline => ../../tools/release/offline
