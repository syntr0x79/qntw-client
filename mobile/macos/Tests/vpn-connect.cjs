// The production UI runs with a local native bridge; no network or VPN is used.
const {chromium}=require(process.env.QUANTWAY_NODE_MODULES+'/playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
let browser;

function bridgeFixture(){
 const profile={job_id:'route-de',label:'Германия · основной',engine:'xray',protocol:'VLESS',transport:'Reality'};
 const job={job_id:'route-de',label:profile.label,engine:'xray',protocol:'vless'};
 const session={device_link:'https://qntw.link/sub/test-only-abcdefghijklmnop',current_device:'mac-test',identity:{kind:'retail'},account:{subscription:{status:'active',headline:'Quantway',days_left:23,link_works:true},features:{}}};
 window.__fixture={calls:[],pending:[],holdProfiles:false,resolveProbe(index=0,kind='ok',overrides={}){
  const pending=this.pending[index];if(!pending)throw Error('No pending probe '+index);
  pending.resolve(kind==='error'?{error:'Проверка маршрутов недоступна.'}:{results:[{job_id:profile.job_id,label:profile.label,outcome:kind,elapsed_ms:41,...(kind==='ok'?{job,control_url:'https://control.example.test/check'}:{}),...overrides}]});
 }};
 window.webkit={messageHandlers:{quantway:{postMessage:async({action,body})=>{
  const f=window.__fixture;f.calls.push({action,body});
  if(action==='bootstrap')return {hasSession:true,providers:[]};
  if(action==='restore-session')return {session};
  if(action==='profiles')return f.holdProfiles?new Promise(resolve=>{f.resolveProfiles=()=>resolve({profiles:[profile]})}):{profiles:[profile]};
  if(action==='tinc-catalog')return {data:{countries:[{code:'DE',state:'available'}]}};
  if(action==='probe')return new Promise(resolve=>{f.pending.push({resolve})});
  if(action==='start'){setTimeout(()=>window.sessionEvent({status:'connected',country:body.country||'DE'}),10);return {accepted:true}}
  if(action==='stop'){window.sessionEvent({status:'ended',exitCode:0});return {}}
  if(action==='cabinet')return {data:session.account};
  return {};
 }}}};
}

(async()=>{
 const entry=path.resolve(process.env.QUANTWAY_UI_ENTRY||path.join(__dirname,'../Sources/QuantwayApp/Resources/UI/index.html'));
 const output=path.resolve(process.env.QUANTWAY_QA_OUTPUT||'/private/tmp/quantway-vpn-connect-qa');fs.mkdirSync(output,{recursive:true});
 browser=await chromium.launch({headless:true,executablePath:process.env.QUANTWAY_CHROME||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});
 const errors=[],requests=[],checks=[];
 async function fresh(){
  const page=await browser.newPage({viewport:{width:1100,height:700},deviceScaleFactor:1});page.setDefaultTimeout(2500);
  page.on('pageerror',e=>errors.push(e.message));
  await page.route(/^https?:/,route=>{requests.push(route.request().url());return route.abort()});
  await page.addInitScript(bridgeFixture);await page.goto('file://'+entry);
  await page.getByRole('button',{name:'Подключить',exact:true}).waitFor();return page;
 }
 const count=(page,action)=>page.evaluate(action=>window.__fixture.calls.filter(c=>c.action===action).length,action);
 const text=(page,value)=>page.locator('main').getByText(value,{exact:true});
 async function off(page){await text(page,'VPN выключен').waitFor()}
 async function settle(page){await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))}

 // An unresolved check must visibly acknowledge either connect control at once.
 let page=await fresh();await page.getByRole('button',{name:'Подключить',exact:true}).click();
 await text(page,'Проверяем маршруты…').waitFor();
 await page.getByRole('button',{name:'Отмена',exact:true}).waitFor();
 assert.equal(await count(page,'probe'),1);assert.equal(await count(page,'start'),0);
 await page.evaluate(()=>window.probeEvent({type:'progress',done:1,total:3}));
 await page.locator('main').getByText(/Проверено 1 из 3/).waitFor();
 await page.screenshot({path:path.join(output,'ordinary-vpn-probing.png')});
 // Repeated clicks on the ring during probing must not duplicate or cancel it.
 assert.equal(await page.locator('button.connection-ring').isDisabled(),true);await page.locator('button.connection-ring').evaluate(button=>button.click());await settle(page);
 assert.equal(await count(page,'probe'),1);assert.equal(await count(page,'start'),0);
 await page.evaluate(()=>window.__fixture.resolveProbe());
 await text(page,'Защищено').waitFor();assert.equal(await count(page,'start'),1);
 const start=await page.evaluate(()=>window.__fixture.calls.find(c=>c.action==='start').body);
 assert.equal(start.mode,'vpn');assert.equal(start.job.job_id,'route-de');assert.equal(start.control_url,'https://control.example.test/check');
 checks.push('connect immediately displays checking state and progress; one probe and one native start');
 await page.close();

 // A late successful probe response cannot connect after cancellation.
 page=await fresh();await page.getByRole('button',{name:'Подключить VPN',exact:true}).click();
 await page.getByRole('button',{name:'Отмена',exact:true}).click();await off(page);
 assert.equal(await count(page,'cancel-probe'),1);await page.evaluate(()=>window.__fixture.resolveProbe());await settle(page);
 assert.equal(await count(page,'start'),0);await off(page);
 checks.push('cancelled probe cannot start a delayed connection');await page.close();

 // A cancelled older check must not overwrite or connect a newer attempt.
 page=await fresh();await page.getByRole('button',{name:'Подключить',exact:true}).click();
 await page.getByRole('button',{name:'Отмена',exact:true}).click();await off(page);
 await page.getByRole('button',{name:'Подключить',exact:true}).click();await text(page,'Проверяем маршруты…').waitFor();
 await page.evaluate(()=>window.__fixture.resolveProbe(0));await settle(page);assert.equal(await count(page,'start'),0);
 await page.evaluate(()=>window.__fixture.resolveProbe(1));await text(page,'Защищено').waitFor();assert.equal(await count(page,'start'),1);
 checks.push('stale result cannot complete a newer connection attempt');await page.close();

 // A failed check remains on the main screen after transient messages expire.
 page=await fresh();await page.getByRole('button',{name:'Подключить',exact:true}).click();
 await page.evaluate(()=>window.__fixture.resolveProbe(0,'timeout'));
 await text(page,'Не удалось подключиться').waitFor();await page.getByRole('button',{name:'Повторить',exact:true}).waitFor();
 assert.equal(await count(page,'start'),0);
 await page.evaluate(()=>document.querySelectorAll('.q-toast').forEach(e=>e.remove()));
 assert.match(await page.locator('main').innerText(),/Нет проверенного рабочего маршрута|Ни один маршрут не прошёл проверку|маршрут.*недоступ|не.*рабоч.*маршрут/i);
 await page.screenshot({path:path.join(output,'ordinary-vpn-failed.png')});
 checks.push('no working route produces persistent failure and retry');await page.close();

 // Network/bridge errors receive the same persistent failure treatment.
 page=await fresh();await page.getByRole('button',{name:'Подключить',exact:true}).click();
 await page.evaluate(()=>window.__fixture.resolveProbe(0,'error'));
 await text(page,'Не удалось подключиться').waitFor();assert.equal(await count(page,'start'),0);
 assert.match(await page.locator('main').innerText(),/Проверка маршрутов недоступна/);
 checks.push('probe request error is visible on the connection screen');await page.close();

 // A route already checked in Routes must go straight to the native start.
 page=await fresh();await page.getByRole('link',{name:'Маршруты',exact:true}).click();
 await page.getByRole('button',{name:'Проверить все',exact:true}).click();
 await page.evaluate(()=>window.__fixture.resolveProbe());await page.getByText('41 мс',{exact:true}).waitFor();
 await page.getByRole('link',{name:'Главная',exact:true}).click();
 await page.getByRole('button',{name:'Подключить',exact:true}).click();await text(page,'Защищено').waitFor();
 assert.equal(await count(page,'probe'),1);assert.equal(await count(page,'start'),1);
 checks.push('previously verified route starts without a duplicate check');await page.close();

 // The probe may return a fresh route absent from the earlier profile list.
 page=await fresh();await page.getByRole('button',{name:'Подключить',exact:true}).click();
 await page.evaluate(()=>window.__fixture.resolveProbe(0,'ok',{job_id:'route-new-nl',label:'Нидерланды · новый',job:{job_id:'route-new-nl',label:'Нидерланды · новый',engine:'xray',protocol:'vless'}}));
 await text(page,'Защищено').waitFor();assert.equal(await count(page,'start'),1);
 const freshStart=await page.evaluate(()=>window.__fixture.calls.find(c=>c.action==='start').body);
 assert.equal(freshStart.job.job_id,'route-new-nl');assert.equal(freshStart.control_url,'https://control.example.test/check');
 assert.match(await page.locator('#source-trigger').innerText(),/Нидерланды · новый/);
 checks.push('new successful job missing from initial profiles is selected and started');await page.close();

 // Safe reason codes are translated both in the persistent failure and Routes.
 page=await fresh();await page.getByRole('button',{name:'Подключить',exact:true}).click();
 await page.evaluate(()=>window.__fixture.resolveProbe(0,'fail',{reason:'timeout'}));
 await text(page,'Не удалось подключиться').waitFor();
 await page.evaluate(()=>document.querySelectorAll('.q-toast').forEach(e=>e.remove()));
 assert.match(await page.locator('main').innerText(),/Время ожидания истекло/);
 assert.doesNotMatch(await page.locator('main').innerText(),/\btimeout\b/);
 assert.equal(await count(page,'start'),0);
 await page.screenshot({path:path.join(output,'ordinary-vpn-timeout-reason.png')});
 await page.getByRole('link',{name:'Маршруты',exact:true}).click();
 await text(page,'Время ожидания истекло').waitFor();
 assert.doesNotMatch(await page.locator('main').innerText(),/\btimeout\b/);
 await page.screenshot({path:path.join(output,'routes-timeout-reason.png')});
 checks.push('timeout reason code is translated on the persistent home error and route row');await page.close();

 // Loading a source cannot leave an enabled connection control with no action.
 page=await fresh();await page.evaluate(()=>{window.__fixture.holdProfiles=true;void loadSource(S.source)});await settle(page);
 const loadingState=await page.locator('main').innerText();
 assert.match(loadingState,/Загруз|Подготов|Проверяем/,'main screen acknowledges source loading');
 const connect=page.getByRole('button',{name:'Подключить',exact:true});
 if(await connect.count())assert.equal(await connect.isDisabled(),true,'connection action is disabled while profiles load');
 await page.evaluate(()=>window.__fixture.resolveProfiles());await off(page);
 checks.push('loading profiles has a visible state instead of a silent ignored click');await page.close();

 assert.deepEqual(errors,[]);assert.deepEqual(requests,[]);
 const report={entry,checks,errors,externalRequests:requests};fs.writeFileSync(path.join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));
 await browser.close();
})().catch(async error=>{console.error(error);await browser?.close();process.exitCode=1});
