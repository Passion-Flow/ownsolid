import pathlib,select,subprocess,sys,time
root=pathlib.Path(__file__).parent
while True:
 result=subprocess.run(['python3',str(root/'qualify-current-stage.py'),sys.argv[1]])
 if result.returncode==0:
  published=subprocess.run(['python3',str(root/'publish-current-stage.py'),sys.argv[1]])
  if published.returncode==0:raise SystemExit(0)
  result=published
 print('ACTIVE_FIX_WINDOW_60_SECONDS: fix the observed failure, then enter run, or enter exit. No automatic retry.',flush=True)
 ready,_,_=select.select([sys.stdin],[],[],60)
 if not ready:raise SystemExit(result.returncode)
 if sys.stdin.readline().strip()!='run':raise SystemExit(result.returncode)
