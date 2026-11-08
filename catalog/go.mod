module github.com/Passion-Flow/ownsolid/catalog

go 1.27.0

require (
	github.com/Passion-Flow/ownsolid/api v0.0.0
	github.com/Passion-Flow/ownsolid/tools/release/offline v0.0.0
)

require github.com/Passion-Flow/ownsolid/pkg/license v0.0.0 // indirect

replace github.com/Passion-Flow/ownsolid/api => ../api

replace github.com/Passion-Flow/ownsolid/tools/release/offline => ../tools/release/offline

replace github.com/Passion-Flow/ownsolid/pkg/license => ../pkg/license
