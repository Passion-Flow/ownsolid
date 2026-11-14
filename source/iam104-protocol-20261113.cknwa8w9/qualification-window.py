import pathlib,select,subprocess,sys
root=pathlib.Path(__file__).parent
while True:
 result=subprocess.run(['python3',str(root/'qualify-native.py')])
 if result.returncode==0:result=subprocess.run(['python3',str(root/'qualify-live.py')])
 if result.returncode==0:raise SystemExit(0)
 print('ACTIVE_FIX_WINDOW_60_SECONDS: inspect and fix the observed failure, then enter run or exit. No automatic retry.',flush=True)
 ready,_,_=select.select([sys.stdin],[],[],60)
 if not ready or sys.stdin.readline().strip()!='run':raise SystemExit(result.returncode)
