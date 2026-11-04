# Bounded release view wire contract

This package owns the secret-free recorded projection shared by offline scope publication
and control reads. MaximumEvents256 and MaximumBytes262144 are fixed version1 wire safety
limits; they do not truncate or change original journal retention. FirstSequence and Sequence
always identify the exact contiguous retained suffix. All generated timestamps refer to
original journal events, never observation/live health. The projection intentionally omits
raw Status and all nested task/credential/Engine records. The pure seven-field Configuration parser is shared by publisher and reader; it
adds no protected-file IO or business authority. Optional installed_release binds
only a completed account22 M17 activation and preserves the original scope
bindings. Historical snapshots omit it unchanged. See the M17 Admin view contract and focused `go test ./view`.

The additive Kubernetes frame references one actual original-lock ordered index,
its prepared plan and last original event. Its optional nested installed release
requires a real installation/restored/upgrade serving terminal with outcome
`serving_verified`, schema 30 and same signed profile. It does not define a new
manufacturer class. Source order is independent of wall-clock monotonicity. The
legacy format and InstalledRelease predicate retain their original bytes/rules.
