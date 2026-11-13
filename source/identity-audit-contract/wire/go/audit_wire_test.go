package auditwire

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "net/http/httptest"
    "net/http/httptrace"
    "sync/atomic"
    "testing"
    "time"

    identity "github.com/Passion-Flow/ownsolid/sdk/go/identity"
)

const auditPath = "/api/v1/admin/audit-events"
const problem = `{"type":"about:blank","title":"STORAGE_COMMIT_OUTCOME_UNKNOWN","status":409,"code":"STORAGE_COMMIT_OUTCOME_UNKNOWN","request_id":"33333333-3333-4333-8333-333333333333"}`
const page = `{"data":{"items":[],"next_cursor":null}}`

type roundTrip func(*http.Request) (*http.Response, error)
func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(r *http.Request, code int) *http.Response {
    body, media := problem, "application/problem+json"
    if code == 200 { body, media = page, "application/json" }
    return &http.Response{StatusCode: code, Header: http.Header{"Content-Type":{media}}, Body: io.NopCloser(bytes.NewBufferString(body)), Request:r}
}
func params(assistant bool) *identity.ListAuditEventsParams {
    category, cursor, limit := "audit", identity.Cursor("opaque+/=%"), identity.Limit(2)
    out := &identity.ListAuditEventsParams{Category:&category, Cursor:&cursor, Limit:&limit}
    if assistant { value := identity.ListAuditEventsParamsXOwnsolidAuditActor("true"); out.XOwnsolidAuditActor=&value }
    return out
}
func checkRequest(t *testing.T, r *http.Request, assistant bool) {
    t.Helper()
    if r.Method != http.MethodGet || r.URL.Path != auditPath || r.ContentLength != 0 { t.Fatal("changed the bodyless audit GET") }
    q := r.URL.Query()
    if len(q)!=3 || q.Get("category")!="audit" || q.Get("cursor")!="opaque+/=%" || q.Get("limit")!="2" { t.Fatal("changed audit selection") }
    if r.Header.Get("Idempotency-Key")!="" { t.Fatal("invented a mutation replay key") }
    expected := "";if assistant {expected="true"}
    if r.Header.Get("X-Ownsolid-Audit-Actor")!=expected {t.Fatal("changed representation")}
}
func checkProblem(t *testing.T, got *identity.ListAuditEventsHTTPResponse) {
    t.Helper()
    if got==nil || got.StatusCode()!=409 || got.GetApplicationproblemJSON409()==nil || got.GetJSON200()!=nil {t.Fatal("409 did not use its explicit Problem decoder")}
    data,err:=json.Marshal(got.GetApplicationproblemJSON409());if err!=nil {t.Fatal(err)}
    var body map[string]any;if err=json.Unmarshal(data,&body);err!=nil {t.Fatal(err)}
    if body["code"]!="STORAGE_COMMIT_OUTCOME_UNKNOWN" || body["request_id"]!="33333333-3333-4333-8333-333333333333" {t.Fatal("Problem identity changed")}
    if _,ok:=body["data"];ok {t.Fatal("Problem exposed a success page")}
}
func TestProblem409DoesNotRetry(t *testing.T) {
    for _,assistant:=range []bool{false,true} {t.Run(map[bool]string{false:"standard",true:"assistant"}[assistant],func(t *testing.T){
        calls:=0
        c,err:=identity.NewClientWithResponses("https://admin.example.invalid",identity.WithHTTPClient(&http.Client{Transport:roundTrip(func(r *http.Request)(*http.Response,error){
            calls++;checkRequest(t,r,assistant);return response(r,409),nil
        })}));if err!=nil {t.Fatal(err)}
        got,err:=c.ListAuditEventsWithResponse(t.Context(),params(assistant));if err!=nil {t.Fatal(err)};checkProblem(t,got)
        if calls!=1 {t.Fatal("409 triggered an automatic second call")}
    })}
}
func TestExplicitRetryIsANewRead(t *testing.T) {
    calls:=0
    c,err:=identity.NewClientWithResponses("https://admin.example.invalid",identity.WithHTTPClient(&http.Client{Transport:roundTrip(func(r *http.Request)(*http.Response,error){
        calls++;checkRequest(t,r,false);code:=409;if calls==2 {code=200};return response(r,code),nil
    })}));if err!=nil {t.Fatal(err)}
    first,err:=c.ListAuditEventsWithResponse(t.Context(),params(false));if err!=nil {t.Fatal(err)};checkProblem(t,first)
    if calls!=1 {t.Fatal("first read retried itself")}
    second,err:=c.ListAuditEventsWithResponse(t.Context(),params(false));if err!=nil || second.GetJSON200()==nil || calls!=2 {t.Fatalf("explicit reread failed: %v",err)}
}
func TestGeneratedClientReturnsTransportFailure(t *testing.T) {
    calls:=0
    c,err:=identity.NewClientWithResponses("https://admin.example.invalid",identity.WithHTTPClient(&http.Client{Transport:roundTrip(func(r *http.Request)(*http.Response,error){
        calls++;return nil,errors.New("controlled lost reply")
    })}));if err!=nil {t.Fatal(err)}
    got,err:=c.ListAuditEventsWithResponse(context.Background(),params(false))
    if err==nil || got!=nil || calls!=1 {t.Fatal("transport failure acquired a page or an automatic retry")}
}
func TestHTTP1LostReplyRequiresAnIsolatedTransport(t *testing.T) {
    for _,singleUse:=range []bool{false,true} {t.Run(map[bool]string{false:"shared_transport_control",true:"single_use_transport"}[singleUse],func(t *testing.T){
        var auditCalls atomic.Int32
        server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
            if r.URL.Path=="/prime" {io.WriteString(w,"ok");return}
            if r.URL.Path!=auditPath {t.Error("unexpected fixture path");w.WriteHeader(404);return}
            if auditCalls.Add(1)==1 {
                conn,_,err:=w.(http.Hijacker).Hijack();if err!=nil {t.Error(err);return};conn.Close();return
            }
            w.Header().Set("Content-Type","application/problem+json");w.WriteHeader(409);io.WriteString(w,problem)
        }));defer server.Close()
        transport:=http.DefaultTransport.(*http.Transport).Clone()
        transport.Proxy=nil;transport.DisableKeepAlives=singleUse;transport.ForceAttemptHTTP2=false;transport.MaxConnsPerHost=1
        transport.Protocols=new(http.Protocols);transport.Protocols.SetHTTP1(true)
        defer transport.CloseIdleConnections()
        client:=&http.Client{Transport:transport,Timeout:5*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
        prime,err:=client.Get(server.URL+"/prime");if err!=nil {t.Fatal(err)}
        if _,err=io.Copy(io.Discard,prime.Body);err!=nil {t.Fatal(err)};prime.Body.Close()
        c,err:=identity.NewClientWithResponses(server.URL,identity.WithHTTPClient(client));if err!=nil {t.Fatal(err)}
        var connections atomic.Int32
        var firstReused atomic.Bool
        trace:=&httptrace.ClientTrace{GotConn:func(info httptrace.GotConnInfo){if connections.Add(1)==1 {firstReused.Store(info.Reused)}}}
        ctx:=httptrace.WithClientTrace(t.Context(),trace)
        got,err:=c.ListAuditEventsWithResponse(ctx,params(false))
        if firstReused.Load()==singleUse {t.Fatal("fixture did not establish the intended connection reuse control")}
        if singleUse {
            if err==nil || got!=nil || auditCalls.Load()!=1 {t.Fatalf("single-use read replayed after lost reply: calls=%d error=%v",auditCalls.Load(),err)}
        } else {
            if err!=nil || auditCalls.Load()!=2 {t.Fatalf("shared-connection control did not expose the retry: calls=%d error=%v",auditCalls.Load(),err)}
            checkProblem(t,got)
        }
    })}
}
