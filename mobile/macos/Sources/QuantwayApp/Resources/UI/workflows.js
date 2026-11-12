'use strict';
// Real cabinet data is loaded on demand. Stateboard examples never supply account data.
const W=(()=>{
 const {el,text,action,actions,message,card,field,kv,list,row,modal,confirm,closeDialog,progress}=UI;
 const allTabs=[['sub','Подписка'],['devices','Устройства'],['payments','Оплата'],['bonuses','Бонусы'],['account','Аккаунт'],['other','Другие VPN'],['support','Поддержка']];
 const money=v=>v==null||v===''?'—':String(v).includes('₽')?String(v):v+' ₽';
 const feature=k=>S.session?.account?.features?.[k]!==false;
 let invoiceID=null,invoiceRoot=null,invoiceTimer=null,telegramTimer=null,supportBusy=false,selectedTicket=null,selectedThread=null,attachments=[],sending=false,supportTimer=null;
 const tabs=()=>allTabs;

 function cabinetContext(){return {
  tab:S.tab,session:S.session,me:S.session?.account,source:S.source,providers:S.providers,providersUnsaved:S.providersUnsaved,
  feature,navigate:tab=>navigate('cabinet',tab),renew:()=>renew(),promo,pause,createDevice,buySeat,refresh:()=>loadTab(true),
  deviceOpen:openDevice,deviceLink,rename:renameDevice,deviceMutation,replaceDevice,
  showInvoice,allInvoices:()=>paymentArchive('invoices'),allHistory:()=>paymentArchive('history'),
  copy,open,proxyDetails,shareInvite,telegramLink,rotateCode,purchase,logout,
  providerAdd:()=>modal('ext_first','Добавить подписку',root=>root.append(providerForm())),providerOpen:providerDetails,providerUse:useProvider,
  retryProviders:async()=>{await persistProviders();renderCabinet()}
 } }
 const content=()=>$('#cabinet-content');
 const copy=async value=>{await api('copy',{text:value});toast('Скопировано')};
 async function loadTab(force=false){
  const tab=S.tab;if(tab==='other'||!S.session)return;
  const old=S.cache[tab];if(old?.loading||(!force&&old?.data&&Date.now()-old.at<30000))return;
  const cache=S.cache[tab]={...old,loading:true,error:null};if(S.page==='cabinet'&&!(tab==='support'&&$('.support-layout')))renderCabinet();
  try{
   const get=path=>cabinet('GET',path);
   if(tab==='sub'){const [me,devices]=await Promise.allSettled([get('me'),get('devices')]);if(me.status==='rejected')throw me.reason;cache.data={me:me.value,devices:devices.status==='fulfilled'?devices.value:null};S.session.account=me.value;}
   else if(tab==='devices')cache.data=await get('devices');
   else if(tab==='payments'){const [invoices,history]=await Promise.all([get('invoices'),get('history')]);cache.data={invoices,history};}
   else if(tab==='bonuses'){const [referral,proxy]=await Promise.allSettled([feature('referral')?get('referral'):Promise.resolve(null),get('tg-proxy?peek=1')]);if(old?.data&&proxy.status==='rejected'&&(referral.status==='rejected'||!feature('referral')))throw proxy.reason;cache.data={referral:referral.status==='fulfilled'?referral.value:null,referralError:referral.status==='rejected'?referral.reason.message:null,proxy:proxy.status==='fulfilled'?proxy.value:null,proxyError:proxy.status==='rejected'?proxy.reason.message:null};}
   else if(tab==='account'){cache.data=await get('me');S.session.account=cache.data;}
   else if(tab==='support')cache.data=await get('support');
   cache.at=Date.now();cache.error=null;
  }catch(e){cache.error=e.message;if(e.status===401){S.loginPhase='session_invalid';S.session=null;S.page='login';render();return}}
  finally{cache.loading=false;if(S.page==='cabinet'&&S.tab===tab){if(tab==='support'&&$('.support-layout'))updateSupport(cache);else renderCabinet()}}
 }
 function renderCabinet(){
  const main=$('#app main');if(!main)return;
  CabinetA.shell(main,tabs(),cabinetContext());
  const out=content();clearInterval(supportTimer);supportTimer=null;
  if(S.tab==='other'){renderOther(out);return}
  if(!S.session){out.append(UI.empty('Войдите в Quantway','Аккаунт открывает подписку, устройства и поддержку. Внешние подписки доступны без входа.','Войти',()=>{S.page='login';S.loginPhase='';render()}));return}
  const cache=S.cache[S.tab];if(!cache?.data){out.append(cache?.error?message(cache.error,'err'):progress());if(cache?.error)out.append(action('Повторить',()=>loadTab(true),'primary'));return}
  const compactError=['payments','bonuses'].includes(S.tab);
  if(cache.error&&!compactError)out.append(message('Не удалось обновить. Показаны сохранённые данные: '+cache.error,'warn'),action('Повторить',()=>loadTab(true)));
  const d=cache.data;
  if(S.tab==='sub')renderSub(out,d);
  if(S.tab==='devices')renderDevices(out,d);
  if(S.tab==='payments')renderPayments(out,d);
  if(S.tab==='bonuses')renderBonuses(out,d);
  if(S.tab==='account')renderAccount(out,d);
  if(cache.error&&compactError){const note=out.querySelector('.ca-page-title p');if(note){note.classList.add('ca-refresh-error');note.title=cache.error;note.setAttribute('role','status');note.replaceChildren(el('span',{text:'Сохранённые данные · Не удалось обновить'}),CabinetA.button('Повторить',()=>loadTab(true),'text'));}}
  if(S.tab==='support'){out.style.overflow='hidden';renderSupport(out,d);supportTimer=setInterval(()=>{if(document.visibilityState==='visible')safe(()=>loadTab(true))},8000)}
 }
 function renderSub(out,data){CabinetA.subscription(out,data,cabinetContext())}
 async function pause(resume=false){
  if(!await confirm(resume?'resumed':'pause_confirm',resume?'Возобновить подписку?':'Поставить подписку на паузу?',resume?'Списание дней возобновится по условиям подписки.':'Во время паузы VPN не работает. Остаток дней сохранится до возобновления.',resume?'Возобновить':'Поставить на паузу'))return;
  if(active())await stopConnection();const d=await cabinet('POST','subscription/'+(resume?'resume':'pause'));toast(d.message||(resume?'Подписка возобновлена':'Подписка на паузе'));await loadTab(true);
 }
 async function promo(){
  const state=await cabinet('GET','promo');modal('promo_input','Промокод',root=>{
   if(state.held){root.append(message('Применён код: '+state.held,'ok'),actions(action('Удалить промокод',async()=>{if(await confirm('promo_applied','Удалить промокод?','Цена следующего счёта будет рассчитана без этого кода.','Удалить')){await cabinet('DELETE','promo');toast('Промокод удалён')}}),action('Готово',closeDialog)));return}
   const input=field('Промокод','',{id:'promo-code',placeholder:'Введите код'});root.append(input,actions(action('Отмена',closeDialog,'',{'data-cancel':true}),action('Применить',async()=>{if(!input.input.value.trim()){input.input.setAttribute('aria-invalid','true');throw Error('Введите промокод.')}const r=await cabinet('POST','promo',{code:input.input.value.trim()});root.replaceChildren(message(r.message||(r.granted_days?'Добавлено '+r.granted_days+' дней':'Промокод применён'),'ok'),action('Готово',closeDialog,'primary'));S.cache.sub=null;S.cache.payments=null},'primary')));
  });
 }
 function renderDevices(out,data){CabinetA.devices(out,data,cabinetContext())}
 function openDevice(device){modal('dev_detail',device.name||'Устройство',root=>CabinetA.deviceDialog(root,device,cabinetContext()),{width:420})}
 async function createDevice(){
  const r=await cabinet('GET','platforms'),platforms=(r.platforms||[]).filter(p=>['windows','android','ios','macos','linux','tv'].includes(p.id));
  modal('dev_create','Новое устройство',root=>{const name=field('Название','',{id:'device-name',maxLength:40,placeholder:'Например, телефон'}),platform=field('Платформа',platforms[0]?.id||'android',{select:platforms.map(p=>({id:p.id,label:p.label}))});root.append(name,platform,actions(action('Отмена',closeDialog,'',{'data-cancel':true}),action('Создать',async()=>{const d=await cabinet('POST','devices',{name:name.input.value.trim(),platform:platform.input.value});closeDialog();S.cache.devices=null;S.cache.sub=null;await deviceLink(d.device);if(S.page==='cabinet')safe(()=>loadTab(true))},'primary')))});
 }
 function renameDevice(device){modal('dev_rename_error','Переименовать устройство',root=>{const f=field('Название',device.name,{id:'device-name',maxLength:40,hint:'От 1 до 40 символов'});root.append(f,actions(action('Отмена',closeDialog,'',{'data-cancel':true}),action('Сохранить',async()=>{if(!f.input.value.trim()){f.input.setAttribute('aria-invalid','true');throw Error('Введите название.')}await cabinet('PATCH','devices/'+device.id,{name:f.input.value.trim()});closeDialog();await loadTab(true)},'primary')))});}
 async function deviceMutation(d,operation){
  const own=d.id===S.session.current_device,copyText={block:['Заблокировать устройство?','Доступ «'+d.name+'» будет остановлен. Разблокировать можно в этом списке.','Заблокировать'],unblock:['Разблокировать устройство?','Устройство «'+d.name+'» снова получит доступ.','Разблокировать'],delete:['Удалить устройство?','Ссылка «'+d.name+'» перестанет работать. Это действие нельзя отменить.','Удалить'],reissue:['Перевыпустить ссылку?','Старая ссылка «'+d.name+'» перестанет работать. Новую ссылку нужно будет перенести на устройство.','Перевыпустить']}[operation];
  if(!await confirm('dev_'+operation,copyText[0],copyText[1]+(own?' Текущее подключение этого Mac будет остановлено.':''),copyText[2],operation!=='unblock'))return;
  if(own&&active())await stopConnection();const r=await cabinet(operation==='delete'?'DELETE':'POST','devices/'+d.id+(operation==='delete'?'':'/'+operation));
  if(own){if(operation==='block'||operation==='delete'){S.session.device_link='';S.source=null;S.profiles=[];S.results=[];S.selected=null;S.session.needs_replacement=operation==='delete'}else if(operation==='reissue'){await refreshAccount();if(S.session.device_link)await loadSource({id:'account',name:'Quantway',link:S.session.device_link})}}
  toast(r.message||'Изменения сохранены');await loadTab(true);if(operation==='reissue')await deviceLink(r.device||d);
 }
 async function replaceDevice(d){
  if(!await confirm('dev_replace','Заменить «'+d.name+'» этим Mac?','Доступ выбранного устройства будет отозван. Его место займёт этот Mac.','Заменить',true))return;
  if(active())await stopConnection();await api('replace-device',{device_id:d.id});await refreshBinding();toast('Этот Mac подключён к подписке');await loadTab(true);
 }
 async function refreshBinding(){const r=await api('account-refresh',{bind_device:true});const d=r.data||{};S.session={...S.session,...d.binding,identity:d.identity||S.session.identity,account:d.account||S.session.account};if(S.session.device_link)await loadSource({id:'account',name:'Quantway',link:S.session.device_link})}
 async function deviceLink(device){
  if(device.blocked){modal('dev_link_blocked','Подключение устройства',root=>root.append(message('Устройство заблокировано. Для подключения его нужно разблокировать. При утечке ссылки сначала перевыпустите её.','warn'),actions(action('Перевыпустить ссылку',()=>deviceMutation(device,'reissue')),action('Разблокировать',async()=>{await deviceMutation(device,'unblock');closeDialog()},'primary'))));return}
  const d=await cabinet('GET','devices/'+device.id+'/connect');modal('dev_link_qr','Подключение устройства',root=>{
   root.classList.add('ca-device-link');
   const qrBox=el('div',{class:'ca-qr-placeholder'},progress()),qrActions=el('div',{class:'ca-qr-actions'},el('h3',{text:device.name}),text('Отсканируйте QR-код в VPN-приложении или скопируйте ссылку.'),action('Скопировать ссылку',()=>copy(d.sub_url),'primary'));
   root.append(message('Ссылка даёт доступ к VPN. Передавайте её только владельцу устройства.','warn'),el('div',{class:'ca-qr-row'},qrBox,qrActions),field('Ссылка на подписку',d.sub_url,{readOnly:true,secret:true,id:'device-link'}));
   api('qr',{text:d.sub_url}).then(r=>{if(root.isConnected&&r.image){qrBox.replaceChildren(el('img',{class:'q-qr',src:r.image,alt:'QR-код подключения устройства'}));qrActions.append(action('Сохранить QR',()=>api('save-image',{image:r.image,name:'Quantway-QR.png'})))}}).catch(e=>UI.error(e.message,root));
   root.append(actions(action('Перевыпустить ссылку',()=>deviceMutation(device,'reissue')),device.id===S.session.current_device?action('Использовать на этом Mac',async()=>{if(active())await stopConnection();await loadSource({id:'account',name:'Quantway',link:d.sub_url});closeDialog();navigate('home')}):null));
  },{width:620});
 }
 async function buySeat(){
  const [d,plans]=await Promise.all([cabinet('GET','devices'),cabinet('GET','plans')]);if(!d.buy)throw Error('Дополнительные места сейчас недоступны.');let method=plans.methods?.[0]?.id;
  modal('dev_buy_seat','Дополнительное место',root=>root.append(kv([['Стоимость',money(d.buy.price)],['Действует','До конца текущей подписки'],...(d.buy.monthly?[['При продлении',d.buy.monthly]]:[])]),field('Способ оплаты',method,{select:(plans.methods||[]).map(m=>({id:m.id,label:m.label})),onChange:v=>{method=v}}),text('Одно дополнительное место. Срок подписки не изменится.'),actions(action('Отмена',closeDialog,'',{'data-cancel':true}),action('Создать счёт',async()=>{if(!method)throw Error('Способы оплаты сейчас недоступны.');const r=await cabinet('POST','invoices',{plan:'device',method});S.cache.payments=null;if(r.notice)toast(r.notice,'info');await showInvoice(r.invoice)},'primary'))));
 }
 async function renew(){
  const plans=await cabinet('GET','plans');if(!plans.plans?.length)throw Error('Тарифы сейчас недоступны. Попробуйте позже.');
  let selected=plans.plans.find(p=>p.best)||plans.plans[0],extra=Number(plans.devices?.current??0),method=plans.methods?.[0]?.id;
  const minimum=0,maximum=Number(plans.devices?.max??0);extra=Math.max(minimum,Math.min(maximum,extra));
  modal('renew','Продление подписки',root=>{
   const planList=el('div',{class:'ca-renew-plans'}),details=el('div',{class:'ca-renew-total'});
   const methodField=field('Способ оплаты',method,{select:(plans.methods||[]).map(m=>({id:m.id,label:m.label})),onChange:v=>{method=v}});
   const seats=field('Количество устройств',String((selected.devices||0)+extra),{type:'number',id:'renew-seats',onChange:v=>{extra=Number(v)-Number(selected.devices||0);draw()}});
   seats.classList.add('ca-renew-seats');seats.input.step='1';
   const price=p=>p.promo_price||p.price;
   const quotedTotal=()=>{const total=selected.totals?.find(t=>t.devices===extra);return total?(total.promo_price||total.price):!plans.devices&&extra===0?price(selected):null};
   const createInvoice=action('Создать счёт',async()=>{
    if(!Number.isInteger(extra)||extra<minimum||extra>maximum)throw Error('Проверьте количество устройств.');if(!quotedTotal())throw Error('Выберите количество устройств с доступной стоимостью.');if(!method)throw Error('Способы оплаты сейчас недоступны.');
    const r=await cabinet('POST','invoices',{plan:selected.id,method,devices:extra});S.cache.payments=null;if(r.notice)toast(r.notice,'info');await showInvoice(r.invoice);
   },'primary');
   const draw=()=>{
    seats.input.min=String(Number(selected.devices||0)+minimum);seats.input.max=String(Number(selected.devices||0)+maximum);seats.input.value=String(Number(selected.devices||0)+extra);seats.input.disabled=!plans.devices;
    planList.replaceChildren(...plans.plans.map(p=>{const tile=action('',()=>{selected=p;draw()},'',{'aria-label':p.title,'aria-pressed':p.id===selected.id});tile.className='ca-plan-tile'+(p.id===selected.id?' selected':'');tile.append(el('div',{class:'ca-plan-heading'},el('strong',{text:p.title}),p.save?el('span',{class:'ca-badge',text:p.save}):null),el('strong',{class:'ca-plan-price',text:money(price(p))}),el('small',{class:'ca-plan-note',text:p.per_day?p.per_day+' / день':p.devices+' устройств'}));return tile}));
    const sum=quotedTotal();createInvoice.disabled=!sum;
    details.replaceChildren(kv([['К оплате',sum?money(sum):'Недоступно для выбранного количества']]));if(extra>0&&plans.devices?.monthly)details.append(text('Дополнительное место: '+plans.devices.monthly+'.','muted'));if(extra>Number(plans.devices?.current??0)&&plans.devices?.topup)details.append(text('В сумму включена доплата '+plans.devices.topup+' за каждое новое место на оставшиеся '+CabinetA.dayText(plans.devices.topup_days)+'.','muted'));if(selected.promo_note)details.append(text(selected.promo_note,'muted'));
   };
   draw();root.append(text('Новые дни добавятся к оставшимся. Выберите срок и количество устройств.'),planList,seats,methodField,details,actions(action('Отмена',closeDialog,'',{'data-cancel':true}),createInvoice),text('Разовая оплата. Автоматических списаний нет.','muted'));
  },{width:620});
 }
 function renderPayments(out,data){CabinetA.payments(out,data,cabinetContext())}
 async function paymentArchive(kind){
  const key=kind==='invoices'?'invoices':'history',cache=S.cache.payments?.data;
  let data=cache?.[key]||await cabinet('GET',key);
  const root=modal('payment_archive',kind==='invoices'?'Все счета':'История подписки',()=>{}, {width:620});
  const draw=()=>{const rows=kind==='invoices'?(data.invoices||[]).map(i=>row(i.title,i.date||UI.date(i.deadline_at),money(i.amount_rub)+' · '+(i.status||i.state),false,i.state==='paid'?'ok':'',()=>showInvoice(i))):(data.moves||[]).map(m=>row(m.what,m.when,(m.delta>0?'+':'')+m.delta+' дн.',false,m.delta>0?'ok':''));root.replaceChildren(list(rows));if(!rows.length)root.append(text('Записей пока нет.'));if(data.more)root.append(action('Показать ещё',async()=>{const next=await cabinet('GET',key+'?before='+encodeURIComponent(data.next_before));data={...next,[kind==='invoices'?'invoices':'moves']:[...(data[kind==='invoices'?'invoices':'moves']||[]),...(next[kind==='invoices'?'invoices':'moves']||[])]};draw()}));};draw();
 }
 async function showInvoice(invoice){
  invoiceID=invoice.id;const root=invoiceRoot=modal('invoice_'+invoice.state,'Счёт',()=>{});drawInvoice(root,invoice);
  clearInterval(invoiceTimer);invoiceTimer=setInterval(()=>{if(!root.isConnected){clearInterval(invoiceTimer);if(invoiceRoot===root){invoiceID=null;invoiceRoot=null}return}if(document.visibilityState==='visible'&&['awaiting','review'].includes(invoice.state))safe(async()=>{const r=await cabinet('GET','invoices/'+invoice.id+'?poll=1');if(root.isConnected&&invoiceRoot===root){invoice=r.invoice;drawInvoice(root,invoice)}})},10000);
 }
 function drawInvoice(root,i){
  const paid=i.state==='paid',pending=i.state==='awaiting',review=i.state==='review';root.closest('dialog').dataset.designState='invoice_'+(paid?'paid':pending?'pending':i.state);
  root.replaceChildren(message(i.status||{paid:'Оплачено',awaiting:'Ожидает оплаты',review:'Платёж проверяется',declined:'Платёж отклонён',closed:'Счёт закрыт'}[i.state]||'Статус неизвестен',paid?'ok':pending||review?'warn':'err'),kv([['Тариф',i.title],['Сумма',money(i.amount_rub)],['Срок оплаты',i.deadline||UI.date(i.deadline_at)]]));
  if(i.address)root.append(field('Адрес Bitcoin',i.address,{readOnly:true}),text(i.amount_btc+' BTC'),action('Скопировать адрес',()=>copy(i.address)));
  if(pending||review)root.append(actions(pending&&i.pay_url?action('Перейти к оплате',()=>open(i.pay_url),'primary'):null,action('Проверить оплату',async()=>{const r=await cabinet('GET','invoices/'+i.id+'?poll=1');if(root.isConnected&&invoiceRoot===root)drawInvoice(root,r.invoice);if(r.invoice.state==='paid'){S.cache.sub=null;S.cache.payments=null;await refreshAccount()}})));
  if(paid){S.cache.sub=null;S.cache.payments=null;root.append(action('Готово',async()=>{closeDialog();await loadTab(true)},'primary'))}else if(!pending&&!review)root.append(action('Создать новый счёт',renew,'primary'));
 }
 function renderBonuses(out,data){CabinetA.bonuses(out,data,cabinetContext())}
 function proxyDetails(proxy){modal('proxy_details','Прокси для Telegram',root=>{root.append(message('Прокси защищает только трафик Telegram.','info'));for(const link of proxy?.links||[])root.append(row(link.host,'Telegram'),actions(action('Открыть Telegram',()=>open(link.tg||link.url),'primary'),action('Скопировать ссылку',()=>copy(link.url||link.tg))));if(['ready','grace'].includes(proxy?.state))root.append(action('Перевыпустить прокси',async()=>{if(await confirm('proxy_reissue','Перевыпустить прокси?','Старые ссылки перестанут работать. Настройки Telegram потребуется обновить.','Перевыпустить',true)){await cabinet('POST','tg-proxy/rotate');await loadTab(true)}}));})}
 function shareInvite(referral){modal('invite_share','Пригласить друга',root=>root.append(text(referral.share_text||'Поделитесь ссылкой с другом.'),field('Ссылка приглашения',referral.link,{readOnly:true}),action('Скопировать приглашение',()=>copy(referral.share_text||referral.link),'primary')))}
 function renderAccount(out,data){CabinetA.account(out,data,cabinetContext())}
 async function telegramLink(){
  const r=await cabinet('POST','telegram-link');modal('telegram_link','Привязка Telegram',root=>root.append(message('Откройте бота и подтвердите привязку. Ожидаем подтверждения…','info'),progress(),actions(action('Открыть бота',()=>open(r.url),'primary'),action('Отмена',closeDialog))));
  clearInterval(telegramTimer);telegramTimer=setInterval(()=>{if(!$('#dialog[data-design-state=telegram_link]')){clearInterval(telegramTimer);return}safe(async()=>{const me=await cabinet('GET','me');if(me.telegram){clearInterval(telegramTimer);S.session.account=me;const root=$('.q-dialog-body');if(root)root.replaceChildren(message('Telegram привязан.','ok'),action('Готово',async()=>{closeDialog();await loadTab(true)},'primary'))}})},5000);
 }
 async function rotateCode(){
  if(!await confirm('code_reissue','Выпустить новый код входа?','Старый код и другие сеансы входа могут перестать работать. Сохраните новый код.','Выпустить',true))return;
  const r=await cabinet('POST','token');const code=r.token||r.code;if(!code)throw Error('Сервер не вернул код. Проверьте доступ в кабинете.');
  modal('code_issued','Новый код входа',root=>root.append(field('Новый код',code,{secret:true,readOnly:true}),actions(action('Скопировать',()=>copy(code),'primary'),action('Готово',closeDialog))));
 }
 function purchase(){modal('purchase_code','Присоединить покупку',root=>{const code=field('Код покупки',S.drafts.purchase||'',{secret:true,id:'purchase-code',onChange:v=>S.drafts.purchase=v});root.append(code,actions(action('Отмена',closeDialog,'',{'data-cancel':true}),action('Присоединить',async()=>{if(!code.input.value.trim())throw Error('Введите код покупки.');const r=await cabinet('POST','purchases/attach',{code:code.input.value.trim()});S.drafts.purchase='';root.replaceChildren(message(r.message||'Покупка присоединена.','ok'),action('Готово',async()=>{closeDialog();await refreshAccount();await loadTab(true)},'primary'))},'primary')))});}
 async function logout(){
  if(!await confirm('logout_confirm','Выйти из аккаунта?','VPN будет отключён. Внешние подписки останутся на этом Mac.','Выйти',true))return;
  if(active())await stopConnection();await api('logout');clearInterval(telegramTimer);clearInterval(invoiceTimer);invoiceID=null;selectedTicket=null;selectedThread=null;attachments=[];S.session=null;S.cache={};S.drafts={};S.source=null;S.profiles=[];S.results=[];S.selected=null;S.mode='vpn';S.page='login';S.loginPhase='logout_done';render();
 }
 async function persistProviders(){try{await api('save-providers',{providers:S.providers});S.providersUnsaved=false}catch(e){S.providersUnsaved=true;toast('Подписка доступна до закрытия приложения. Не удалось сохранить в связке ключей.','warn')} }
 function renderOther(out){CabinetA.providers(out,S.providers,cabinetContext())}
 async function useProvider(provider){if(active()){if(!await confirm('source_switch_confirm','Сменить источник?','Текущее соединение будет остановлено.','Сменить'))return;await stopConnection()}await loadSource(provider);renderCabinet();toast('Источник выбран. Подключение — на главной.')}
 async function deleteProvider(provider){if(!await confirm('ext_delete_confirm','Удалить «'+provider.name+'»?','Подписка будет удалена с этого Mac. Аккаунт у провайдера сохранится.','Удалить',true))return;if(S.source?.id===provider.id){if(active())await stopConnection();await loadSource(null)}S.providers=S.providers.filter(p=>p.id!==provider.id);await persistProviders();closeDialog();renderCabinet()}
 function providerDetails(provider){let host='Ссылка скрыта';try{host=new URL(provider.link).hostname}catch{}modal('ext_details',provider.name,root=>root.append(row(host,provider.checkedAt?'Проверено: '+new Date(provider.checkedAt).toLocaleString('ru-RU'):'Маршруты ещё не проверены'),field('Ссылка на подписку',provider.link,{secret:true,readOnly:true}),actions(action(S.source?.id===provider.id?'Активный источник':'Использовать',()=>useProvider(provider),'primary'),action('Скопировать ссылку',()=>copy(provider.link))),actions(action('Изменить',()=>editProvider(provider)),action('Удалить',()=>deleteProvider(provider),'danger'))))}
 function providerForm(provider){
  const draft=provider?{...provider}:(S.drafts.provider||={name:'',link:''});const root=card(provider?'ext_edit':'ext_first',provider?'Изменить подписку':'Добавить подписку');
  const name=field('Название',draft.name,{id:'provider-name',maxLength:80,placeholder:'Например, рабочий VPN',onChange:v=>draft.name=v}),link=field('Ссылка на подписку',draft.link,{id:'provider-link',secret:true,placeholder:'https://…',onChange:v=>draft.link=v});root.append(name,link);
  root.append(actions(provider?action('Отмена',closeDialog,'',{'data-cancel':true}):null,provider?action('Скопировать ссылку',()=>copy(link.input.value)):null,action(provider?'Сохранить':'Загрузить',async()=>{
   const raw=link.input.value.trim();let url;try{url=new URL(raw)}catch{link.input.setAttribute('aria-invalid','true');throw Error('Введите полную HTTPS-ссылку на подписку.')}if(url.protocol!=='https:'||url.username||url.password){link.input.setAttribute('aria-invalid','true');throw Error('Нужна HTTPS-ссылка без имени пользователя и пароля.')}
   if(!name.input.value.trim()){name.input.setAttribute('aria-invalid','true');throw Error('Введите название подписки.')}
   // A successful load validates the feed format, not route reachability.
   const loaded=await api('profiles',{link:raw});if(!loaded.profiles?.length)throw Error('В подписке нет поддерживаемых маршрутов.');
   if(provider&&S.source?.id===provider.id&&active()){if(!await confirm('source_switch_confirm','Обновить активную подписку?','Для обновления ссылки текущее соединение будет остановлено.','Обновить'))return;await stopConnection()}
   const item={id:provider?.id||crypto.randomUUID(),name:name.input.value.trim(),link:raw,loaded:true};S.providers=provider?S.providers.map(p=>p.id===provider.id?item:p):[...S.providers,item];S.drafts.provider={name:'',link:''};await persistProviders();closeDialog();if(S.source?.id===provider?.id)await loadSource(item);renderCabinet();toast('Подписка загружена. Доступность маршрутов проверяется отдельно.');
  },'primary')));return root;
 }
 function editProvider(provider){modal('ext_edit','Изменить подписку',root=>root.append(providerForm(provider)));}
 // Support polling updates only history/status; composer DOM and draft survive.
 function renderSupport(out,d){
  const tickets=el('aside',{class:'support-tickets q-stack',id:'support-tickets'}),chat=el('section',{class:'support-chat','aria-label':'Переписка'},el('div',{class:'chat-head',id:'chat-title'}),el('div',{class:'messages',id:'support-messages',role:'log','aria-live':'polite'}),action('Новые сообщения ↓',()=>{const log=$('#support-messages');log.scrollTop=log.scrollHeight;$('#new-messages').hidden=true},'',{id:'new-messages',hidden:true}),el('div',{class:'chat-composer',id:'support-composer','data-error-scope':'true'}));out.append(el('div',{class:'support-layout'},tickets,chat));updateSupport({data:d});
 }
 function updateSupport(cache){
  if(!$('.support-layout'))return;const d=cache.data;if(!d)return;const tickets=$('#support-tickets');const add=action('',()=>{selectedTicket=null;selectedThread=null;updateSupport(cache);$('#support-draft')?.focus()},'',{'aria-label':d.current?'Написать в текущем обращении':'Новое обращение',title:d.current?'Написать в текущем обращении':'Новое обращение'});add.classList.add('ca-icon-button');add.append(CabinetA.icon('plus'));tickets.replaceChildren(el('div',{class:'ca-ticket-header'},el('h3',{text:'Обращения'}),add));
  if(cache.error)tickets.append(message('Не удалось обновить переписку. '+cache.error,'warn'));
  const rows=[];if(d.current)rows.push(row(d.current.subject||'Текущее обращение','№ '+d.current.id,d.current.state,selectedTicket==null,'ok',()=>{selectedTicket=null;selectedThread=null;updateSupport(cache)}));for(const t of d.history||[])rows.push(row(t.subject||'Обращение № '+t.id,'Закрыто · '+UI.date(t.closed),'Закрыто',selectedTicket===t.id,'',async()=>{const r=await cabinet('GET','support/tickets/'+t.id);selectedTicket=t.id;selectedThread=r;updateSupport(cache)}));tickets.append(list(rows));
  if(!rows.length)tickets.append(text('Обращений пока нет. Напишите, что произошло.','muted'));
  tickets.append(el('small',{class:'ca-support-note',text:S.session?.account?.telegram?'Ответим здесь и в привязанном Telegram.':'Ответим в этом чате.'}));
  const thread=selectedTicket?selectedThread:null,messages=thread?.messages||d.current?.messages||[],closed=!!thread?.ticket?.closed||thread?.ticket?.state==='closed';
  $('#chat-title').replaceChildren(el('span',{class:'ca-avatar',text:'Q'}),el('div',{class:'ca-chat-info'},el('strong',{class:'ca-chat-title',text:thread?.ticket?.subject||d.current?.subject||'Новое обращение'}),el('small',{class:'ca-chat-subtitle',text:closed?'Обращение закрыто':'Поддержка Quantway'})),el('span',{class:'ca-badge'+(closed?'':' ok'),text:closed?'Закрыто':d.current?'Открыто':'Новое'}));
  const log=$('#support-messages'),signature=JSON.stringify([selectedTicket,messages]);if(log.dataset.signature!==signature){const previous=log.dataset.signature,nearBottom=log.scrollHeight-log.clientHeight-log.scrollTop<60,oldScroll=log.scrollTop;log.dataset.signature=signature;log.replaceChildren(...messages.map(m=>{const bubble=el('div',{class:'message'+(!m.from_us?' mine':'')},el('strong',{style:'font-size:11px;color:#93A3B3;display:block;margin-bottom:3px',text:m.author||(m.from_us?'Поддержка':'Вы')}),el('span',{text:m.body}));if(m.note)bubble.append(text(m.note,'muted'));for(const id of m.images||[])bubble.append(action('Открыть вложение',()=>viewAttachment(id)));bubble.append(el('time',{class:'message-time',text:m.at}));return bubble}));if(!messages.length)log.append(text('Сообщения появятся здесь. Опишите проблему и при необходимости приложите снимок экрана.','muted'));log.scrollTop=nearBottom?log.scrollHeight:oldScroll;if(nearBottom)requestAnimationFrame(()=>{if(log.isConnected&&log.dataset.signature===signature)log.scrollTop=log.scrollHeight});const newer=$('#new-messages');newer.hidden=nearBottom||!previous||log.dataset.ticket!==String(selectedTicket);log.dataset.ticket=String(selectedTicket);log.onscroll=()=>{if(log.scrollHeight-log.clientHeight-log.scrollTop<60)newer.hidden=true};}
  const composer=$('#support-composer'),key=String(selectedTicket||'current');if(composer.dataset.key!==key){composer.dataset.key=key;composer.replaceChildren();if(closed&&!thread.reopenable){composer.append(message('Обращение закрыто. Чтобы задать другой вопрос, откройте текущее обращение.','info'),action('Написать в поддержку',()=>{selectedTicket=null;selectedThread=null;updateSupport(cache)}));return}
   if(closed)composer.append(message('Следующее сообщение продолжит это обращение до '+UI.date(thread.deadline)+'.','info'));
   if(d.current?.undeliverable)composer.append(message('Сообщения временно не доставляются. Поддержка увидит их после восстановления связи.','warn'));
   const draftKey='support-'+key,f=field('',S.drafts[draftKey]||'',{multiline:true,id:'support-draft',maxLength:Math.min(d.max_runes||4000,4000),placeholder:'Ваше сообщение…',onChange:v=>{S.drafts[draftKey]=v}});f.input.setAttribute('aria-label','Сообщение в поддержку');f.input.onkeydown=e=>{if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing){e.preventDefault();safe(()=>sendSupport(draftKey))}};
   composer.append(f,el('div',{class:'attachment-preview',id:'support-attachments'}),el('div',{class:'q-form-error'}),actions(feature('images')?CabinetA.button('Изображение',pickAttachments,'quiet','paperclip',{id:'attach-button'}):null,CabinetA.button(sending?'Отправляем…':'Отправить',()=>sendSupport(draftKey),'primary','send',{id:'send-button',disabled:sending})));drawAttachments();
  }
 }
 function drawAttachments(){const wrap=$('#support-attachments');if(!wrap)return;wrap.replaceChildren(...attachments.map((a,i)=>el('figure',{},el('img',{src:'data:'+a.mime+';base64,'+a.image_base64,alt:a.name,style:'object-fit:cover'}),action('Убрать',()=>{if(sending)return;attachments.splice(i,1);drawAttachments()},'',{disabled:sending}))));}
 async function pickAttachments(){if(sending)return;if(attachments.length>=5)throw Error('Можно прикрепить до пяти изображений.');const r=await api('pick-images',{limit:5-attachments.length});for(const image of r.images||[]){if(attachments.length>=5)break;if(image.size>10*1024*1024)throw Error('Каждое изображение должно быть не больше 10 МБ.');attachments.push(image)}drawAttachments();}
 async function sendSupport(draftKey){
  if(sending)return;const body=(S.drafts[draftKey]||'').trim();if(!body&&!attachments.length)throw Error('Напишите сообщение или добавьте изображение.');if([...body].length>4000)throw Error('Сообщение должно быть не длиннее 4000 символов.');
  sending=true;const input=$('#support-draft'),button=$('#send-button'),attach=$('#attach-button');if(input)input.disabled=true;if(button){button.disabled=true;button.textContent='Отправляем…'}if(attach)attach.disabled=true;drawAttachments();
  try{
   // The API accepts one image per request. Remove only confirmed uploads, so retry never resends them.
   if(attachments.length){let caption=body;while(attachments.length){const image=attachments[0];await cabinet('POST','support/images',{image_base64:image.image_base64,caption});attachments.shift();if(caption){S.drafts[draftKey]='';caption='';if(input)input.value=''}drawAttachments()}}
   else{await cabinet('POST','support/messages',{body});S.drafts[draftKey]='';if(input)input.value=''}
   $('.q-form-error',$('#support-composer'))?.replaceChildren();selectedTicket=null;selectedThread=null;await loadTab(true);
  }catch(e){UI.error(e.message+' Текст и неотправленные вложения сохранены. Можно повторить отправку.',$('#support-composer'));}
  finally{sending=false;if(input?.isConnected){input.disabled=false;input.focus()}if(button?.isConnected){button.disabled=false;button.replaceChildren(CabinetA.icon('send'),document.createTextNode('Отправить'))}if(attach?.isConnected)attach.disabled=false;drawAttachments()}
 }
 async function viewAttachment(id){const r=await cabinet('GET','support/attachments/'+id);if(!['image/png','image/jpeg'].includes(r.mime)||!r.image_base64)throw Error('Изображение недоступно.');const image='data:'+r.mime+';base64,'+r.image_base64;modal('attach_received','Вложение',root=>root.append(el('img',{src:image,alt:'Изображение из переписки',style:'max-width:100%;max-height:420px;object-fit:contain;border-radius:10px'}),action('Сохранить изображение',()=>api('save-image',{image,name:'Quantway-support-'+id+(r.mime==='image/png'?'.png':'.jpg')}),'primary')));}
 async function onForeground(){
  if(!S.session)return;const root=invoiceRoot,id=invoiceID;if(id&&root?.isConnected){const r=await cabinet('GET','invoices/'+id+'?poll=1');if(root.isConnected&&root===invoiceRoot&&id===invoiceID)drawInvoice(root,r.invoice)}
  if(S.page==='cabinet')await loadTab(true);
 }
 async function accountAction(key){const routes={devices:'devices',invoices:'payments',history:'payments',support:'support'};if(routes[key])return navigate('cabinet',routes[key]);if(key==='logout')return logout();if(key==='promo')return promo();if(key==='pause')return pause(subscription().status==='paused');if(key==='telegram')return telegramLink();if(key==='purchase')return purchase();if(key==='token')return rotateCode();if(key==='quantway'){if(!S.session)return navigate('login');if(active())await stopConnection();await refreshBinding();return navigate('home')}}
 return {tabs,renderCabinet,loadTab,buySeat,accountAction,onForeground};
})();
