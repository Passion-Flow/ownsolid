module github.com/Passion-Flow/ownsolid/services/agent

go 1.27.0

require (
	github.com/Passion-Flow/ownsolid/api v0.0.0
	github.com/Passion-Flow/ownsolid/tools/release/offline v0.0.0
	github.com/prometheus/client_model v0.6.3
	github.com/prometheus/common v0.72.1
	google.golang.org/grpc v1.85.0
)

require (
	github.com/Passion-Flow/ownsolid/pkg/license v0.0.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260817212433-ac3dfec99bb1 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/Passion-Flow/ownsolid/api => ../../api

replace github.com/Passion-Flow/ownsolid/pkg/license => ../../pkg/license

replace github.com/Passion-Flow/ownsolid/tools/release/offline => ../../tools/release/offline
