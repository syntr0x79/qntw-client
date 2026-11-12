// Browser-only data fixture. Never starts a native helper or contacts a service.
module.exports=function installCabinetFixture(){
 const ids=['11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','33333333-3333-4333-8333-333333333333'];
 const devices=[
  {id:ids[0],name:'Этот Mac',platform:'macos',online:true,month_bytes:1331439861,last_seen:'Сейчас',created_at:'2026-10-04'},
  {id:ids[1],name:'iPhone',platform:'ios',online:true,month_bytes:882900992,last_seen:'Сегодня, 14:20',created_at:'2026-10-04'},
  {id:ids[2],name:'Домашний ПК',platform:'windows',online:false,month_bytes:2340757176,last_seen:'Вчера, 21:45',created_at:'2026-10-04'}
 ];
 const me={subscription:{status:'active',headline:'Подписка на 3 месяца',days_left:23,until_text:'27 ноября',ring_pct:77,link_works:true,devices:5},features:{promo:true,referral:true,support:true,images:true},telegram:true,bot_username:'quantway_bot',org:null,invite:{bonus_days:7,friend_days:3}};
 const invoice=(n,state='paid')=>({id:'invoice-'+n,title:n===3?'1 месяц':'3 месяца',state,status:state==='paid'?'Оплачено':'Ожидает оплаты',amount_rub:n===3?'399 ₽':'999 ₽',is_fiat:true,date:({1:'4 октября',2:'4 июля',3:'4 июня',4:'4 мая',5:'4 апреля',6:'4 марта'})[n]||'4 ноября',deadline:'5 ноября, 12:00',deadline_at:null,pay_url:'https://example.test/pay/'+n});
 const moves=[{what:'Продление на 3 месяца',when:'4 октября · оплата по СБП',delta:90},{what:'Бонус за приглашение',when:'18 сентября · друг оплатил',delta:7},{what:'Подписка возобновлена',when:'12 сентября',delta:0}];
 const providers=[{id:'work',name:'Рабочий VPN',link:'https://work.example.com/feed',loaded:true,checkedAt:1793776800000},{id:'personal',name:'Личная подписка',link:'https://vpn.example.com/feed',loaded:true}];
 const f=window.__fixture={scenario:'normal',calls:[],fail:{},devices,me,providers,invoices:[invoice(1),invoice(2),invoice(3)],moves,messages:[],invoicePaid:false,pages:0};
 const session=()=>({device_link:'https://qntw.example/sub/design-qa',current_device:ids[0],devices:devices,identity:{kind:f.scenario==='organization'?'owner':'retail'},account:me});
 const freshMe=()=>{me.subscription.status=f.scenario==='paused'?'paused':f.scenario==='expired'?'expired':'active';me.subscription.days_left=f.scenario==='expired'?0:23;me.subscription.link_works=!['expired','paused'].includes(f.scenario);me.telegram=f.scenario!=='unlinked';return me};
 const ticket=()=>({id:1248,subject:'Не подключается VPN на Mac',state:'Открыто',messages:[{from_us:false,body:'Здравствуйте! На телефоне VPN работает, а на Mac подключение не устанавливается.',at:'14:12'},{from_us:true,body:'Здравствуйте! Уточните, пожалуйста: вы подключаетесь через обычный VPN или Tinc?',at:'14:14'},{from_us:false,body:'Через Tinc. Вот что вижу в приложении.',at:'14:16'},{from_us:true,body:'Спасибо! Откройте «Маршруты» и попробуйте другую доступную страну. Если не поможет, пришлите текст ошибки.',at:'14:18'},...f.messages]});
 window.webkit={messageHandlers:{quantway:{postMessage:async({action,body={}})=>{
  f.calls.push({action,body});if(f.fail[action])return {error:f.fail[action],status:503};
  if(action==='bootstrap')return {hasSession:true,providers:f.scenario==='no-providers'?[]:f.providers};
  if(action==='restore-session'||action==='login'){freshMe();return {session:session()}};
  if(action==='profiles')return {profiles:[{job_id:'de',label:'Германия',engine:'xray',protocol:'VLESS'}]};
  if(action==='tinc-catalog')return {data:{countries:[{code:'DE',state:'available'}]}};
  if(action==='qr')return {image:'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aN1sAAAAASUVORK5CYII='};
  if(action==='start'||action==='country'||action==='stop')throw Error('Unexpected network lifecycle action during cabinet QA: '+action);
  if(action!=='cabinet')return {};
  const p=body.path,m=body.method,b=body.body||{};if(f.fail[p])return {error:f.fail[p],status:503};let data={};
  if(p==='me')data=freshMe();
  else if(p==='devices'&&m==='GET'){devices[1].blocked=f.scenario==='blocked-device';const ds=f.scenario==='no-devices'?[]:devices;data={devices:ds,in_use:ds.length,paid:f.scenario==='device-limit'?3:5,link_works:true,overdrawn:false,refused:null,month_bytes:4555098030,online_count:ds.filter(x=>x.online).length,buy:{price:'76 ₽',monthly:'99 ₽ в месяц',left:5}}}
  else if(p==='platforms')data={platforms:[{id:'android',label:'Android'},{id:'ios',label:'iOS'},{id:'macos',label:'macOS'},{id:'windows',label:'Windows'}]};
  else if(p.startsWith('devices/')&&p.endsWith('/connect')){if(devices.find(x=>x.id===p.split('/')[1])?.blocked)return {error:'Устройство заблокировано',status:409};data={sub_url:'https://qntw.example/sub/device-test'}}
  else if(p.startsWith('devices/')&&p.endsWith('/reissue'))data={device:devices.find(x=>x.id===p.split('/')[1]),message:'Ссылка перевыпущена'};
  else if(p.startsWith('devices/')&&m==='PATCH'){devices.find(x=>x.id===p.split('/')[1]).name=b.name;}
  else if(p==='plans'){data={plans:[{id:'month',title:'1 месяц',price:'399 ₽',promo_price:'',devices:3,days:30,per_day:'13,3 ₽',totals:[{devices:0,price:'399 ₽',promo_price:''},{devices:2,price:'597 ₽',promo_price:''}]},{id:'quarter',title:'3 месяца',price:'999 ₽',promo_price:'',devices:3,days:90,save:'−17%',totals:[{devices:0,price:'999 ₽',promo_price:''},{devices:2,price:'1 593 ₽',promo_price:''}]},{id:'year',title:'12 месяцев',price:'2 990 ₽',promo_price:'',devices:3,days:365,save:'−38%',totals:[{devices:0,price:'2 990 ₽',promo_price:''},{devices:2,price:'5 366 ₽',promo_price:''}]}],methods:[{id:'sbp',label:'СБП'},{id:'card',label:'Банковская карта'}],devices:{current:2,max:7,monthly:'99 ₽ в месяц',topup:'',topup_days:23}};if(f.scenario==='renew-topup'){data.devices.topup='76 ₽';data.plans[0].totals.push({devices:3,price:'772 ₽',promo_price:''})}}
  else if(p==='invoices'&&m==='POST')data={invoice:invoice('new','awaiting')};
  else if(p.startsWith('invoices/'))data={invoice:invoice('new',f.invoicePaid?'paid':'awaiting')};
  else if(p==='invoices'){const items=f.scenario==='no-payments'?[]:f.scenario==='unpaid'?[invoice(1,'awaiting'),invoice(2)]:f.scenario==='long-history'?[...f.invoices,invoice(4),invoice(5)]:f.invoices;data={invoices:items,more:f.scenario==='long-history',next_before:'older-invoices'}}
  else if(p.startsWith('invoices?before=')){f.pages++;data={invoices:[invoice(6)],more:false};}
  else if(p==='history')data={moves:f.scenario==='no-payments'?[]:f.scenario==='long-history'?[...moves,{what:'Старая операция',when:'1 сентября',delta:30}]:moves,more:f.scenario==='long-history',next_before:'older-history'};
  else if(p.startsWith('history?before=')){f.pages++;data={moves:[{what:'Самая ранняя операция',when:'1 августа',delta:30}],more:false};}
  else if(p==='promo')data=m==='POST'?{message:'Промокод применён'}:{};
  else if(p==='referral')data={link:'https://t.me/quantway_bot?start=friend_a7b2',per_invite:7,friend_days:3,invited:8,paid:3,days_earned:21,cap:100,left:f.scenario==='referral-last'?1:f.scenario==='referral-exhausted'?0:97,share_text:'Приглашаю в Quantway'};
  else if(p==='tg-proxy?peek=1')data={state:f.scenario==='proxy-error'?'unavailable':f.scenario==='proxy-grace'?'grace':'ready',until_text:'6 ноября',links:f.scenario==='proxy-error'?[]:[{host:'Нидерланды',url:'https://t.me/proxy?server=example.test',tg:'tg://proxy?server=example.test'}]};
  else if(p==='support')data={current:f.scenario==='no-support'?null:ticket(),history:f.scenario==='no-support'?[]:[{id:1247,subject:'Вопрос по оплате',closed:'2026-10-22',reopenable:false},{id:1246,subject:'Подключение телефона',closed:'2026-10-04',reopenable:false}],max_runes:4000};
  else if(p.startsWith('support/tickets/'))data={ticket:{id:1247,subject:'Вопрос по оплате',closed:true,state:'closed'},messages:[{from_us:false,body:'Спасибо, вопрос решён.',at:'22 октября'}],reopenable:false};
  else if(p==='support/messages'){f.messages.push({from_us:false,body:b.body,at:'Сейчас'});data={ticket_id:1248}}
  else if(p==='telegram-link')data={url:'https://t.me/quantway_bot?start=demo'};
  else if(p==='token')data={code:'QW-TEST-CODE'};
  return {data};
 }}}};
};
