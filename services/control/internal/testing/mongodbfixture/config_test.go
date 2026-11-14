package mongodbfixture

import "testing"

func TestNativeMongoPolicyRefusesUnsafeAndMissingValues(t *testing.T) {
	p := Policy{ListenHost: "127.0.0.1", Port: 17421, ReplicaSet: "m21_nondefault", Database: "local_mongo_test", Collection: "m21_eight", CacheMiB: 300, WorkingMemoryMiB: 1536, StartupSeconds: 40, StopSeconds: 10, QuerySeconds: 8, PollMilliseconds: 250, CertificateDays: 1, MaximumLogBytes: 2 << 20, MaximumConnections: 20}
	if p.Validate() != nil {
		t.Fatal("valid nondefault policy refused")
	}
	for _, change := range []func(*Policy){func(v *Policy) { v.ListenHost = "0.0.0.0" }, func(v *Policy) { v.Port = 0 }, func(v *Policy) { v.CacheMiB = 256 }, func(v *Policy) { v.ReplicaSet = "customer" }, func(v *Policy) { v.MaximumConnections = 0 }, func(v *Policy) { v.WorkingMemoryMiB = 1024 }} {
		bad := p
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("unsafe native fixture policy admitted")
		}
	}
}

func TestNativeMongoPlatformBoundary(t *testing.T) {
	for _, pair := range [][2]string{{"darwin", "arm64"}, {"linux", "amd64"}} {
		if !supportedPlatform(pair[0], pair[1]) {
			t.Fatal("declared native fixture refused", pair)
		}
	}
	for _, pair := range [][2]string{{"linux", "arm64"}, {"darwin", "amd64"}, {"windows", "amd64"}} {
		if supportedPlatform(pair[0], pair[1]) {
			t.Fatal("undeclared native fixture admitted", pair)
		}
	}
}
