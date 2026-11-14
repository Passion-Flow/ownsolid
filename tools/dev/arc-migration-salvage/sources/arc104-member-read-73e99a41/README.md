# ARC-104 member read fault drill

Run `bash reproduce.sh /absolute/path/to/lane-arc` on Docker Desktop with
the cached Linux arm64 PostgreSQL image and Go workspace dependencies.
The runner builds a Go overlay outside the product checkout. It creates one
disposable PostgreSQL 18.6 container with a 512 MiB cap, loopback only and
no external Docker network. The approved pgvector extension is installed in
the disposable test database. It seeds one synthetic joining member with SQL.
The test calls the cell-cluster GET handler over local HTTPS, with real
administrator authority, without replaying a cluster write.

The production outer router and lifecycle are outside this minimal fixture.
The repository's PostgreSQL wire proxy cuts or suppresses COMMIT responses.
The other controls apply an actual PostgreSQL lock timeout, a request deadline
while the query waits, and a cancellation or deadline at the COMMIT call.
Three runs cover seven cases. The runner retains a separate log directory and
disposes only its own PostgreSQL container, volume and private credential files.

The supplied test and PostgreSQL logs are from the successful run. Use
`shasum -a 256 -c SHA256SUMS` to check the preserved inputs and evidence.
