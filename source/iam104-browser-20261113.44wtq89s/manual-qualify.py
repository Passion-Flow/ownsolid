import pathlib,select,subprocess,sys
root=pathlib.Path(__file__).parent
for attempt in range(3):
    result=subprocess.run([sys.executable,str(root/'qualify.py')])
    if result.returncode==0:raise SystemExit(0)
    if attempt==2:raise SystemExit(result.returncode)
    print('IAM_EXPLICIT_FIX_WINDOW_45_SECONDS run-or-exit',flush=True)
    ready,_,_=select.select([sys.stdin],[],[],45)
    if not ready or sys.stdin.readline().strip()!='run':raise SystemExit(result.returncode)
raise SystemExit(1)
