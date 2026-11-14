"""Write an out-of-tree overlay for the exact original fixture source."""
import json
from pathlib import Path
import subprocess
import sys

source, out = map(Path,sys.argv[1:])
here=Path(__file__).resolve().parent
module=source/"services/control"
prefix="github.com/Passion-Flow/ownsolid/services/control/internal/testing/coldtrace"
replacements={}
def replace_once(text,old,new):
    if text.count(old)!=1:
        raise SystemExit(f"unexpected source shape: {old}")
    return text.replace(old,new)

for name, rel in {
    "database.go":"internal/platform/database/database.go",
    "handler.go":"internal/modules/identity/http/handler.go",
}.items():
    origin=module/rel
    text=origin.read_text()
    text=replace_once(text,'import (','import (\n\t"'+prefix+'"')
    if name=="database.go":
        text=replace_once(text,"\tpc.MaxConns = c.Database.MaxOpenConnections","\tpc.ConnConfig.Tracer = coldtrace.Tracer{}\n\tpc.MaxConns = c.Database.MaxOpenConnections")
    else:
        bind='\tctx, cancel := context.WithTimeout(r.Context(), config.Seconds(requestSeconds))\n\tdefer cancel()\n\tr = r.WithContext(ctx)'
        text=replace_once(text,bind,bind+'''
    if r.Method == http.MethodGet && cellclusterhttp.Owns(r.URL.Path) && strings.HasSuffix(r.URL.Path, "/members") {
        r = r.WithContext(coldtrace.Start(r.Context(),requestID))
    }''')
        result="\t\tdata, status, e := h.api(w, r, audience, origin, requestID)\n"
        text=replace_once(text,result,result+'''
        if e != nil {
            code, problemStatus := httpserver.PublicProblem(e)
            coldtrace.End(r.Context(), problemStatus, code)
        } else {
            coldtrace.End(r.Context(), status, "")
        }
''')
        call="\t\treturn h.CellClusters.Handle(r, a, requestID)"
        pos=text.index(call)
        i=text.rfind("\t\ta, e := s.Authenticate(ctx, session, audience)",0,pos)
        original=text[i:pos+len(call)]
        if i<0 or original.count("Authenticate")!=1:
            raise SystemExit("missing member authorization boundary")
        text=replace_once(text,original,original
            .replace("s.Authenticate(ctx,", 's.Authenticate(coldtrace.Stage(ctx, "authorization"),')
            .replace("Handle(r, a,", 'Handle(r.WithContext(coldtrace.Stage(ctx, "member_read")), a,'))
    target=out/name
    target.write_text(text)
    replacements[str(origin)]=str(target)
replacements[str(module/"internal/testing/coldtrace/trace.go")]=str(here/"trace.go")
subprocess.run(["gofmt","-w",str(out/"database.go"),str(out/"handler.go")],check=True)
(out/"control-overlay.json").write_text(json.dumps({"Replace":replacements}))
