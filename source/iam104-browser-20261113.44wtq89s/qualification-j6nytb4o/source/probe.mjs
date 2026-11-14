import {readFile,writeFile} from 'node:fs/promises';
import {createRequire} from 'node:module';
import {randomBytes} from 'node:crypto';
const cfg=JSON.parse(await readFile(process.argv[2],'utf8'));
const require=createRequire(cfg.Module);
const {chromium}=require(cfg.Module);
const pkg=require('./package.json');
if(pkg.version!==cfg.ExpectedPlaywright)throw new Error('PINNED_PLAYWRIGHT_VERSION_REQUIRED');
const browser=await chromium.launch({channel:'chromium',headless:true,timeout:15000,args:['--no-proxy-server',`--ignore-certificate-errors-spki-list=${cfg.Pins.join(',')}`,`--host-resolver-rules=${cfg.HostRules}`]});
process.once('SIGTERM',()=>{void browser.close().finally(()=>process.exit(143));});
const results={playwright:pkg.version,browser:browser.version(),cases:[],unpinned_certificate_refused:false};
if(results.browser!==cfg.ExpectedBrowser){await browser.close();throw new Error('PINNED_BROWSER_VERSION_REQUIRED');}
try{
 const origins=new Set([cfg.SP,cfg.IDP,cfg.Untrusted]);
 for(const name of ['positive','missing_flow','swapped_session']){
  const context=await browser.newContext({ignoreHTTPSErrors:false,serviceWorkers:'block'});
  try{
   await context.route('**/*',route=>{const u=new URL(route.request().url());return u.protocol==='https:'&&origins.has(u.origin)?route.continue():route.abort('blockedbyclient');});
   const page=await context.newPage();
   await page.goto(`${cfg.SP}/start?case=${name}`,{waitUntil:'domcontentloaded',timeout:10000});
   if(!page.url().startsWith(cfg.IDP+'/form?'))throw new Error('EXACT_IDP_FORM_REQUIRED');
   const cookies=await context.cookies(cfg.SP);
   const attributes=cookies.map(c=>({name:c.name,domain:c.domain,path:c.path,httpOnly:c.httpOnly,secure:c.secure,sameSite:c.sameSite}));
   for(const [cookie,site] of [['__Host-IAM104Flow','None'],['__Host-IAM104Session','Lax'],['__Host-IAM104CSRF','Strict']]){
    const found=cookies.find(c=>c.name===cookie);if(!found||found.sameSite!==site||!found.secure||!found.httpOnly||found.path!=='/'||found.domain!==new URL(cfg.SP).hostname)throw new Error('EXACT_PRIVATE_COOKIE_ATTRIBUTES_REQUIRED');
   }
   if(name==='missing_flow')await context.clearCookies({name:'__Host-IAM104Flow'});
   if(name==='swapped_session')await context.addCookies([{name:'__Host-IAM104Session',value:randomBytes(32).toString('base64url'),url:cfg.SP,secure:true,httpOnly:true,sameSite:'Lax'}]);
   const finalPath=name==='missing_flow'?'/acs':'/finish';
   await Promise.all([page.waitForURL(cfg.SP+finalPath,{waitUntil:'domcontentloaded',timeout:10000}),page.locator('#submit').click()]);
   if(name==='positive'&&await page.locator('h1').textContent()!=='Transport complete')throw new Error('TRANSPORT_COMPLETION_REQUIRED');
   results.cases.push({name,attributes,final_origin:new URL(page.url()).origin,final_path:new URL(page.url()).pathname});
  }finally{await context.close();}
 }
 const context=await browser.newContext({ignoreHTTPSErrors:false,serviceWorkers:'block'});
 try{const page=await context.newPage();await context.route('**/*',route=>new URL(route.request().url()).origin===cfg.Untrusted?route.continue():route.abort('blockedbyclient'));try{await page.goto(cfg.Untrusted,{waitUntil:'domcontentloaded',timeout:10000});}catch(error){results.unpinned_certificate_refused=/ERR_CERT_AUTHORITY_INVALID|ERR_CERT/.test(String(error));}if(!results.unpinned_certificate_refused)throw new Error('UNPINNED_CERTIFICATE_WAS_ACCEPTED');}finally{await context.close();}
 const creditsContext=await browser.newContext();try{const page=await creditsContext.newPage();await page.goto('chrome://credits',{waitUntil:'domcontentloaded',timeout:10000});await writeFile(cfg.Credits,await page.content(),{mode:0o600});}finally{await creditsContext.close();}
 await writeFile(cfg.Results,JSON.stringify(results,null,2)+'\n',{mode:0o600});
}finally{await browser.close();}
