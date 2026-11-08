module github.com/Passion-Flow/ownsolid/deploy/offline

go 1.27.0

require (
	github.com/Passion-Flow/ownsolid/pkg/capabilities v0.0.0
	github.com/Passion-Flow/ownsolid/pkg/license v0.0.0
	github.com/Passion-Flow/ownsolid/pkg/operatorsocket v0.0.0
	go.yaml.in/yaml/v3 v3.0.5
)

replace github.com/Passion-Flow/ownsolid/pkg/capabilities => ../../pkg/capabilities

replace github.com/Passion-Flow/ownsolid/pkg/license => ../../pkg/license

replace github.com/Passion-Flow/ownsolid/pkg/operatorsocket => ../../pkg/operatorsocket
