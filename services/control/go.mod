module github.com/Passion-Flow/ownsolid/services/control

go 1.27.0

require (
	github.com/Passion-Flow/ownsolid/api v0.0.0
	github.com/Passion-Flow/ownsolid/pkg/license v0.0.0
	github.com/Passion-Flow/ownsolid/tools/release/offline v0.0.0
	github.com/aws/aws-sdk-go-v2 v1.47.3
	github.com/aws/aws-sdk-go-v2/service/s3 v1.114.3
	github.com/aws/smithy-go v1.28.5
	github.com/creack/pty v1.1.24
	github.com/felixge/httpsnoop v1.1.1-0.20261001170733-b0adf18e9f12
	github.com/go-sql-driver/mysql v1.10.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.11.0
	github.com/nats-io/nats.go v1.54.0
	github.com/nats-io/nkeys v0.4.16
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	go.mongodb.org/mongo-driver/v2 v2.9.2
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/crypto v0.58.0
	golang.org/x/term v0.47.0
	golang.org/x/text v0.43.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.22 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.11.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.20.6 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.2.0 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)

replace github.com/Passion-Flow/ownsolid/api => ../../api

replace github.com/Passion-Flow/ownsolid/pkg/license => ../../pkg/license

replace github.com/Passion-Flow/ownsolid/tools/release/offline => ../../tools/release/offline
