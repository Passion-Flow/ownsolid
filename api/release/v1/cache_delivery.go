package release

// This distinct CPU selection retains the M17 account22 graph and adds only the
// optional M23 directory cache. A capability declares available implementation,
// not that the deployment enabled it or observed a healthy external cache.
const CumulativeCacheCPUProfile = "m23-postgresql-cpu-cache-linux-amd64"
const CumulativeCacheApplicationProfile = "releases-cache-v1"
const ModelCacheCapability = "M23"
