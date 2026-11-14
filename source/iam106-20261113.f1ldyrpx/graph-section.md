### Complete source-inspection graph

Metadata resolution outside the product selected the following 27 modules,
including the SDK and graph-only/test dependencies. All 35 license, notice and
patent files were retained with source module sums and SHA-256 hashes in
`iam106-20261113.f1ldyrpx`; no compilation or server was started by that
inspection. This is the isolated candidate graph, not the product's final
minimal-version selection, which must be checked again before admission.

| Module | Version | License material |
| --- | --- | --- |
| `github.com/bmatcuk/doublestar/v4` | v4.10.4 | [MIT](https://github.com/bmatcuk/doublestar/blob/v4.10.4/LICENSE) |
| `github.com/cespare/xxhash/v2` | v2.3.0 | [MIT](https://github.com/cespare/xxhash/blob/v2.3.0/LICENSE) |
| `github.com/go-chi/chi/v5` | v5.5.0 | [MIT](https://github.com/go-chi/chi/blob/v5.5.0/LICENSE) |
| `github.com/go-jose/go-jose/v4` | v4.1.9 | [Apache-2.0, BSD-3-Clause](https://github.com/go-jose/go-jose/blob/v4.1.9/LICENSE) |
| `github.com/go-logr/logr` | v1.4.4 | [Apache-2.0](https://github.com/go-logr/logr/blob/v1.4.4/LICENSE) |
| `github.com/go-logr/stdr` | v1.2.2 | [Apache-2.0](https://github.com/go-logr/stdr/blob/v1.2.2/LICENSE) |
| `github.com/golang/mock` | v1.6.0 | [Apache-2.0](https://github.com/golang/mock/blob/v1.6.0/LICENSE) |
| `github.com/google/go-github/v31` | v31.0.0 | [BSD-3-Clause](https://github.com/google/go-github/blob/v31.0.0/LICENSE) |
| `github.com/google/go-querystring` | v1.2.0 | [BSD-3-Clause](https://github.com/google/go-querystring/blob/v1.2.0/LICENSE) |
| `github.com/google/uuid` | v1.6.0 | [BSD-3-Clause](https://github.com/google/uuid/blob/v1.6.0/LICENSE) |
| `github.com/gorilla/securecookie` | v1.1.2 | [BSD-3-Clause](https://github.com/gorilla/securecookie/blob/v1.1.2/LICENSE) |
| `github.com/jeremija/gosubmit` | v0.2.8 | [MIT](https://github.com/jeremija/gosubmit/blob/v0.2.8/LICENSE) |
| `github.com/muhlemmer/gu` | v0.3.1 | [Unlicense](https://github.com/muhlemmer/gu/blob/v0.3.1/LICENSE) |
| `github.com/muhlemmer/httpforwarded` | v0.1.0 | [BSD-3-Clause](https://github.com/muhlemmer/httpforwarded/blob/v0.1.0/LICENSE) |
| `github.com/rs/cors` | v1.11.1 | [MIT](https://github.com/rs/cors/blob/v1.11.1/LICENSE) |
| `github.com/stretchr/testify` | v1.12.1 | [MIT, BSD-3-Clause, ISC](https://github.com/stretchr/testify/blob/v1.12.1/LICENSE) |
| `github.com/zitadel/oidc/v3` | v3.52.0 | [Apache-2.0](https://github.com/zitadel/oidc/blob/v3.52.0/LICENSE) |
| `github.com/zitadel/schema` | v1.3.2 | [BSD-3-Clause](https://github.com/zitadel/schema/blob/v1.3.2/LICENSE) |
| `go.opentelemetry.io/auto/sdk` | v1.2.1 | [Apache-2.0](https://github.com/open-telemetry/opentelemetry-go-instrumentation/blob/sdk/v1.2.1/LICENSE) |
| `go.opentelemetry.io/otel` | v1.46.0 | [Apache-2.0, BSD-3-Clause](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/LICENSE) |
| `go.opentelemetry.io/otel/metric` | v1.46.0 | [Apache-2.0, BSD-3-Clause](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/LICENSE) |
| `go.opentelemetry.io/otel/trace` | v1.46.0 | [Apache-2.0, BSD-3-Clause](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/LICENSE) |
| `go.yaml.in/yaml/v3` | v3.0.5 | [Apache-2.0, MIT](https://github.com/yaml/go-yaml/blob/v3.0.5/LICENSE) |
| `golang.org/x/crypto` | v0.53.0 | [BSD-3-Clause](https://github.com/golang/crypto/blob/v0.53.0/LICENSE) |
| `golang.org/x/net` | v0.55.0 | [BSD-3-Clause](https://github.com/golang/net/blob/v0.55.0/LICENSE) |
| `golang.org/x/oauth2` | v0.38.0 | [BSD-3-Clause](https://github.com/golang/oauth2/blob/v0.38.0/LICENSE) |
| `golang.org/x/text` | v0.43.0 | [BSD-3-Clause](https://github.com/golang/text/blob/v0.43.0/LICENSE) |

All listed licenses permit commercial and disconnected use and redistribution
subject to their required notices and terms. Preserve the Unlicense text as
well as the MIT, BSD, ISC and Apache material. The JOSE JSON fork also carries
BSD-3-Clause. Testify includes BSD-3-Clause `difflib` and ISC `spew` material;
the three OpenTelemetry API modules include BSD-3-Clause code in their Apache
license files. YAML has per-file MIT and Apache coverage, not a blanket choice
of either license. Retain the SDK/YAML notices and the Go subrepositories'
patent grants. Graph-only GitHub clients, test generators and telemetry APIs
do not authorize outbound services or runtime exporters. Their actual import
and network behavior still need the prototype's disconnected qualification.

