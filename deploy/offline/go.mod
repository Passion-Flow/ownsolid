module github.com/Passion-Flow/ownsolid/deploy/offline

go 1.27.0

require (
	filippo.io/age v1.3.2
	github.com/Passion-Flow/ownsolid/api v0.0.0
	github.com/Passion-Flow/ownsolid/pkg/capabilities v0.0.0
	github.com/Passion-Flow/ownsolid/pkg/license v0.0.0
	github.com/Passion-Flow/ownsolid/pkg/operatorsocket v0.0.0
	github.com/Passion-Flow/ownsolid/tools/release/offline v0.0.0
	go.yaml.in/yaml/v3 v3.0.5
)

require (
	filippo.io/hpke v0.4.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)

replace github.com/Passion-Flow/ownsolid/api => ../../api

replace github.com/Passion-Flow/ownsolid/tools/release/offline => ../../tools/release/offline

replace github.com/Passion-Flow/ownsolid/pkg/capabilities => ../../pkg/capabilities

replace github.com/Passion-Flow/ownsolid/pkg/license => ../../pkg/license

replace github.com/Passion-Flow/ownsolid/pkg/operatorsocket => ../../pkg/operatorsocket
