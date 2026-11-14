from pathlib import Path
import json,os
b=Path(__file__).resolve().parent;r=Path(os.environ.get('PROTOTYPE_SOURCE',str(b.parent.parent/'lane-arc107-handoff')))
replacements={}
def overlay(relative,content):
 output=b/(relative.replace('/','-'));output.write_text(content);replacements[str(r/relative)]=str(output)
def replace(text,old,new):
 assert text.count(old)==1,(old,text.count(old));return text.replace(old,new)
p='services/agent/internal/client/session.go';s=(r/p).read_text()
s=s.replace('*grpc.ClientConn','*ageClientConn')
s=replace(s,'rebalance  wire.SessionRebalance','rebalance  wire.SessionRebalance\n retiring *ageRetirement')
s=replace(s,'last   uint64','last   uint64\n connection *ageClientConn\n age ageHint')
s=replace(s,'\ts.dropStream()\n\tif s.conn != nil {\n\t\ts.conn.Close()\n\t\ts.conn = nil\n\t}\n\tif s.pollHTTP != nil {','\ts.dropStream()\n if s.retiring != nil { s.retiring.Close(); s.retiring=nil }\n\tif s.conn != nil {\n\t\ts.conn.Close()\n\t\ts.conn = nil\n\t}\n\tif s.pollHTTP != nil {')
s=replace(s,'\t\tif disposition := s.tryRebalance(ctx, h); disposition != nil {','  if disposition := s.tryAgeHandoff(ctx,h); disposition != nil { return *disposition,nil }\n\t\tif disposition := s.tryRebalance(ctx, h); disposition != nil {')
s=replace(s,'\tconn, e := grpc.NewClient("passthrough:///"+address,','\tconn, e := newAgeClientConn("passthrough:///"+address,')
s=replace(s,'\treturn conn, nil\n}\n\n// openWithHeartbeat','\treturn conn, nil\n}\n\n// openWithHeartbeat')
s=replace(s,'return &sessionStream{stream: stream, cancel: cancel}, nil','return &sessionStream{stream: stream, cancel: cancel,connection:conn}, nil')
s=replace(s,'\tm.ID = st.last + 1',' started:=s.now()\n\tm.ID = st.last + 1')
s=replace(s,'\treturn reply, nil\n}\n\n// streamError',' if m.ID==1 && reply.Disposition.AcceptsSessionRebalance() {st.readAge(started)}\n\treturn reply, nil\n}\n\n// streamError')
s=replace(s,'func (s *Session) tryRebalance(ctx context.Context, h wire.Handshake) *wire.Disposition {','func (s *Session) tryRebalance(ctx context.Context, h wire.Handshake) *wire.Disposition {\n if s.retirementPending(){return nil}')
overlay(p,s)
replacements[str(r/'services/agent/internal/client/session_age_prototype.go')]=str(b/'client_age.go')
p='services/control/internal/platform/httpserver/connection_age.go';s=(r/p).read_text()
s=replace(s,'type connectionDrainKey struct{}','type connectionDrainKey struct{}\ntype connectionCloseKey struct{}')
s=replace(s,'ctx = context.WithValue(ctx, connectionDrainKey{}, aged.drainAt)','ctx = context.WithValue(ctx, connectionDrainKey{}, aged.drainAt)\n ctx=context.WithValue(ctx,connectionCloseKey{},aged.closeAt)')
s+='\nfunc ConnectionCloseAt(ctx context.Context) time.Time { at,_:=ctx.Value(connectionCloseKey{}).(time.Time);return at }\n'
overlay(p,s)
p='services/control/internal/platform/agentgateway/gateway.go';s=(r/p).read_text()
s=replace(s,'"strings"','"strings"\n "strconv"')
s=replace(s,'\t\t\tregistered = true','''
   registered = true
   if !age.IsZero() && reply.Disposition.AcceptsSessionRebalance() {
    now:=time.Now();drain:=age.Sub(now);closing:=httpserver.ConnectionCloseAt(ctx).Sub(now)
    if drain>0 && closing>=drain { if err:=stream.SetHeader(metadata.Pairs("ownsolid-connection-drain-ns",strconv.FormatInt(int64(drain),10),"ownsolid-connection-close-ns",strconv.FormatInt(int64(closing),10)));err!=nil{return nil} }
   }
''')
overlay(p,s)
overlay('services/agent/prototypebridge/bridge.go','''package prototypebridge
import (
 "github.com/Passion-Flow/ownsolid/services/agent/internal/client"
 "github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
)
type Client = client.Client
type Session = client.Session
func New(origin,ca string)(*Client,error){p:=settings.Default();p.ControlRequestSeconds=1;p.ControlHeaderSeconds=1;p.TLSHandshakeSeconds=1;return client.New(origin,ca,p)}
''')
replacements[str(r/'services/control/internal/platform/agentgateway/transport_binding_prototype_test.go')]=str(b/'transport_binding_test.go')
replacements[str(r/'services/control/internal/platform/agentgateway/client_handoff_prototype_test.go')]=str(b/'client_handoff_test.go')
(b/'client-overlay.json').write_text(json.dumps({'Replace':replacements},indent=2)+'\n')
print('prepared',len(replacements),'external overlays')

replacements[str(r/'api/agent/v1/session_age_prototype.go')]=str(b/'wire_session_age.go')
replacements[str(r/'services/agent/sessiontransport/connection.go')]=str(b/'session_transport.go')
(b/'client-overlay.json').write_text(json.dumps({'Replace':replacements},indent=2)+'\n')

