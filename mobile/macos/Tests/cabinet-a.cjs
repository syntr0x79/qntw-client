// Runs the actual bundled macOS HTML with a local native-bridge fixture.
// No native helper, payment, VPN connection, message or external request is made.
const {chromium}=require(process.env.QUANTWAY_NODE_MODULES+'/playwright');
const path=require('path'),fs=require('fs'),assert=require('assert');
const installFixture=require('./fixtures/cabinet-a/bridge.cjs');
let browser;

(async()=>{
 const root=path.resolve(__dirname,'../../..');
 const entry=path.resolve(process.env.QUANTWAY_UI_ENTRY||path.join(root,'mobile/macos/Sources/QuantwayApp/Resources/UI/index.html'));
 const out=path.resolve(process.env.QUANTWAY_QA_OUTPUT||path.join(root,'output/design/quantway-cabinet-app-qa'));
 fs.mkdirSync(out,{recursive:true});
 browser=await chromium.launch({headless:true,executablePath:process.env.QUANTWAY_CHROME||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});
 const page=await browser.newPage({viewport:{width:1100,height:700},deviceScaleFactor:1});page.setDefaultTimeout(8000);
 const errors=[],externalRequests=[],issues=[],checks=[];let views=0;
 page.on('pageerror',e=>errors.push(e.message));
 page.on('request',r=>{if(/^https?:/.test(r.url()))externalRequests.push(r.url())});
 await page.route(/^https?:/,route=>route.abort());
 await page.addInitScript(installFixture);
 await page.goto('file://'+entry);
 await page.getByRole('link',{name:'Кабинет',exact:true}).click();
 await page.locator('.cabinet-a').waitFor();
 const tabs=[['sub','Подписка','subscription'],['devices','Устройства','devices'],['payments','Оплата','payments'],['bonuses','Бонусы','bonuses'],['account','Аккаунт','account'],['other','Другие VPN','providers'],['support','Поддержка','support']];
 async function settle(){await page.evaluate(async()=>{await document.fonts.ready;await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))})}
 async function select(tab){await page.getByRole('tab',{name:tabs.find(t=>t[0]===tab)[1],exact:true}).click();await page.waitForFunction(()=>{const s=document.querySelector('#cabinet-content');return s&&s.children.length&&!s.querySelector(':scope>.q-progress')});await settle()}
 async function scenario(name){await page.evaluate(async name=>{window.__fixture.scenario=name;await window.appBecameActive()},name);await settle()}
 async function geometry(context){
  const found=await page.evaluate(()=>{
   const out=[],content=document.querySelector('#cabinet-content'),main=document.querySelector('.cabinet-a');
   const rect=e=>e.getBoundingClientRect();
   if(/₽\s*₽/.test(main?.textContent||''))out.push({kind:'duplicated-formatted-currency'});
   if(document.documentElement.scrollWidth>innerWidth+1)out.push({kind:'document-horizontal-overflow'});
   for(const el of document.querySelectorAll('.cabinet-a,.cabinet-a #cabinet-content,.ca-card,.ca-device-row,.ca-provider-row,.ca-button')){
    if(el.scrollWidth>el.clientWidth+3)out.push({kind:'horizontal-overflow',class:el.className,width:el.clientWidth,content:el.scrollWidth});
   }
   const tab=main?.dataset.caTab;
   if(['payments','bonuses'].includes(tab)){
    if(content.scrollHeight>content.clientHeight+1)out.push({kind:'vertical-screen-scroll',tab,height:content.clientHeight,content:content.scrollHeight});
    for(const el of content.querySelectorAll('.ca-card,.ca-payment-details'))if(el.scrollHeight>el.clientHeight+2)out.push({kind:'clipped-card',class:el.className,height:el.clientHeight,content:el.scrollHeight});
   }
   for(const grid of document.querySelectorAll('.ca-grid')){
    const cards=[...grid.children].filter(e=>e.classList.contains('ca-card'));
    if(cards.length===2&&Math.abs(rect(cards[0]).height-rect(cards[1]).height)>2)out.push({kind:'unequal-paired-cards',heights:cards.map(e=>rect(e).height)});
    const footers=cards.map(e=>e.querySelector('.ca-card-footer')).filter(Boolean);
    if(footers.length===2&&Math.abs(rect(footers[0]).bottom-rect(footers[1]).bottom)>2)out.push({kind:'misaligned-card-actions',bottoms:footers.map(e=>rect(e).bottom)});
   }
   for(const selector of ['.ca-device-row','.ca-provider-row']){
    const buttons=[...document.querySelectorAll(selector)].map(e=>e.querySelector('button')).filter(Boolean);
    if(buttons.length>1&&buttons.some(e=>Math.abs(rect(e).right-rect(buttons[0]).right)>1))out.push({kind:'action-column',selector});
   }
   const composer=document.querySelector('#support-composer');
   if(composer&&rect(composer).bottom>innerHeight+1)out.push({kind:'support-composer-clipped'});
   const log=document.querySelector('#support-messages'),lastMessage=log?.querySelector('.message:last-child');
   if(lastMessage&&log.scrollHeight-log.clientHeight-log.scrollTop>2)out.push({kind:'latest-support-message-hidden',remaining:log.scrollHeight-log.clientHeight-log.scrollTop});
   return out;
  });
  issues.push(...found.map(issue=>({context,...issue})));views++;
 }
 async function capture(name,context){await geometry(context);await page.screenshot({path:path.join(out,name+'.png')});}

 for(const [width,height] of [[960,640],[1100,700],[1440,900]]){
  await page.setViewportSize({width,height});
  for(const [id,,file] of tabs){await select(id);await capture(`${file}-${width}`,{screen:id,width,scenario:'normal'});}
 }
 await page.setViewportSize({width:960,height:640});
 for(const [id,names] of [['sub',['paused','expired']],['devices',['device-limit','no-devices']],['payments',['unpaid','no-payments','long-history']],['bonuses',['proxy-grace','proxy-error','referral-last','referral-exhausted']],['account',['unlinked']],['support',['no-support']]]){
  await select(id);
  for(const name of names){await scenario(name);await capture(`${id}-${name}-960`,{screen:id,width:960,scenario:name})}
  await scenario('normal');
 }
 for(const [tab,paths] of [['payments',['invoices']],['bonuses',['referral','tg-proxy?peek=1']]]){
  await select(tab);await page.evaluate(async paths=>{for(const path of paths)window.__fixture.fail[path]='Временно недоступно';await window.appBecameActive()},paths);await settle();
  await page.locator('.ca-refresh-error').waitFor();await capture(`${tab}-stale-error-960`,{screen:tab,width:960,scenario:'stale-error'});
  await page.evaluate(async()=>{window.__fixture.fail={};await window.appBecameActive()});await settle();
 }
 checks.push('7 screens at 960/1100/1440; additional API states; no payment/bonus vertical scrolling');

 // A compact preview must still expose every record through its full-list action.
 await select('payments');await scenario('long-history');
 await page.getByRole('button',{name:'Все счета',exact:true}).click();
 await page.getByRole('dialog').waitFor();await page.getByRole('dialog').getByText('4 июня',{exact:true}).waitFor();
 await page.getByRole('button',{name:/Показать ещё счета|Показать ещё|Загрузить ещё/}).click();
 await page.waitForFunction(()=>window.__fixture.calls.some(c=>c.body?.path==='invoices?before=older-invoices'));
 await page.screenshot({path:path.join(out,'all-invoices.png')});await page.keyboard.press('Escape');
 await page.getByRole('button',{name:'Вся история',exact:true}).click();
 await page.getByText('Старая операция',{exact:true}).waitFor();
 await page.getByRole('button',{name:/Показать ещё операции|Показать ещё|Загрузить ещё/}).click();
 await page.getByText('Самая ранняя операция',{exact:true}).waitFor();await page.keyboard.press('Escape');
 assert.equal(await page.evaluate(()=>window.__fixture.pages),2);checks.push('full invoice/history pagination remains accessible');await scenario('normal');

 await select('devices');await page.getByRole('button',{name:'Управлять',exact:true}).first().click();
 const deviceDialog=page.getByRole('dialog');await deviceDialog.waitFor();
 const box=await deviceDialog.boundingBox();assert(box.width<=422&&box.height<430,'device card must remain compact');
 await page.screenshot({path:path.join(out,'device-detail.png')});
 await page.getByRole('button',{name:'Переименовать',exact:true}).click();
 await page.getByLabel('Название',{exact:true}).fill('Mac Studio');await page.getByRole('button',{name:'Сохранить',exact:true}).click();
 await page.getByText('Mac Studio',{exact:true}).waitFor();
 assert(await page.evaluate(()=>window.__fixture.calls.some(c=>c.body?.method==='PATCH'&&c.body?.body?.name==='Mac Studio')));checks.push('device action connected to real PATCH workflow');

 await scenario('blocked-device');await page.getByRole('button',{name:'Управлять',exact:true}).nth(1).click();
 const blockedCallStart=await page.evaluate(()=>window.__fixture.calls.length);await page.getByRole('button',{name:'Ссылка и QR',exact:true}).click();
 await page.getByRole('button',{name:'Перевыпустить ссылку',exact:true}).click();await page.getByRole('button',{name:'Перевыпустить',exact:true}).click();
 await page.waitForFunction(()=>window.__fixture.calls.some(c=>c.body?.path?.endsWith('/reissue')));await page.getByRole('button',{name:'Перевыпустить ссылку',exact:true}).waitFor();
 assert.equal(await page.evaluate(start=>window.__fixture.calls.slice(start).filter(c=>c.body?.path?.endsWith('/connect')).length,blockedCallStart),0,'blocked device link/reissue must not call the unavailable connection endpoint');
 await page.keyboard.press('Escape');await scenario('normal');checks.push('blocked device link can be reissued without fetching its connection URL');
 await page.getByRole('button',{name:'Купить место',exact:true}).click();await page.getByRole('dialog').getByText('До конца текущей подписки',{exact:true}).waitFor();
 await page.screenshot({path:path.join(out,'buy-seat.png')});await page.getByRole('button',{name:'Создать счёт',exact:true}).click();await page.getByRole('button',{name:'Перейти к оплате',exact:true}).waitFor();
 assert(await page.evaluate(()=>window.__fixture.calls.some(c=>c.body?.path==='invoices'&&c.body?.method==='POST'&&c.body.body.plan==='device'&&!Object.hasOwn(c.body.body,'devices'))));await page.keyboard.press('Escape');checks.push('buy-seat action creates device-only invoice without extending the subscription');

 await select('payments');await page.getByRole('button',{name:'Продлить подписку',exact:true}).click();
 assert.equal(await page.locator('#renew-seats').inputValue(),'5','renewal shows total seats, including the 3 seats in the plan');
 await page.getByRole('button',{name:'Создать счёт',exact:true}).click();await page.getByRole('button',{name:'Перейти к оплате',exact:true}).waitFor();
 assert(await page.evaluate(()=>window.__fixture.calls.some(c=>c.body?.path==='invoices'&&c.body?.method==='POST'&&c.body.body.devices===2)));await page.keyboard.press('Escape');checks.push('renew action displays total seats and sends API extra seats; formatted price respected');
 await page.getByRole('button',{name:'Продлить подписку',exact:true}).click();assert.equal(await page.locator('#renew-seats').getAttribute('min'),'3');
 await page.locator('#renew-seats').fill('6');await page.locator('#renew-seats').press('Tab');assert(await page.getByRole('button',{name:'Создать счёт',exact:true}).isDisabled(),'a missing server total must prevent invoice creation');
 await page.locator('#renew-seats').fill('3');await page.locator('#renew-seats').press('Tab');await page.getByRole('button',{name:'Создать счёт',exact:true}).click();await page.getByRole('button',{name:'Перейти к оплате',exact:true}).waitFor();
 assert(await page.evaluate(()=>window.__fixture.calls.some(c=>c.body?.path==='invoices'&&c.body?.method==='POST'&&c.body.body.devices===0)));await page.keyboard.press('Escape');
 await scenario('renew-topup');await page.getByRole('button',{name:'Продлить подписку',exact:true}).click();await page.locator('#renew-seats').fill('6');await page.locator('#renew-seats').press('Tab');
 await page.getByRole('dialog').getByText(/доплата 76 ₽.*23 дня/).waitFor();assert(await page.getByRole('button',{name:'Создать счёт',exact:true}).isEnabled());await page.screenshot({path:path.join(out,'renew-topup.png')});await page.keyboard.press('Escape');await scenario('normal');checks.push('renew permits zero extras, requires a server total and explains remaining-term topup');
 await select('bonuses');await page.getByRole('button',{name:'Скопировать',exact:true}).first().click();
 assert(await page.evaluate(()=>window.__fixture.calls.some(c=>c.action==='copy'&&c.body.text.includes('friend_a7b2'))));checks.push('referral copy uses account data');
 await page.getByRole('button',{name:'Поделиться приглашением',exact:true}).click();
 await page.getByRole('dialog').getByRole('button',{name:'Скопировать приглашение',exact:true}).waitFor();
 await page.evaluate(()=>window.appBecameActive());
 assert.equal(await page.getByRole('dialog').getByRole('button',{name:'Скопировать приглашение',exact:true}).count(),1,'a closed invoice must not overwrite an unrelated modal on foreground refresh');
 await page.keyboard.press('Escape');checks.push('closed invoice does not overwrite an unrelated modal on foreground refresh');

 await select('support');const draft=page.getByLabel('Сообщение в поддержку');await draft.fill('Проверка сохранения черновика');
 const draftRefresh=await page.evaluate(async()=>{const before=document.querySelector('#support-draft');before.focus();before.setSelectionRange(4,10);await window.appBecameActive();const after=document.querySelector('#support-draft');return {sameNode:before===after,focused:document.activeElement===after,selection:[after.selectionStart,after.selectionEnd],text:after.value}});
 assert.deepEqual(draftRefresh,{sameNode:true,focused:true,selection:[4,10],text:'Проверка сохранения черновика'},'support refresh must preserve composer node, focus, caret and draft');
 await draft.press('Enter');await page.getByText('Проверка сохранения черновика',{exact:true}).waitFor();assert(await page.evaluate(()=>window.__fixture.calls.some(c=>c.body?.path==='support/messages'&&c.body?.body?.body==='Проверка сохранения черновика')));checks.push('support refresh preserves DOM, focus, caret and draft; send uses current workflow');

 // Reference images use the approved gallery's matching fixture screen; keep them
 // alongside actual app captures for a visual comparison, not a synthetic pixel score.
 const reference=path.join(root,'output/design/quantway-cabinet-2026-11-04/Quantway-Cabinet-A-Reviewed.html');
 if(fs.existsSync(reference)){
  const ref=await browser.newPage({viewport:{width:1200,height:900},deviceScaleFactor:1});
  for(const [, ,screen] of tabs){await ref.goto('file://'+reference+'#'+new URLSearchParams({screen,variant:'A',size:1100,state:0}));await ref.waitForFunction(screen=>document.querySelector('.app-window')?.dataset.screen===screen,screen);await ref.evaluate(()=>document.fonts.ready);await ref.locator('.app-window').screenshot({path:path.join(out,screen+'-reference.png')})}
  await ref.close();
 }
 const lifecycleCalls=await page.evaluate(()=>window.__fixture.calls.filter(c=>['start','stop','country'].includes(c.action)));assert.equal(lifecycleCalls.length,0);
 const report={views,checks,errors,externalRequests,issues,entry,output:out};fs.writeFileSync(path.join(out,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));await browser.close();
 assert.equal(errors.length,0,'browser errors');assert.equal(externalRequests.length,0,'external requests');assert.equal(issues.length,0,'layout issues');
})().catch(async e=>{console.error(e);await browser?.close();process.exitCode=1});
