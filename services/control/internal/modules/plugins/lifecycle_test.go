package plugins

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func TestPluginLifecycleRequiresLiveJoinedOwnerAndOpaqueCompletion(t *testing.T) {
	public, _, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := wire.KeyID(public)
	s := &Service{Identity: &identity.Service{Config: config.Config{Plugins: config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: id, PublicKey: base64.StdEncoding.EncodeToString(public)}}}}}}
	if s.available() {
		t.Fatal("unstarted plugin execution available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e = s.Start(ctx); e != nil || !s.available() {
		t.Fatal("started current owner", e)
	}
	if e = s.Start(ctx); e == nil || e.Error() != "PLUGIN_ALREADY_STARTED" {
		t.Fatal("duplicate owner", e)
	}
	cancel()
	if s.available() {
		t.Fatal("cancelled owner still available")
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stop, done := context.WithTimeout(context.Background(), time.Second)
			defer done()
			if e := s.Stop(stop); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if s.available() {
		t.Fatal("stopped owner still available")
	}
	if e = s.Start(context.Background()); e != nil {
		t.Fatal("explicit new process owner", e)
	}
	cleanup, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if e = s.Stop(cleanup); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(&CompletedModelList{actor: "synthetic-private-user", question: "synthetic-private-question", proof: storedProof{GrantAuthority: "synthetic-private-grant"}})
	if e != nil || string(raw) != "{}" {
		t.Fatal("private completion became serializable", e)
	}
}
