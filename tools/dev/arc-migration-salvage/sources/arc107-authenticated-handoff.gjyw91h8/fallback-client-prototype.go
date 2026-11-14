package client

import (
 "testing"
 "time"
 wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
 "google.golang.org/grpc/codes"
)

func TestPrototypePreviouslyUsableStreamRetriesWithinReconnectBound(t *testing.T) {
 p := wire.DefaultPacing()
 p.ReconnectMaxSeconds = 7
 c := &control{pacing: &p}
 s := serve(t,c,true)
 current := time.Now()
 s.now = func() time.Time { return current }
 if _,e := s.Heartbeat(t.Context(),wire.Handshake{}); e!=nil || s.Transport()!=TransportStream { t.Fatalf("initial stream: %v",e) }
 c.mu.Lock();c.refuse=codes.Unavailable;c.mu.Unlock()
 s.dropStream()
 for range wire.SessionFallbackProbes {
  if _,e:=s.Heartbeat(t.Context(),wire.Handshake{});e==nil {t.Fatal("transient stream refusal succeeded")}
  if _,e:=s.Heartbeat(t.Context(),wire.Handshake{});e!=nil || s.Transport()!=TransportHTTPS {t.Fatalf("HTTPS probe: %v",e)}
 }
 if delay:=s.fallbackUntil.Sub(current);delay<=0 || delay>time.Duration(p.ReconnectMaxSeconds)*time.Second {t.Fatalf("previously usable stream retained fallback %s beyond advertised %d seconds",delay,p.ReconnectMaxSeconds)}
 c.mu.Lock();c.refuse=codes.OK;c.mu.Unlock()
 current=s.fallbackUntil.Add(-time.Nanosecond)
 if _,e:=s.Heartbeat(t.Context(),wire.Handshake{});e!=nil || s.Transport()!=TransportHTTPS {t.Fatalf("before due: %v",e)}
 current=current.Add(time.Nanosecond)
 if _,e:=s.Heartbeat(t.Context(),wire.Handshake{});e!=nil || s.Transport()!=TransportStream {t.Fatalf("stream recovery: %v",e)}
 if _,e:=s.Claim(t.Context(),0,7);e!=nil {t.Fatal(e)}
 c.mu.Lock();defer c.mu.Unlock()
 if len(c.cursors)!=1 || c.cursors[0]!=7 || len(c.acks)!=0 {t.Fatalf("fallback replayed a claim/ACK or changed cursor: %v/%v",c.cursors,c.acks)}
}
