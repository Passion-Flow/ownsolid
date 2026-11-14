import pathlib,select,subprocess,sys
p=pathlib.Path(__file__).parent
while True:
 r=subprocess.run(['python3',str(p/'qualify-storage.py')])
 if r.returncode==0:raise SystemExit(0)
 print('FIX_WINDOW_60_SECONDS: inspect the actual failure and enter run only after a concrete fix; no automatic retry.',flush=True)
 ready,_,_=select.select([sys.stdin],[],[],60)
 if not ready or sys.stdin.readline().strip()!='run':raise SystemExit(r.returncode)
