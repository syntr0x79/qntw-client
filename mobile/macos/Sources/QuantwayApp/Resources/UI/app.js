'use strict';
const $=(q,r=document)=>r.querySelector(q), $$=(q,r=document)=>[...r.querySelectorAll(q)];
const S={page:'login',session:null,providers:[],source:null,profiles:[],results:[],selected:null,mode:'vpn',tab:'sub',filter:'all',query:'',status:'off',busy:false,helperRunning:false,catalog:null,country:'',assigned:'',started:0,traffic:null,mesh:null,cache:{},drafts:{},loginPhase:'restore_running',code:'',loadEpoch:0,loginEpoch:0,probeEpoch:0,extra:false,checkedAt:null,catalogError:null};
let connectEpoch=0;
const api=async(action,body={})=>{
 if(!window.webkit?.messageHandlers?.quantway)throw Error('Откройте Quantway.app для работы с аккаунтом и VPN.');
 const v=await window.webkit.messageHandlers.quantway.postMessage({action,body:Object.fromEntries(Object.entries(body).filter(([,v])=>v!==undefined))});
 if(v?.error){const e=Error(v.error);e.status=v.status;e.code=v.code;throw e}return v||{};
};
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const open=url=>api('open',{url});
const toast=(value,kind='ok')=>UI.toast(value,kind);
const active=()=>S.helperRunning||!['off','failed'].includes(S.status);
const subscription=()=>S.session?.account?.subscription||{};
const days=()=>subscription().days_left;
const countryName=code=>{try{return code?new Intl.DisplayNames(['ru'],{type:'region'}).of(code):'—'}catch{return code||'—'}};
const label=()=>S.mode==='tinc'?countryName(S.country):(S.selected?.label||'Выберите маршрут');
const probeReason=r=>({http_unauthorized:'Сервис проверки требует вход',http_forbidden:'Сервис проверки отклонил запрос',http_not_found:'Адрес проверки не найден',http_rate_limited:'Сервис ограничил частоту проверок',http_4xx:'Сервис проверки отклонил запрос',http_5xx:'Ошибка сервиса проверки',http_status_error:'Неожиданный ответ сервиса',connection_refused:'Сервер отклонил соединение',connection_reset:'Соединение сброшено',unexpected_eof:'Соединение преждевременно закрыто',network_unreachable:'Сеть сервера недоступна',socket_permission:'macOS запретила сетевой запрос',dns_failed:'Не удалось определить адрес сервера',socket_binding_failed:'Не удалось выбрать сетевой интерфейс',cancelled:'Проверка отменена',network_changed:'Сеть изменилась',configuration_or_network:'Ошибка настроек или сети',request_failed:'Нет ответа через маршрут',tls_verification_failed:'Ошибка сертификата сервера',handshake_timeout:'Нет ответа от VPN-сервера',timeout:'Время ожидания истекло'}[r?.reason]||{cancelled:'Проверка отменена',timeout:'Время ожидания истекло',unprobeable:'Маршрут не удалось проверить'}[r?.outcome]||'Нет связи');
const subText=()=>S.session?(days()!=null?'Подписка · '+days()+' дн.':S.session.identity?.kind==='owner'?'Организация':'Quantway'):(S.source?.name||'Добавьте подписку');
const segment=on=>'height:36px;padding:0 14px;border:0;border-radius:9px;font:inherit;font-size:13px;font-weight:700;cursor:pointer;'+(on?'background:#1F3340;color:#E8EEF3':'background:transparent;color:#93A3B3');
async function safe(fn){try{return await fn?.()}catch(e){toast(e.message,'err')}}
const cabinet=async(method,path,body)=>{const r=await api('cabinet',{method,path,body});return r.data||{}};
function resolve(v,scope){return v.trim().split('.').reduce((x,k)=>x?.[k],scope)}
function bind(node,scope){
 if(node.nodeType===3){node.textContent=node.textContent.replace(/\{\{(.*?)\}\}/g,(_,v)=>resolve(v,scope)??'');return}
 if(node.nodeType!==1)return;
 if(node.tagName==='SC-IF'&&!resolve(node.getAttribute('value').replace(/[{}]/g,''),scope)){node.remove();return false}
 if(node.tagName==='SC-FOR'){const list=resolve(node.getAttribute('list').replace(/[{}]/g,''),scope)||[],name=node.getAttribute('as'),frag=document.createDocumentFragment();for(const item of list)for(const child of [...node.childNodes]){const c=child.cloneNode(true);if(bind(c,{...scope,[name]:item})!==false)frag.append(c)}node.replaceWith(frag);return}
 for(const a of [...node.attributes]){
  if(a.name==='sc-camel-on-click'){const fn=resolve(a.value.replace(/[{}]/g,''),scope);node.addEventListener('click',()=>safe(fn));node.removeAttribute(a.name)}
  else if(a.name==='sc-camel-view-box'){node.setAttribute('viewBox',a.value);node.removeAttribute(a.name)}
  else if(['disabled','checked','selected'].includes(a.name)&&a.value.includes('{{')){node.toggleAttribute(a.name,!!resolve(a.value.replace(/[{}]/g,''),scope))}
  else if(a.value.includes('{{'))node.setAttribute(a.name,a.value.replace(/\{\{(.*?)\}\}/g,(_,v)=>resolve(v,scope)??''));
 }
 for(const child of [...node.childNodes])bind(child,scope);
}
function statusModel(){
 const displayStatus=['off','failed'].includes(S.status)&&S.busy?(S.probing?'checking':'loading'):S.status;
 const needsSub=S.status==='off'&&S.source?.id==='account'&&subscription().link_works===false;
 const route=(S.mode==='tinc'?'Страна выхода: '+countryName(S.assigned||S.country):S.runningLabel||label());
 const models={
  off:needsSub?['warn','lock','Нужна подписка','Продлите подписку в кабинете, чтобы включить VPN.','Нет','Напрямую, без VPN']:['idle','power','VPN выключен','Нажмите кнопку, чтобы подключиться','Нет','Напрямую, без VPN'],
  preparing:['busy','busy','Подготовка…','Готовим сетевой компонент Quantway','Нет','Напрямую, без VPN'],
  loading:['busy','busy','Загружаем маршруты…','Получаем настройки подписки. Подключение станет доступно после загрузки.','Нет','Напрямую, без VPN'],
  checking:['busy','busy','Проверяем маршруты…',S.probeProgress?'Проверено '+S.probeProgress.done+' из '+S.probeProgress.total+'. Выбираем доступный маршрут.':'Проверяем доступность серверов. Сеть Mac пока не изменяется.','Нет','Напрямую, без VPN'],
  authorizing:['busy','lock','Подтвердите запрос macOS','Пароль вводится только в системном окне. Quantway его не видит.','Нет','Напрямую, без VPN'],
  connecting:['busy','busy','Подключение…','Соединение устанавливается. Защита ещё не подтверждена.','Устанавливается','Защита ещё не подтверждена'],
  waiting:['warn','busy',S.errorCode==='capacity_exhausted'?'Ожидаем свободное место':'Ожидаем подключения',S.connectionMessage||'Сервис временно занят. Повторяем запрос.','Нет',S.started?'Не передаются':'Напрямую, без VPN'],
  connected:['ok','power','Защищено',(S.mode==='tinc'?'Mesh (Tinc) · ':'Обычный VPN · ')+route,'Подтверждено','Через VPN'],
  reconnecting:['warn','busy','Переподключение…','Связь с сервером потеряна. Сейчас данные через VPN не передаются.','Потеряно','Не передаются'],
  expired:['err','alert','Доступ истёк','Передача через VPN остановлена. Продлите подписку или обновите доступ в кабинете.','Остановлено','Не передаются'],
  revoked:['err','alert','Доступ отозван','Доступ этого устройства изменён в кабинете. Передача через VPN остановлена.','Остановлено','Не передаются'],
  stopping:['busy','busy','Отключение…','Восстанавливаем сетевые настройки. Подождите пару секунд.','Завершается','Неизвестно до завершения'],
  failed:S.cleanupError?['err','alert','Сеть не восстановлена','Сетевые настройки вернулись не полностью. Интернет может не работать.','Нет','Неизвестно']:['err','alert','Не удалось подключиться',S.lastError||'Проверьте интернет и попробуйте ещё раз.','Нет','Напрямую, без VPN']
 };
 const [tone,icon,title,sub,conn,data]=models[displayStatus]||models.off;
 const actions=[];
 const add=(label,run,primary=false,disabled=false)=>actions.push({label,isLink:false,isBtn:true,go:run,disabled,style:'min-height:44px;padding:8px 18px;border-radius:12px;font:inherit;font-size:14px;font-weight:800;cursor:pointer;'+(primary?'border:0;background:#6FD6C8;color:#072024':'border:1px solid #2A3A48;background:transparent;color:#E8EEF3')});
 if(displayStatus==='loading')add('Загрузка…',null,false,true);
 else if(displayStatus==='checking')add('Отмена',()=>S.status==='checking'?stopConnection():cancelProbe());
 else if(S.status==='off'){if(needsSub)add('Продлить',()=>navigate('cabinet','sub'),true);else{add('Подключить',toggle,true);add('Сменить маршрут',()=>navigate('routes'))}}
 else if(S.status==='connected'){add('Отключить',toggle);if(S.mode==='tinc')add('Состояние сети',meshDialog)}
 else if(['expired','revoked'].includes(S.status)){add('В кабинет',()=>navigate('cabinet','sub'),true);add('Отключить',toggle)}
 else if(S.status==='failed'){if(!S.cleanupError)add('Повторить',toggle,true);add(S.cleanupError?'Инструкция':'Подробности',()=>{S.extra=!S.extra;render()})}
 else if(S.status==='stopping')add('Подождите…',null,false,true);
 else{add(S.status==='waiting'?'Отменить ожидание':'Отмена',toggle);if(S.status==='authorizing')add('Зачем это?',()=>{S.extra=!S.extra;render()})}
 const C={idle:'#93A3B3',busy:'#6FD6C8',ok:'#6FD6C8',warn:'#E8C46A',err:'#F08A7E'}[tone],on=tone==='ok';
 const dot=color=>'width:8px;height:8px;border-radius:50%;flex-shrink:0;background:'+color;
 return {tone,icon,title,sub,actions,mainDisabled:!['off','connected','failed'].includes(displayStatus)||!!S.cleanupError,main:()=>['off','connected','failed'].includes(displayStatus)?toggle():null,mainLabel:displayStatus==='off'?(needsSub?'Открыть подписку':'Подключить VPN'):on?'Отключить VPN':title,icPower:icon==='power',icBusy:icon==='busy',icLock:icon==='lock',icAlert:icon==='alert',titleStyle:'font-size:26px;font-weight:800;color:'+(tone==='idle'?'#E8EEF3':C),ringStyle:'width:200px;height:200px;flex-shrink:0;border-radius:50%;border:0;padding:16px;background:'+(on?'rgba(111,214,200,.12)':'#162230')+';box-shadow:0 0 0 '+(['busy','warn'].includes(tone)?'2px ':'1px ')+(tone==='idle'?'#24384A':C)+(on?',0 0 80px rgba(111,214,200,.25)':''),coreStyle:'width:100%;height:100%;border-radius:50%;display:flex;align-items:center;justify-content:center;'+(on?'background:#6FD6C8;color:#072024':'background:#1F3340;color:'+(tone==='idle'?'#C5D0DA':C)),shortStatus:on?'Подключено':displayStatus==='off'?'Отключено':displayStatus==='failed'?'Ошибка':title,dotStyle:dot(S.status==='off'?'#5C6B78':C),now:[{k:'Соединение',v:conn,dot:dot(on?'#6FD6C8':conn==='Нет'?'#5C6B78':C)},{k:'Данные',v:data,dot:dot(on?'#6FD6C8':data==='Не передаются'||data==='Неизвестно'?'#F08A7E':'#5C6B78')}]};
}
function vals(){
 const model=statusModel();const modes=[['vpn','Обычный VPN'],['tinc','Mesh (Tinc)']].map(([id,label])=>({label,disabled:active()||(id==='tinc'&&S.source?.id!=='account'),style:segment(S.mode===id)+(active()?';cursor:default':''),pick:()=>changeMode(id)}));
 const tabs=W.tabs().map(([id,label])=>({label,style:segment(S.tab===id),pick:()=>navigate('cabinet',id)}));
 const results=new Map(S.results.map(r=>[r.job_id,r]));
 const routes=S.profiles.map(p=>{const r=results.get(p.job_id),sel=S.selected?.job_id===p.job_id;const ok=r?.outcome==='ok',dead=r&&r.outcome!=='ok';return {...p,code:p.country||flagCode(p.label)||'↗',name:p.label,note:ok?'Маршрут доступен':dead?probeReason(r):'Ещё не проверен',proto:[p.protocol,p.transport].filter((v,i,a)=>v&&a.indexOf(v)===i).join(' · '),ping:r?(ok?r.elapsed_ms+' мс':'не прошла'):'—',pingStyle:'font-size:13px;font-weight:700;color:'+(ok?'#6FD6C8':dead?'#F08A7E':'#93A3B3'),rowStyle:'display:grid;grid-template-columns:1fr 160px 120px 110px;gap:12px;align-items:center;padding:10px 16px;border-radius:12px;background:'+(sel?'#13251F':'#111B23')+';border:1px solid '+(sel?'#1F5A52':'#1A2530'),dis:!!dead||S.status==='stopping',btnText:sel?'Выбран':'Выбрать',btnStyle:'min-height:36px;border-radius:9px;font:inherit;font-size:13px;font-weight:700;'+(sel?'background:#6FD6C8;color:#072024;border:0':'background:transparent;color:#E8EEF3;border:1px solid #2A3A48')+(dead?';opacity:.45':''),pick:()=>pickRoute(p),ok}}).filter(r=>(!S.query||[r.name,r.proto].join(' ').toLowerCase().includes(S.query.toLowerCase()))&&(S.filter==='all'||r.ok));
 const chips=[['all','Все'],['ok','Только рабочие']].map(([id,label])=>({label,pick:()=>{S.filter=id;render()},style:'height:42px;padding:0 14px;border-radius:10px;font:inherit;font-size:13px;font-weight:700;'+(S.filter===id?'background:#13403A;color:#6FD6C8;border:1px solid #1F5A52':'background:#131D26;color:#93A3B3;border:1px solid #22323F')}));
 return {...model,showForm:!['restore_running','login_loading','keychain_unavailable','subscription_inactive','no_seats','org_owner_no_vpn','device_unavailable','logout_done'].includes(S.loginPhase),done:false,login,modes,tabs,isSub:S.tab==='sub',isOther:S.tab==='other',routes,chips,check:checkRoutes,checkedText:S.busy?(S.probeProgress?'Проверено '+S.probeProgress.done+' из '+S.probeProgress.total+'…':'Загрузка…'):S.results.length?'Проверено '+S.results.length+' · '+S.results.filter(r=>r.outcome==='ok').length+' работают':'Проверка ещё не выполнялась',route:'Источник: '+(S.source?.name||'Не выбран')+' · '+(S.mode==='tinc'?'Mesh (Tinc) · '+countryName(S.assigned||S.country):'Обычный VPN · '+label()),sourceMenu,subLine:subText(),hasExtra:S.extra,extra:S.cleanupError?'Сетевые настройки восстановлены не полностью. Сохраните работу и перезагрузите Mac. Если интернет не восстановился, обратитесь в поддержку. Не запускайте новое подключение в этом сеансе.':S.status==='authorizing'?'macOS запрашивает разрешение для настройки сетевого интерфейса VPN. Пароль вводится только в системном окне. Отменить можно в любой момент.':(S.errorCode?'Код: '+S.errorCode+'. ':'')+(S.lastError||'Системный помощник настраивает интерфейс, маршруты и DNS только на время подключения.'),stats:[{k:'Загрузка',v:rate('rx'),key:'down'},{k:'Отдача',v:rate('tx'),key:'up'},{k:'Задержка',v:'—',key:'latency'},{k:'В сети',v:connectionTime(),key:'time'}],statsNote:S.status==='connected'?'Скорость — измеренный трафик интерфейса, не тариф. «В сети» — время подтверждённого соединения.':'Метрики появятся после подтверждения соединения и первой выборки.',...tincVals()};
}
// "🇳🇱 AWG - Нидерланды" → "NL": route labels carry the country as a flag only.
function flagCode(text){const m=/^\s*([\u{1F1E6}-\u{1F1FF}])([\u{1F1E6}-\u{1F1FF}])/u.exec(text||'');return m?String.fromCharCode(m[1].codePointAt(0)-0x1F1A5,m[2].codePointAt(0)-0x1F1A5):''}
function rate(k){return S.status==='connected'&&S.traffic?.[k]!=null?(S.traffic[k]*8/1e6).toFixed(1)+' Мбит/с':'—'}
function connectionTime(){if(S.status!=='connected')return'—';const s=Math.max(0,Math.floor((Date.now()-S.started)/1000));return Math.floor(s/3600)+':'+String(Math.floor(s/60)%60).padStart(2,'0')+':'+String(s%60).padStart(2,'0')}
function render(){
 const focus=document.activeElement,focusId=focus?.id,selection=focus?.selectionStart;
 const template=S.page==='routes'&&S.mode==='tinc'?'tinc-routes':S.page;
 // Re-rendering the same view must not throw lists back to the top.
 const app=$('#app'),view=template+(S.page==='cabinet'?':'+S.tab:''),scrolls=app.dataset.view===view?scrollState(app):[];
 const frag=$('template#'+template).content.cloneNode(true);for(const c of [...frag.childNodes])bind(c,vals());app.replaceChildren(frag);app.dataset.view=view;
 $$('button[aria-label="Свернуть"]').forEach(b=>b.onclick=()=>api('minimize'));$$('button[aria-label="Закрыть"]',$('#app')).forEach(b=>b.onclick=requestClose);
 const go={'Home.dc.html':'home','P0-Connect.dc.html':'home','Routes.dc.html':'routes','P0-TincCountries.dc.html':'routes','Cabinet.dc.html':'cabinet'};
 $$('a',$('#app')).forEach(a=>{const h=a.getAttribute('href');if(go[h])a.onclick=e=>{e.preventDefault();if(h==='Routes.dc.html'&&S.mode==='tinc'&&S.page==='routes')safe(()=>changeMode('vpn'));else navigate(go[h])};else if(h==='#')a.onclick=e=>e.preventDefault()});
 updateNav();
 if(S.page==='login')setupLogin();
 if(S.page==='home'){const ring=$('main button[aria-label]');ring?.classList.add('connection-ring');const spinner=statusModel().icBusy&&ring?.querySelector('svg');if(spinner){spinner.classList.add('connection-spinner');spinner.style.animationDelay=-(performance.now()%SPIN_MS)+'ms'}}
 if(S.page==='routes')setupRoutes();
 if(S.page==='cabinet')W.renderCabinet();
 if(focusId&&focus?.closest('#app')){const n=document.getElementById(focusId);if(n){n.focus();if(typeof selection==='number'&&typeof n.setSelectionRange==='function')try{n.setSelectionRange(selection,selection)}catch{}}}
 restoreScroll(app,scrolls);
}
// The spinner element is recreated on every render; a phase taken from a
// shared clock keeps the rotation continuous instead of restarting at 0°.
const SPIN_MS=1200;
function nodePath(e,root){const p=[];while(e&&e!==root){p.unshift([...e.parentNode.children].indexOf(e));e=e.parentNode}return p}
function scrollState(root){return $$('*',root).filter(e=>e.scrollTop||e.scrollLeft).map(e=>[nodePath(e,root),e.scrollTop,e.scrollLeft])}
function restoreScroll(root,list){for(const[p,top,left]of list){let e=root;for(const i of p)e=e?.children[i];if(e){e.scrollTop=top;e.scrollLeft=left}}}
// Probe progress only changes the "Проверено N из M" counter: patch the text
// in place so buttons are not replaced under the cursor 100+ times per check.
function patchProgress(e){let n=0;const w=document.createTreeWalker($('#app'),NodeFilter.SHOW_TEXT);for(let t;(t=w.nextNode());)if(/Проверено \d+ из \d+/.test(t.data)){t.data=t.data.replace(/Проверено \d+ из \d+/,'Проверено '+e.done+' из '+e.total);n++}return n>0}
function updateNav(){const v=statusModel(),nav=$('#app nav');if(!nav)return;const box=nav.lastElementChild;if(box){const spans=$$('span',box);if(spans[0])spans[0].style.cssText=v.dotStyle;if(spans[1])spans[1].textContent=v.shortStatus;if(spans[2])spans[2].textContent=subText()}}
function updateMetrics(){for(const[key,v]of Object.entries({down:rate('rx'),up:rate('tx'),time:connectionTime()}))$$('[data-stat="'+key+'"]').forEach(n=>n.textContent=v)}
function navigate(page,tab){S.page=page;if(tab)S.tab=tab;closePopup();render();if(page==='cabinet')safe(()=>W.loadTab());}
function setupLogin(){
 const input=$('#code');if(input){input.value=S.code;input.disabled=S.loginPhase==='code_checking';input.toggleAttribute('aria-invalid',['code_empty','code_invalid','code_expired'].includes(S.loginPhase));input.oninput=()=>S.code=input.value;input.onkeydown=e=>{if(e.key==='Enter')safe(login)};const b=$('button',input.parentElement);b.disabled=S.loginPhase==='code_checking';b.textContent=b.disabled?'Проверяем…':'Войти'}
 const links=$$('a',$('#app'));const tg=links.find(a=>a.textContent.includes('Telegram'));if(tg){tg.innerHTML='Получить код в Telegram';tg.onclick=e=>{e.preventDefault();safe(()=>open('https://t.me/quantway_bot'))}}
 const other=links.find(a=>a.textContent.includes('другой VPN'));if(other)other.onclick=e=>{e.preventDefault();navigate('cabinet','other')};
 let state=$('#login-state');if(!state){const left=$('#app>div>div:nth-child(2)>div:first-child');state=UI.el('div',{id:'login-state',class:'login-state'});const form=input?.closest('sc-if');if(form)form.after(state);else left?.append(state)}
 const phase=S.loginPhase;state.dataset.designState=phase;
 const messages={code_empty:['err','Введите код. Его выдаёт @quantway_bot или личный кабинет на сайте.'],code_invalid:['err','Код не подошёл. Проверьте символы или получите новый в боте.'],code_expired:['err','Срок действия кода истёк. Получите новый код в боте или кабинете.'],code_checking:['info','Проверяем код…'],offline:['warn','Нет подключения к интернету. Проверьте сеть. Код остался в поле.'],login_loading:['ok','Вход выполнен. Загружаем подписку этого Mac…'],restore_running:['info','Восстанавливаем вход…'],restore_temp_error:['warn','Не удалось связаться с сервером. Данные подписки могут быть устаревшими.'],session_invalid:['err','Сессия больше не действует. Войдите снова. Внешние подписки на этом Mac сохранены.'],keychain_unavailable:['warn','Вход выполнен, но не сохранён в связке ключей macOS. После закрытия приложения нужно будет войти снова.'],subscription_inactive:['warn','Вход выполнен, но подписка не активна. VPN заработает после продления.'],no_seats:['warn','На подписке нет свободных мест для этого Mac. Выберите устройство для замены в кабинете.'],org_owner_no_vpn:['info','Кабинет организации доступен, но личной VPN-подписки нет. Подключение на этом Mac недоступно.'],device_unavailable:['warn',S.session?.device_error||'Доступ этого Mac недоступен. Обновите устройства или проверьте подписку.'],logout_done:['ok','Вы вышли. VPN отключён, сетевые настройки восстановлены. Внешние подписки сохранены.']};
 if(messages[phase])state.append(UI.message(messages[phase][1],messages[phase][0]));
 if(['restore_running','login_loading'].includes(phase))state.append(UI.progress());
 if(phase==='code_checking')state.append(UI.action('Отмена',async()=>{S.loginEpoch++;await api('cancel-login');S.loginPhase='';render()}));
 if(phase==='restore_temp_error')state.append(UI.actions(UI.action('Повторить',restore,'primary'),UI.action('Продолжить',()=>navigate('home'))));
 if(phase==='keychain_unavailable')state.append(UI.actions(UI.action('Повторить сохранение',async()=>{await api('retry-storage');await finishLogin()},'primary'),UI.action('Продолжить',()=>finishLogin(true))));
 if(phase==='subscription_inactive')state.append(UI.actions(UI.action('Продлить',()=>navigate('cabinet','sub'),'primary'),UI.action('Позже',()=>navigate('home'))));
 if(phase==='no_seats')state.append(UI.actions(UI.action('Выбрать устройство для замены',()=>navigate('cabinet','devices'),'primary'),UI.action('Купить место',()=>W.buySeat())));
 if(phase==='logout_done')state.append(UI.action('Войти снова',()=>{S.loginPhase='';render()},'primary'));
 if(phase==='device_unavailable')state.append(UI.actions(UI.action('Обновить доступ',async()=>{const r=await api('account-refresh',{bind_device:true});const d=r.data||{};S.session={...S.session,...d.binding,account:d.account||S.session.account,identity:d.identity||S.session.identity};await finishLogin()},'primary'),UI.action('Устройства',()=>navigate('cabinet','devices'))));
 if(phase==='org_owner_no_vpn')state.append(UI.action('Открыть кабинет',()=>navigate('cabinet','sub'),'primary'));
}
async function login(){
 if(S.loginPhase==='code_checking')return;if(!S.code.trim()){S.loginPhase='code_empty';render();$('#code')?.focus();return}
 const epoch=++S.loginEpoch;S.loginPhase='code_checking';render();
 try{const r=await api('login',{code:S.code.trim()});if(epoch!==S.loginEpoch)return;S.session=r.session;S.code='';S.accountAt=Date.now();if(r.storageWarning){S.loginPhase='keychain_unavailable';render();return}await finishLogin()}catch(e){if(epoch!==S.loginEpoch)return;S.loginPhase=e.code?.includes('expired')?'code_expired':e.status===401||e.status===400?'code_invalid':'offline';render();$('#code')?.focus()}
}
async function finishLogin(skipWarning=false){
 S.loginPhase='login_loading';render();if(S.session?.device_link)await loadSource({id:'account',name:'Quantway',link:S.session.device_link});
 if(S.session?.needs_replacement)S.loginPhase='no_seats';else if(S.session?.identity?.kind==='owner'&&!S.session?.device_link)S.loginPhase='org_owner_no_vpn';else if(subscription().link_works===false)S.loginPhase='subscription_inactive';else if(S.session?.device_error&&!S.session?.device_link)S.loginPhase='device_unavailable';else{S.loginPhase='';S.page='home'}render();
}
async function restore(){S.loginPhase='restore_running';render();try{const r=await api('restore-session');S.session=r.session;S.accountAt=Date.now();if(r.storageWarning){S.loginPhase='keychain_unavailable';render();return}await finishLogin()}catch(e){S.loginPhase=e.status===401?'session_invalid':'restore_temp_error';render()}}
async function refreshAccount(){if(!S.session)return;const r=await api('restore-session');S.session=r.session;S.accountAt=Date.now();S.cache.sub=null;if(S.page==='cabinet')render();}
async function loadSource(source){
 connectEpoch++;if(S.probing)await cancelProbe();
 const epoch=++S.loadEpoch;S.source=source;S.profiles=[];S.results=[];S.selected=null;S.catalog=null;S.sourceError=null;S.catalogError=null;if(source?.id!=='account')S.mode='vpn';if(!source){render();return}S.busy=true;render();
 try{const r=await api('profiles',{link:source.link});if(epoch!==S.loadEpoch)return;S.profiles=r.profiles||[];S.selected=S.profiles[0]||null;S.sourceAt=Date.now();if(source.id==='account')await loadCatalog();}catch(e){if(epoch===S.loadEpoch)S.sourceError=e.message;throw e}finally{if(epoch===S.loadEpoch){S.busy=false;render()}}
}
async function loadCatalog(){if(S.source?.id!=='account')return;try{const r=await api('tinc-catalog',{link:S.source.link});S.catalog=r.data;S.catalogAt=Date.now();S.catalogError=null;const countries=S.catalog?.countries||[];if(!countries.some(c=>c.code===S.country&&c.state==='available'))S.country=countries.find(c=>c.state==='available')?.code||''}catch(e){S.catalogError=e.message;throw e}}
async function changeMode(mode){if(active())return toast('Отключите VPN перед сменой режима.','warn');S.mode=mode;if(S.status==='failed'&&!S.cleanupError){S.status='off';S.lastError='';S.errorCode='';S.extra=false}if(mode==='tinc'&&S.source?.id==='account')await safe(loadCatalog);render()}
async function checkRoutes(forConnection=false){
 if(active()&&!(forConnection&&S.status==='checking'))return toast('Отключите VPN перед проверкой маршрутов.','warn');if(!S.source)return navigate('cabinet','other');if(S.busy)return;
 const source=S.source,mode=S.mode,epoch=++S.probeEpoch;S.busy=true;S.probing=mode==='vpn';S.probeProgress=null;render();
 try{
  if(mode==='tinc')await loadCatalog();
  else{
   const r=await api('probe',{link:source.link});if(epoch!==S.probeEpoch||source!==S.source||mode!==S.mode)return;
   S.results=r.results||[];S.checkedAt=Date.now();
   if(source.id!=='account'){const provider=S.providers.find(p=>p.id===source.id);if(provider){provider.checkedAt=S.checkedAt;await api('save-providers',{providers:S.providers}).catch(()=>{S.providersUnsaved=true})}}
   if(epoch!==S.probeEpoch||source!==S.source||mode!==S.mode)return;
   const working=S.results.filter(r=>r.outcome==='ok'&&r.job),best=working.find(r=>r.job_id===S.selected?.job_id)||[...working].sort((a,b)=>a.elapsed_ms-b.elapsed_ms)[0];
   if(best){let profile=S.profiles.find(p=>p.job_id===best.job_id);if(!profile){profile={job_id:best.job_id,label:best.label||best.job.label||'Доступный маршрут',engine:best.job.engine,protocol:best.job.engine};S.profiles.push(profile)}S.selected=profile}
   if(!forConnection)toast('Проверка маршрутов завершена.');
  }
 }catch(e){if(epoch===S.probeEpoch)throw e}
 finally{if(epoch===S.probeEpoch){S.busy=false;S.probing=false;render()}}
}
async function cancelProbe(){connectEpoch++;const epoch=++S.probeEpoch;try{await api('cancel-probe')}finally{if(epoch===S.probeEpoch){S.busy=false;S.probing=false;S.probeProgress=null;if(S.status==='checking')S.status='off';render()}}}
window.probeEvent=e=>{if(S.probing&&e.type==='progress'){const had=!!S.probeProgress;S.probeProgress=e;if((S.page==='routes'||S.page==='home')&&!(had&&patchProgress(e)))render()}};
async function stopConnection(){
 if(S.status==='checking'){await cancelProbe();return}
 if(!active())return;if(!S.helperRunning){S.status='off';render();return}if(S.stopPromise)return S.stopPromise;
 S.status='stopping';render();S.stopPromise=new Promise((resolve,reject)=>{const timer=setTimeout(()=>{S.stopWait=null;reject(Error('Сетевой помощник ещё завершает работу. Подождите перед новым подключением.'))},20000);S.stopWait={resolve:()=>{clearTimeout(timer);S.cleanupError?reject(Error('Не удалось полностью восстановить сеть.')):resolve()},reject:e=>{clearTimeout(timer);S.stopWait=null;reject(e)}}});
 try{await api('stop');await S.stopPromise}catch(e){S.stopWait?.reject(e);await S.stopPromise.catch(()=>{});throw e}finally{S.stopPromise=null}
}
async function toggle(){if(active())return stopConnection();return startConnection()}
async function startConnection(){
 if(S.busy)return toast('Дождитесь завершения загрузки или отмените проверку маршрутов.','info');if(S.cleanupError)throw Error('Сначала восстановите сетевые настройки по инструкции.');if(!S.source)return sourceMenu();if(S.source.id==='account'&&subscription().link_works===false)return navigate('cabinet','sub');
 const attempt=++connectEpoch,source=S.source,mode=S.mode;
 S.lastError='';S.errorCode='';S.cleanupError=false;S.extra=false;S.started=0;
 try{
  if(mode==='tinc'){if(source.id!=='account')throw Error('Mesh (Tinc) работает только с Quantway.');if(!S.country)throw Error('Выберите доступную страну в маршрутах.')}
  else{
   let r=S.results.find(r=>r.job_id===S.selected?.job_id&&r.outcome==='ok'&&r.job);
   if(!r){S.status='checking';S.probeProgress=null;render();await checkRoutes(true);if(attempt!==connectEpoch||source!==S.source||mode!==S.mode)return;r=S.results.find(r=>r.job_id===S.selected?.job_id&&r.outcome==='ok'&&r.job)}
   if(!r){const counts=new Map();for(const result of S.results){const reason=probeReason(result);counts.set(reason,(counts.get(reason)||0)+1)}const ordered=[...counts].sort((a,b)=>b[1]-a[1]),other=ordered.slice(2).reduce((n,[,count])=>n+count,0),detail=ordered.slice(0,2).map(([reason,count])=>reason+' — '+count).concat(other?'Другие причины — '+other:[]).join('; ');throw Error('Ни один маршрут не прошёл проверку.'+(detail?' '+detail+'.':'')+' Подробности — в разделе «Маршруты».');}
   S.job=r.job;S.control=r.control_url;
  }
  if(attempt!==connectEpoch||source!==S.source||mode!==S.mode)return;
  S.status='authorizing';S.helperRunning=true;render();await api('start',{mode,link:source.link,country:S.country,job:mode==='vpn'?S.job:undefined,control_url:mode==='vpn'?S.control:undefined});
 }catch(e){if(attempt!==connectEpoch)return;S.helperRunning=false;S.status='failed';S.lastError=e.message;render()}
}
async function pickRoute(profile){if(S.selected?.job_id===profile.job_id)return;if(active()){if(!await UI.confirm('routes_switch','Переключиться на другой маршрут?','Текущее соединение «'+(S.runningLabel||label())+'» будет завершено. Затем подключимся через «'+profile.label+'».','Переключиться'))return;await stopConnection();S.selected=profile;await startConnection()}else{S.selected=profile;render()}}
window.sessionEvent=e=>{
 const old=S.status;if(e.code)S.errorCode=e.code;if(e.error){S.lastError=e.error;S.errorCode=e.code||S.errorCode;if(e.code==='network_cleanup_failed')S.cleanupError=true;}
 if(e.status==='ended'){S.helperRunning=false;S.status=S.lastError||e.exitCode!==0?'failed':'off';S.traffic=null;S.mesh=null;S.assigned='';const waiter=S.stopWait;S.stopWait=null;waiter?.resolve();render();return}
 if(e.status==='stopped'){S.status='stopping';return}
 if(e.status==='connected'&&!S.started)S.started=Date.now();if(e.status)S.status=e.status;if(S.mode==='tinc'&&e.status==='connected'&&e.desired_country&&e.country!==e.desired_country)S.status='reconnecting';if(e.country)S.assigned=e.country;if(e.status==='connected'&&S.mode==='vpn')S.runningLabel=S.selected?.label;
 const messages={'Waiting for an ingress':'Устанавливаем защищённую связь со входом.','Waiting for the authorized exit':'Проверяем передачу данных через назначенный выход.','Starting the authorized path':'Запускаем Tinc и внутренний туннель.','Server configuration temporarily unavailable':'Не удалось обновить настройки сервера. Повторяем запрос.'};if(e.message)S.connectionMessage=messages[e.message]||e.message;if(e.retry_after)S.retryAt=Date.now()+e.retry_after*1000;
 if(e.mesh){const previous=S.mesh?.traffic,t=e.mesh.traffic;if(t&&previous&&t.epoch===previous.epoch){const dt=(Date.parse(t.sampled_at)-Date.parse(previous.sampled_at))/1000;if(dt>0)S.traffic={rx:Math.max(0,t.rx_bytes-previous.rx_bytes)/dt,tx:Math.max(0,t.tx_bytes-previous.tx_bytes)/dt}}S.mesh=e.mesh}
 if(old!==S.status)render();else{updateNav();updateMetrics();if(S.page==='routes'&&S.mode==='tinc')render()}
 if($('#mesh-layer'))updateMeshPanel();
};
function setupRoutes(){if(S.mode==='tinc'){
 const emptyLink=$('main .q-empty-state a');if(emptyLink)emptyLink.onclick=e=>{e.preventDefault();safe(loadCatalog)};
 const links=$$('main a');for(const a of links)if(a.getAttribute('href')==='#'){a.onclick=e=>{e.preventDefault();safe(async()=>{S.busy=true;render();try{await loadCatalog()}finally{S.busy=false;render()}})}}
 return}
 const input=$('main input');if(input){input.value=S.query;input.id='route-search';input.oninput=()=>{S.query=input.value;render()}}$$('main span').filter(e=>e.textContent==='Пинг').forEach(e=>e.textContent='Проверка');
 const main=$('main'),button=$$('button',main).find(b=>b.textContent==='Проверить все');if(button&&S.busy){button.textContent='Отменить проверку';button.onclick=()=>safe(cancelProbe)}
 if(!vals().routes.length){const list=main.lastElementChild;list.append(UI.empty(S.query?'Ничего не найдено':'Маршрутов пока нет',S.query?'По вашему запросу нет маршрутов.':'Обновите подписку или добавьте другую.',S.query?'Сбросить поиск':'Обновить подписку',()=>S.query?(S.query='',render()):loadSource(S.source)))}
 $$('main .q-route-name').forEach(n=>n.title=n.textContent);
}
function tincVals(){const codes=S.catalog?.countries||[],valid=S.source?.id==='account',noSub=valid&&subscription().link_works===false;
 const state=!valid?'noQuantway':noSub?'noSub':S.busy&&!S.catalog?'loading':S.catalogError?'error':codes.length?'list':'empty';const connected=S.status==='connected';const target=countryName(S.country),assigned=countryName(S.assigned||S.country);
 const messages={noQuantway:['Mesh (Tinc) работает только с Quantway','Переключите источник на Quantway, чтобы выбрать страну выхода.'],noSub:['Нужна активная подписка Quantway','Продлите подписку в кабинете.'],empty:['Стран пока нет','В вашей подписке пока нет доступных стран для Mesh (Tinc).'],error:['Не удалось загрузить страны','Проверьте интернет и повторите. Ранее выбранная страна сохранена.']};
 return {rows:codes.map(c=>{const sel=S.country===c.code,isAs=connected&&S.assigned===c.code,ok=c.state==='available',prep=c.state==='preparing',disabled=!ok||S.status==='stopping';return {code:c.code,name:c.name||countryName(c.code),note:isAs&&sel?'Выбрана и назначена':isAs?'Назначена сервером':sel?'Выбрана для подключения':prep?'Сервер готовит выход, попробуйте позже':ok?'Из вашей подписки':'Выход сейчас не работает',st:ok?'Доступна':prep?'Готовится':'Недоступна',check:'—',dis:disabled,stStyle:'font-size:13px;font-weight:700;color:'+(ok?'#6FD6C8':prep?'#E8C46A':'#F08A7E'),btn:sel?'Выбрана':'Выбрать',btnStyle:'height:36px;border-radius:9px;font:inherit;font-size:13px;font-weight:700;'+(sel?'background:#6FD6C8;color:#072024;border:0':'background:transparent;color:'+(disabled?'#5C6B78':'#E8EEF3')+';border:1px solid #2A3A48'),rowStyle:'display:grid;grid-template-columns:1fr 150px 130px 130px;gap:12px;align-items:center;padding:10px 16px;border-radius:12px;background:'+(sel?'#13251F':'#111B23')+';border:1px solid '+(sel?'#1F5A52':'#1A2530'),pick:()=>pickCountry(c.code)}}),skel:[1,2,3,4,5],isList:state==='list',isLoading:state==='loading',isEmpty:!['list','loading'].includes(state),emptyTitle:messages[state]?.[0]||'',emptyText:messages[state]?.[1]||'',emptyActs:[{label:state==='noQuantway'?'Сменить источник':state==='noSub'?'В кабинет':'Обновить список',href:state==='noQuantway'?'P0-Connect.dc.html':state==='noSub'?'Cabinet.dc.html':'#',style:'min-height:44px;padding:10px 18px;display:inline-flex;align-items:center;border-radius:12px;text-decoration:none;font-size:14px;font-weight:800;background:#6FD6C8;color:#072024'}],subtitle:state==='loading'?'Первая загрузка':'Страны выхода из вашей подписки',hasBanner:state==='list',bannerTitle:connected?'Подключено · назначена: '+assigned:'Выбрана: '+target,bannerText:connected?(S.country===S.assigned?'Сервер назначил выбранную страну.':'Выбрана «'+target+'», назначена «'+assigned+'».'):'Фактическую страну выхода назначит сервер после подключения.',bannerStyle:'display:flex;gap:12px;padding:12px 16px;border-radius:12px;background:'+(connected?'#13251F':'#111B23')+';border:1px solid '+(connected?'#1F5A52':'#1A2530'),confirm:!!S.switchCountry,target:countryName(S.switchCountry),current:assigned,cancel:()=>{S.switchCountry=null;render()},doSwitch:async()=>{const code=S.switchCountry;S.switchCountry=null;await api('country',{country:code});S.country=code;S.status='reconnecting';S.connectionMessage='Готовим выход в выбранной стране.';render()}};
}
function pickCountry(code){if(code===S.country)return;if(active()){S.switchCountry=code;render();requestAnimationFrame(()=>{const d=$('[role=dialog]',$('#app'));if(d){d.setAttribute('aria-modal','true');d.querySelector('button')?.focus()}})}else{S.country=code;render()}}
function closePopup(){document.querySelector('#source-popup')?.remove()}
function sourceMenu(){closePopup();const anchor=$('#source-trigger'),r=anchor?.getBoundingClientRect(),p=UI.el('div',{id:'source-popup',class:'q-card q-source-popup',role:'dialog','aria-label':'Источник VPN',style:'top:'+Math.min((r?.bottom||80)+8,innerHeight-300)+'px;left:'+Math.max(24,Math.min(r?.left||260,innerWidth-384))+'px'});
 const sources=[...(S.session?.device_link?[{id:'account',name:'Quantway',link:S.session.device_link}]:[]),...S.providers];p.append(UI.el('h2',{text:'Источник VPN'}),UI.list(sources.map(s=>UI.row(s.name,s.id==='account'?'Аккаунт Quantway':'Внешняя подписка',S.source?.id===s.id?'Активен':'',S.source?.id===s.id,'ok',async()=>{closePopup();if(s.id===S.source?.id)return;if(active()){if(!await UI.confirm('source_switch_confirm','Сменить источник?','Текущее соединение через «'+S.source.name+'» будет завершено. Затем подключимся через «'+s.name+'».','Сменить и подключить'))return;await stopConnection();await loadSource(s);await startConnection()}else await loadSource(s)}))),UI.actions(UI.action('Добавить подписку',()=>navigate('cabinet','other')),UI.action('Управлять',()=>navigate('cabinet','other'))));document.body.append(p);p.querySelector('button')?.focus();}
