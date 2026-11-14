import os,pathlib,select,subprocess,sys,time
root=pathlib.Path(sys.argv[1]);attempt=0
while True:
 attempt+=1;log=root/('window-qualification-'+str(attempt)+'.log')
 with log.open('wb') as out:
  result=subprocess.run(['python3',str(root/'qualify-prototype.py'),str(root)],stdout=out,stderr=subprocess.STDOUT)
 print('QUALIFICATION_EXIT',result.returncode,'LOG',log,flush=True)
 print('ACTIVE_REPAIR_WINDOW: send run or exit within 60 seconds',flush=True)
 ready,_,_=select.select([sys.stdin],[],[],60)
 if not ready:print('ACTIVE_REPAIR_WINDOW_EXPIRED',flush=True);break
 command=sys.stdin.readline().strip()
 if command!='run':break
