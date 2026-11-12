const {chromium}=require(process.env.QUANTWAY_NODE_MODULES+'/playwright');
const path=require('path'),fs=require('fs'),assert=require('assert');
(async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.QUANTWAY_CHROME||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});
 const page=await browser.newPage({viewport:{width:1100,height:700},deviceScaleFactor:1});page.setDefaultTimeout(7000);
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(()=>{
  window.__calls=[];window.__fail={};window.__paid=false;window.__msg=[];window.__uploads=0;
  const dev1='11111111-1111-4111-8111-111111111111',dev2='22222222-2222-4222-8222-222222222222';
  let devices=[{id:dev1,name:'Этот Mac',platform:'macos',online:true,month_bytes:123456,last_seen:'Сейчас'},{id:dev2,name:'Телефон',platform:'ios',online:false,month_bytes:9876,last_seen:'Сегодня'}];
  const me={subscription:{status:'active',headline:'Quantway · 1 месяц',days_left:23,until_text:'26 ноября',ring_pct:77,link_works:true},features:{promo:true,referral:true,support:true,images:true},telegram:false,invite:{bonus_days:7,friend_days:3}};
  const session=()=>({device_link:'https://qntw.link/sub/abcdefghijklmnop',current_device:dev1,devices,identity:{kind:'retail'},account:me});
  const invoice=()=>({id:'33333333-3333-4333-8333-333333333333',title:'1 месяц',state:window.__paid?'paid':'awaiting',status:window.__paid?'Оплачено':'Ожидает оплаты',amount_rub:399,pay_url:'https://example.org/pay',deadline:'Сегодня, 23:59'});
  window.webkit={messageHandlers:{quantway:{postMessage:async({action,body})=>{
   window.__calls.push({action,body});if(window.__fail[action])return {error:window.__fail[action],status:action==='login'?401:503};
   if(action==='bootstrap')return {providers:[],hasSession:false};
   if(action==='login'||action==='restore-session')return {session:session()};
   if(action==='profiles')return {profiles:[{job_id:'de',label:'Германия · Франкфурт',engine:'xray',protocol:'VLESS',transport:'Reality'},{job_id:'fi',label:'Финляндия · Хельсинки',engine:'xray',protocol:'Hysteria2'}]};
   if(action==='tinc-catalog')return {data:{countries:[{code:'DE',state:'available'},{code:'FI',state:'available'},{code:'US',state:'preparing'}]}};
   if(action==='probe'){window.probeEvent({type:'progress',done:1,total:2});return {results:[{job_id:'de',outcome:'ok',elapsed_ms:38,job:{engine:'xray'}},{job_id:'fi',outcome:'timeout'}]}}
   if(action==='start')setTimeout(()=>window.sessionEvent({status:'connected',country:body.country||'DE',mesh:{observed_at:new Date().toISOString(),egress:{country:body.country||'DE'},ingresses:[{name:'entry-1',connected:true}],traffic:{rx_bytes:12000,tx_bytes:4000,epoch:'test',sampled_at:new Date().toISOString()}}}),30);
   if(action==='stop')window.sessionEvent({status:'ended',exitCode:0}); // Deliberately synchronous to check stop/switch race.
   if(action==='qr')return {image:'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aN1sAAAAASUVORK5CYII='};
   if(action==='pick-images')return {images:[{name:'screen.png',size:68,mime:'image/png',image_base64:'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aN1sAAAAASUVORK5CYII='}]};
   if(action==='account-refresh')return {data:{account:me,identity:{kind:'retail'},binding:session()}};
   if(action==='cabinet'){
    const p=body.path,m=body.method,b=body.body||{};if(window.__fail[p])return {error:window.__fail[p],status:503};let data={};
    if(p==='me')data=me;
    else if(p==='devices'&&m==='GET')data={devices,in_use:devices.length,paid:3,month_bytes:133332,online_count:1,buy:{price:99,monthly:99,left:'23 дня'}};
    else if(p==='platforms')data={platforms:[{id:'android',label:'Android'},{id:'ios',label:'iOS'},{id:'macos',label:'macOS'},{id:'windows',label:'Windows'},{id:'linux',label:'Linux'}]};
    else if(p==='devices'&&m==='POST'){const d={id:'44444444-4444-4444-8444-444444444444',name:b.name,platform:b.platform};devices.push(d);data={device:d}}
    else if(p.startsWith('devices/')&&p.endsWith('/connect'))data={sub_url:'https://qntw.link/sub/device-secret'};
    else if(p.startsWith('devices/')&&m==='PATCH'){devices.find(d=>d.id===p.split('/')[1]).name=b.name;data={}}
    else if(p==='plans')data={plans:[{id:'month',title:'1 месяц',price:399,devices:3,totals:[{devices:3,price:399},{devices:4,price:498}]},{id:'year',title:'12 месяцев',price:2990,devices:3,totals:[{devices:3,price:2990}]}],methods:[{id:'sbp',label:'СБП'}],devices:{current:3,max:10}};
    else if(p==='invoices'&&m==='POST')data={invoice:invoice()};
    else if(p.startsWith('invoices/'))data={invoice:invoice()};
    else if(p==='invoices')data={invoices:[invoice()],more:false};
    else if(p==='history')data={moves:[{what:'Продление',when:'3 ноября',delta:30}],more:false};
    else if(p==='promo')data=m==='POST'?{message:'Промокод применён'}:{};
    else if(p==='referral')data={link:'https://t.me/quantway_bot?start=friend',per_invite:7,friend_days:3,invited:2,paid:1,days_earned:7,left:100,share_text:'Приглашаю в Quantway'};
    else if(p==='tg-proxy?peek=1')data={state:'ready',links:[{host:'proxy-1',url:'https://t.me/proxy?server=test',tg:'tg://proxy?server=test'}]};
    else if(p==='telegram-link')data={url:'https://t.me/quantway_bot?start=link-test'};
    else if(p==='token')data={code:'QW-new-secret'};
    else if(p==='support')data={current:{id:7,subject:'Проблема с подключением',state:'Открыто',messages:[{from_us:true,author:'Поддержка',body:'Пришлите снимок экрана',at:'14:20'},...window.__msg]},history:[{id:6,subject:'Оплата',closed:'2026-11-01',reopenable:false}],max_runes:4000};
    else if(p==='support/tickets/6')data={ticket:{id:6,subject:'Оплата',state:'closed',closed:'2026-11-01'},messages:[{from_us:false,body:'Спасибо!',at:'Вчера'}],reopenable:false};
    else if(p==='support/messages'){window.__msg.push({from_us:false,body:b.body,at:'Сейчас'});data={ticket_id:7}}
    else if(p==='support/images'){window.__uploads++;window.__msg.push({from_us:false,body:b.caption||'',at:'Сейчас',images:[123]});data={ticket_id:7}}
    return {data};
   }
   return {};
  }}}};
 });
 const output=process.env.QUANTWAY_QA_OUTPUT||'/private/tmp/quantway-macos-qa-0.3.0';fs.mkdirSync(output,{recursive:true});let screens=0,checks=[];
 const shot=async name=>{console.log('screen:',name);assert.equal(await page.locator('#app').innerText().then(t=>t.includes('{{')),false,'unresolved template');await page.screenshot({path:output+'/'+name+'.png'});screens++};const tab=async name=>{await page.getByRole('tab',{name,exact:true}).click()};
 await page.goto('file://'+path.resolve(process.env.QUANTWAY_UI_ENTRY||'mobile/macos/Sources/QuantwayApp/Resources/UI/index.html'));
 await page.getByRole('button',{name:'Войти',exact:true}).click();await page.getByText('Введите код. Его выдаёт @quantway_bot или личный кабинет на сайте.').waitFor();checks.push('empty login');
 await page.evaluate(()=>window.__fail.login='Неверный код');await page.getByLabel('Код входа',{exact:true}).fill('bad');await page.getByRole('button',{name:'Войти',exact:true}).click();await page.getByText('Код не подошёл. Проверьте символы или получите новый в боте.').waitFor();await shot('login-invalid');
 await page.evaluate(()=>delete window.__fail.login);await page.getByLabel('Код входа',{exact:true}).fill('test-code');await page.getByRole('button',{name:'Войти',exact:true}).click();await page.getByRole('heading',{name:'Главная',exact:true}).waitFor();await shot('home');
 await page.getByRole('link',{name:'Маршруты',exact:true}).click();await page.getByRole('button',{name:'Проверить все',exact:true}).click();await page.getByText('38 мс',{exact:true}).waitFor();await shot('routes');
 await page.getByPlaceholder('Страна, город или протокол').fill('ничего');await page.getByRole('heading',{name:'Ничего не найдено'}).waitFor();await page.getByRole('button',{name:'Сбросить поиск'}).click();checks.push('route search/check');
 await page.getByRole('link',{name:'Главная',exact:true}).click();await page.getByRole('button',{name:'Mesh (Tinc)',exact:true}).click();await page.getByRole('button',{name:'Подключить VPN',exact:true}).click();await page.getByText('Защищено',{exact:true}).waitFor();await page.getByRole('button',{name:'Состояние сети',exact:true}).click();await page.getByText('entry-1',{exact:true}).waitFor();await shot('tinc-panel');await page.keyboard.press('Escape');
 await page.getByRole('link',{name:'Маршруты',exact:true}).click();await shot('tinc-countries');await page.getByRole('button',{name:'Выбрать',exact:true}).first().click();await page.getByRole('button',{name:'Переключить',exact:true}).click();await page.waitForFunction(()=>window.__calls.filter(c=>c.action==='start').length===2);checks.push('Tinc switch waits for actual helper end');
 await page.getByRole('link',{name:'Главная',exact:true}).click();await page.getByRole('button',{name:'Отключить VPN',exact:true}).click();await page.getByText('VPN выключен',{exact:true}).waitFor();
 await page.getByRole('link',{name:'Кабинет',exact:true}).click();await page.getByRole('heading',{name:'Подписка',exact:true}).waitFor();await shot('subscription');assert.equal(await page.getByRole('tab').count(),7);
 await tab('Устройства');await page.getByRole('button',{name:'Переименовать'}).first().waitFor();await shot('devices');await page.getByRole('button',{name:'Переименовать'}).first().click();await page.getByLabel('Название',{exact:true}).fill('');await page.getByRole('button',{name:'Сохранить',exact:true}).click();await page.getByText('Введите название.',{exact:true}).waitFor();await page.getByLabel('Название',{exact:true}).fill('Mac Studio');await page.getByRole('button',{name:'Сохранить',exact:true}).click();await page.getByRole('heading',{name:'Mac Studio'}).waitFor();checks.push('device validation/rename');
 await page.getByRole('button',{name:'Удалить',exact:true}).last().click();await shot('delete-confirm');await page.keyboard.press('Escape');assert.equal(await page.evaluate(()=>window.__calls.some(c=>c.body?.method==='DELETE'&&c.body?.path?.startsWith('devices/'))),false);checks.push('cancel destructive action');
 await page.getByRole('button',{name:'Ссылка и QR',exact:true}).first().click();await page.getByAltText('QR-код подключения устройства').waitFor();await shot('device-qr');await page.keyboard.press('Escape');
 await tab('Оплата');await page.getByRole('heading',{name:'История подписки',exact:true}).waitFor();await page.getByRole('button',{name:'Продлить подписку',exact:true}).click();await page.getByRole('button',{name:'Создать счёт',exact:true}).waitFor();await shot('renew');await page.getByRole('button',{name:'Создать счёт',exact:true}).click();await page.getByRole('button',{name:'Перейти к оплате'}).waitFor();await shot('invoice');await page.evaluate(()=>window.__paid=true);await page.getByRole('button',{name:'Проверить оплату'}).click();await page.getByText('Оплачено',{exact:true}).waitFor();await page.getByRole('button',{name:'Готово',exact:true}).click();checks.push('invoice create/read/poll');
 await tab('Бонусы');await page.getByRole('button',{name:'Открыть в Telegram'}).waitFor();await shot('bonuses');await tab('Аккаунт');await page.getByRole('button',{name:'Выпустить новый код'}).waitFor();await shot('account');
 await tab('Другие VPN');await page.getByLabel('Название',{exact:true}).fill('Рабочий VPN');await page.getByLabel('Ссылка на подписку',{exact:true}).fill('https://example.org/feed');await page.getByRole('button',{name:'Загрузить',exact:true}).click();await page.getByRole('heading',{name:'Рабочий VPN',exact:true}).waitFor();await shot('external');assert(await page.evaluate(()=>window.__calls.some(c=>c.action==='save-providers')));checks.push('external format load/save');
 await tab('Поддержка');await page.getByLabel('Сообщение в поддержку').waitFor();await page.getByLabel('Сообщение в поддержку').fill('Мой черновик');await page.evaluate(()=>window.appBecameActive());assert.equal(await page.getByLabel('Сообщение в поддержку').inputValue(),'Мой черновик');await page.getByRole('button',{name:'Прикрепить',exact:true}).click();await page.getByAltText('screen.png').waitFor();await shot('support');
 await page.evaluate(()=>window.__fail['support/images']='Сеть недоступна');await page.getByRole('button',{name:'Отправить',exact:true}).click();await page.getByText(/Текст и неотправленные вложения сохранены/).waitFor();assert.equal(await page.getByLabel('Сообщение в поддержку').inputValue(),'Мой черновик');await page.evaluate(()=>delete window.__fail['support/images']);await page.getByRole('button',{name:'Отправить',exact:true}).click();await page.getByText('Мой черновик',{exact:true}).waitFor();assert.equal(await page.evaluate(()=>window.__uploads),1);checks.push('support drafts/poll/attachment/retry');
 await tab('Другие VPN');await page.setViewportSize({width:960,height:640});await shot('compact');assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);await tab('Поддержка');await shot('compact-support');assert.equal(await page.evaluate(()=>{const r=document.querySelector('#support-composer').getBoundingClientRect();return r.bottom>innerHeight}),false);
 await page.setViewportSize({width:1440,height:900});await page.getByRole('link',{name:'Главная',exact:true}).click();await shot('wide');assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);checks.push('960/1100/1440 layouts');
 await page.evaluate(()=>{window.sessionEvent({status:'failed',code:'network_cleanup_failed',error:'Cleanup failed'});window.sessionEvent({status:'ended',exitCode:0})});await page.getByText('Сеть не восстановлена',{exact:true}).waitFor();assert.equal(await page.getByRole('button',{name:'Повторить',exact:true}).count(),0);await page.getByRole('button',{name:'Инструкция',exact:true}).click();await page.getByText(/Сохраните работу и перезагрузите Mac/).waitFor();checks.push('cleanup failure blocks restart and stays visible after helper end');
 if(errors.length)throw Error(errors.join('\n'));console.log(JSON.stringify({screens,checks,errors},null,2));await browser.close();
})().catch(e=>{console.error(e);process.exit(1)});