replacements[str(r/'api/agent/v1/session_age_prototype_test.go')]=str(b/'wire_session_age_test.go')
replacements[str(r/'services/agent/sessiontransport/connection_test.go')]=str(b/'session_transport_test.go')
(b/'client-overlay.json').write_text(json.dumps({'Replace':replacements},indent=2)+'\n')
p='tools/dev/simulator/internal/link/session.go';s=(r/p).read_text()
s=replace(s,'"google.golang.org/grpc"','"google.golang.org/grpc"\n "github.com/Passion-Flow/ownsolid/services/agent/sessiontransport"')
s=s.replace('*grpc.ClientConn','*sessiontransport.Connection')
s=replace(s,'rebalance  wire.SessionRebalance','rebalance  wire.SessionRebalance\n retiring *sessiontransport.Retirement')
s=replace(s,'budget time.Duration','budget time.Duration\n connection *sessiontransport.Connection\n age connectionAge')
s=replace(s,'conn, e := grpc.NewClient(','conn, e := sessiontransport.New(')
s=replace(s,'func (c *Connection) Next() bool {\n\tc.mu.Lock()\n\tdefer c.mu.Unlock()','func (c *Connection) Next() bool {\n\tc.mu.Lock()\n\tdefer c.mu.Unlock()\n if c.retiring!=nil{_ = c.retiring.Close();c.retiring=nil}')
s=replace(s,'func (c *Connection) Reset() {\n\tc.mu.Lock()\n\tdefer c.mu.Unlock()','func (c *Connection) Reset() {\n\tc.mu.Lock()\n\tdefer c.mu.Unlock()\n if c.retiring!=nil{_ = c.retiring.Close();c.retiring=nil}')
s=replace(s,'\tcurrent := c.conn\n\trank := c.rebalance.Candidate',' if c.retirementPending(){c.mu.Unlock();return nil,nil,nil}\n\tcurrent := c.conn\n\trank := c.rebalance.Candidate')
s=replace(s,'return &Stream{stream: stream, cancel: cancel, budget: budget}, nil','return &Stream{stream: stream, cancel: cancel, budget: budget,connection:conn}, nil')
s=replace(s,'\tm.ID = s.last + 1',' started:=time.Now()\n\tm.ID = s.last + 1')
s=replace(s,'\treturn reply, nil\n}\n\n// streamError',' if m.ID==1&&reply.Disposition.AcceptsSessionRebalance(){s.readAge(started)}\n\treturn reply, nil\n}\n\n// streamError')
overlay(p,s)
replacements[str(r/'tools/dev/simulator/internal/link/session_age_prototype.go')]=str(b/'sim_age.go')
p='tools/dev/simulator/internal/fleet/agent.go';s=(r/p).read_text()
s=replace(s,'\tif a.rebalanceInterval > 0 && !a.partitioned() && !a.active(&a.httpsOnlyUntil) {','''
 if !a.partitioned()&&!a.active(&a.httpsOnlyUntil){
  if disposition:=a.tryConnectionHandoff(ctx,h,idleDeadline);disposition!=nil{return *disposition,nil}
 }
	if a.rebalanceInterval > 0 && !a.partitioned() && !a.active(&a.httpsOnlyUntil) {''')
s+='''
func (a *agent) tryConnectionHandoff(ctx context.Context,h wire.Handshake,idleDeadline time.Time)*wire.Disposition {
 started:=time.Now()
 stream,disposition,err:=a.conn.Handoff(ctx,a.stream,a.node,h,a.pacing,idleDeadline)
 if err!=nil||stream!=nil{a.record(ctx,report.CallConnectionHandoff,started,err)}
 if stream==nil{return nil}
 a.dropStream();a.stream=stream;a.lastExchange=time.Now()
 a.recorder.Succeeded(report.CallHeartbeat,time.Since(started))
 return disposition
}
''';overlay(p,s)
p='tools/dev/simulator/internal/report/report.go';s=(r/p).read_text();s=replace(s,'CallGatewayRebalance = "gateway_rebalance"','CallGatewayRebalance = "gateway_rebalance"\n CallConnectionHandoff = "connection_handoff"');overlay(p,s)
p='tools/dev/simulator/go.mod';s=(r/p).read_text();s=replace(s,'github.com/Passion-Flow/ownsolid/api v0.0.0','github.com/Passion-Flow/ownsolid/api v0.0.0\n github.com/Passion-Flow/ownsolid/services/agent v0.0.0');s+='\nreplace github.com/Passion-Flow/ownsolid/services/agent => ../../../services/agent\n';overlay(p,s)
overlay('tools/dev/simulator/prototypebridge/bridge.go','''package prototypebridge
import (
 "crypto/tls"
 "time"
 "github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/link"
)
type Dialer=link.Dialer
type Node=link.Node
type Stream=link.Stream
func New(origin,ca,certificate,key string)(*Dialer,error){
 d,err:=link.NewDialer(origin,ca,"",2,time.Second);if err!=nil{return nil,err}
 if certificate==""{return d,nil}
 pair,err:=tls.X509KeyPair([]byte(certificate),[]byte(key));if err!=nil{d.Close();return nil,err}
 return d.ForAgent(pair),nil
}
''')
(b/'client-overlay.json').write_text(json.dumps({'Replace':replacements},indent=2)+'\n')
