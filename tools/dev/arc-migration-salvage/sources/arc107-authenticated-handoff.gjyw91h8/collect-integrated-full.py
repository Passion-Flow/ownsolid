from pathlib import Path
import hashlib,json,os,plistlib,re,stat,subprocess
b=Path(__file__).resolve().parent;r=Path((b/'latest-full-repository').read_text().strip());assert r.parent==b and r.name.startswith('full-repository.')
if not(r/'exit').exists():print('Full gate is still running');raise SystemExit(75)
assert(r/'exit').read_text().strip()=='0';source=(r/'ok').read_text().strip();assert source=='20069022c889c22ed9dc804bfcb443534f6f9b85'
revision=(r/'revision').read_text().splitlines();assert revision[0]==source
test=(r/'test.log').read_text();assert '1191 passed' in test and '410 passed' in test and '28 skipped' in test and '10 skipped' in test
result={'source':source,'tree':revision[1],'origin_at_start':revision[2],'start':(r/'start').read_text().strip(),'finish':(r/'finish').read_text().strip(),'check':0,'build':0,'test':0,'logs':{n:hashlib.sha256((r/(n+'.log')).read_bytes()).hexdigest() for n in ('check','build','test')},'browser_summaries':[l.strip() for l in test.splitlines() if re.fullmatch(r'\s+\d+ (passed|skipped)( \([^\n]+\))?',l)]}
mount=Path((r/'mountpoint').read_text().strip());assert mount.parent==Path('/private/tmp') and mount.name.startswith('osa17.') and not mount.exists()
scratch=r/'scratch.sparseimage';i=scratch.lstat();assert stat.S_ISREG(i.st_mode) and i.st_uid==os.getuid() and i.st_nlink==1
info=plistlib.loads(subprocess.check_output(['hdiutil','info','-plist']))
assert all(Path(image.get('image-path','/')).resolve()!=scratch.resolve() for image in info.get('images',[]))
result['scratch_cleanup']={'owned_uid':i.st_uid,'detached':True,'mountpoint_absent':True,'image_bytes_before_unlink':i.st_size,'only_exact_owned_image_removed':True}
scratch.unlink();(r/'scratch-detached-and-removed').write_text('Verified absent from mounted images before exact owned unlink\n')
(b/'integrated-full-qualification.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