document.addEventListener('pointerdown',e=>{if($('#source-popup')&&!e.target.closest('#source-popup,#source-trigger'))closePopup()});
function meshDialog(){UI.closeDialog();$('#mesh-layer')?.remove();const layer=UI.el('div',{id:'mesh-layer',class:'q-panel-layer'});document.body.append(layer);updateMeshPanel();layer.querySelector('aside')?.focus()}
function updateMeshPanel(){const layer=$('#mesh-layer');if(!layer)return;const m=S.mesh,age=m?.observed_at?(Date.now()-Date.parse(m.observed_at))/1000:null,stale=age!=null&&age>60,frag=$('#mesh-panel').content.cloneNode(true);const wasFocused=layer.contains(document.activeElement),close=()=>{layer.remove();$('#source-trigger')?.focus()};const fields={isOpen:true,close,meshAge:age==null?'Нет данных':stale?'Устарело':Math.max(0,Math.floor(age))+' с назад',meshCountry:countryName(m?.egress?.country||S.assigned),meshAssigned:m?.egress?.country?'назначена сервером':'Нет данных',meshVolume:m?.traffic?'↓ '+UI.bytes(m.traffic.rx_bytes)+' · ↑ '+UI.bytes(m.traffic.tx_bytes):'Нет данных',entries:(m?.ingresses||[]).map(p=>({name:p.name,st:stale?'Устарело':p.connected===true?'Подтверждено':p.connected===false?'Нет связи':'Нет данных',stStyle:'font-size:11px;font-weight:700;color:'+(stale?'#E8C46A':p.connected?'#6FD6C8':'#93A3B3'),seen:age==null?'—':Math.max(0,Math.floor(age))+' с назад',seenStyle:'color:#93A3B3',vol:'—',speed:'—'}))};for(const n of [...frag.childNodes])bind(n,fields);layer.replaceChildren(frag);layer.firstElementChild?.addEventListener('click',close);if(wasFocused)layer.querySelector('aside button')?.focus()}
window.lifecycleAction=action=>action==='quit'?requestQuit():requestClose();
async function requestClose(){if(!active())return api('close');if(S.closeToTray)return api('hide-to-tray');UI.modal('close_active','Закрыть окно?',r=>{r.append(UI.message('VPN продолжит работать, управление останется в строке меню.','warn'));const remember=UI.el('label',{class:'muted'},UI.el('input',{type:'checkbox',id:'remember-close'}),' Больше не спрашивать');r.append(remember,UI.actions(UI.action('Отключить и выйти',()=>api('quit')),UI.action('Оставить в строке меню',async()=>{S.closeToTray=$('#remember-close').checked;await api('hide-to-tray',{remember:S.closeToTray});UI.closeDialog()},'primary')))});}
async function requestQuit(){if(!active())return api('quit');if(await UI.confirm('quit_active','Выйти из Quantway?','VPN будет отключён, сетевые настройки восстановлены.','Отключить и выйти'))await api('quit')}
window.accountAction=key=>safe(()=>W.accountAction(key));
window.appBecameActive=()=>safe(()=>W.onForeground());
document.addEventListener('keydown',e=>{if(e.key==='Escape'){closePopup();$('#mesh-layer')?.remove();if(S.switchCountry){S.switchCountry=null;render()}}const modal=$('[role=dialog][aria-modal=true]');if(e.key==='Tab'&&modal&&!$('#dialog')){const nodes=$$('button:not(:disabled),a,[tabindex="0"]',modal),first=nodes[0],last=nodes.at(-1);if(e.shiftKey&&document.activeElement===first){last?.focus();e.preventDefault()}else if(!e.shiftKey&&document.activeElement===last){first?.focus();e.preventDefault()}}});
setInterval(()=>{updateMetrics();if($('#mesh-layer'))updateMeshPanel()},1000);
async function bootstrap(){render();try{const b=await api('bootstrap');S.providers=b.providers||[];S.closeToTray=b.closeToTray===true;if(b.legacyLink&&!S.providers.length){S.providers=[{id:'legacy',name:'Сохранённая подписка',link:b.legacyLink}];await api('save-providers',{providers:S.providers})}if(b.hasSession)await restore();else{S.loginPhase='';render()}}catch(e){S.loginPhase='';render();toast(e.message,'err')}}
bootstrap();
