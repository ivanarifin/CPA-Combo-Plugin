package main

const comboDashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>CPA Combo Router</title>
<style>
:root{
  color-scheme:dark;
  --bg:#0e120f;--panel:#161c18;--panel-2:#1b231e;--inset:#0f1411;
  --line:#2c362f;--line-strong:#425046;
  --text:#edf3ed;--muted:#a6b4a9;--faint:#76857a;
  --lime:#b8f36b;--lime-dim:#8cc942;--lime-wash:#b8f36b14;
  --red:#ff9c8c;--red-wash:#ff8e7d14;--amber:#f2c66d;--amber-wash:#f2c66d12;
  --mono:ui-monospace,"SF Mono",SFMono-Regular,"JetBrains Mono",Menlo,Consolas,monospace;
  --sans:ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;
  font:14px/1.5 var(--sans);
}
*{box-sizing:border-box}
html,body{margin:0;min-height:100%}
body{
  color:var(--text);
  background:
    linear-gradient(#ffffff05 1px,transparent 1px) 0 0/100% 28px,
    linear-gradient(90deg,#ffffff05 1px,transparent 1px) 0 0/28px 100%,
    radial-gradient(ellipse 70% 40% at 85% -8%,#2c4426 0,transparent 70%),
    var(--bg);
}
button,input,select,textarea{font:inherit;color:inherit}
button{cursor:pointer}
h1,h2,h3,p{margin:0}
:focus-visible{outline:2px solid var(--lime);outline-offset:2px}
.mono{font-family:var(--mono)}

.shell{max-width:1200px;margin:0 auto;padding:20px 24px 28px}
.top{display:flex;align-items:center;justify-content:space-between;gap:12px 20px;flex-wrap:wrap;padding-bottom:16px;border-bottom:1px solid var(--line)}
.brand{display:flex;align-items:center;gap:12px;min-width:0}
.mark{width:34px;height:34px;flex:none;border-radius:9px;background:var(--lime);color:#17200f;display:grid;place-items:center;font-weight:900;font-size:17px}
.brand h1{font-size:15px;letter-spacing:-.01em;line-height:1.2}
.brand small{display:block;color:var(--faint);font-size:12px}
.top-right{display:flex;align-items:center;gap:10px 18px;flex-wrap:wrap}
.status{display:flex;align-items:center;gap:8px;color:var(--muted);font-size:12px}
.dot{width:8px;height:8px;border-radius:50%;background:var(--faint);flex:none}
.status[data-state=ok] .dot{background:var(--lime);box-shadow:0 0 10px #b8f36b80}
.status[data-state=busy] .dot{background:var(--amber);animation:pulse 1.1s ease-in-out infinite}
.status[data-state=error] .dot{background:var(--red)}
@keyframes pulse{50%{opacity:.35}}

.switch{display:inline-flex;align-items:center;gap:9px;font-size:13px;cursor:pointer;user-select:none}
.switch input{position:absolute;opacity:0;width:1px;height:1px}
.switch i{width:34px;height:20px;border-radius:99px;background:#2a332d;border:1px solid var(--line-strong);position:relative;flex:none;transition:background .15s,border-color .15s}
.switch i:after{content:"";position:absolute;top:2px;left:2px;width:14px;height:14px;border-radius:50%;background:var(--muted);transition:transform .15s,background .15s}
.switch input:checked+i{background:var(--lime-wash);border-color:var(--lime-dim)}
.switch input:checked+i:after{transform:translateX(14px);background:var(--lime)}
.switch input:focus-visible+i{outline:2px solid var(--lime);outline-offset:2px}

.banner{margin-top:14px;padding:9px 13px;border:1px solid #6a5a30;background:var(--amber-wash);color:var(--amber);border-radius:9px;font-size:13px}

.layout{display:grid;grid-template-columns:264px minmax(0,1fr);gap:20px;padding-top:18px;align-items:start}
.rail{position:sticky;top:14px;display:grid;gap:12px;min-width:0}
.card{border:1px solid var(--line);background:var(--panel);border-radius:12px}
.rail-head{display:flex;align-items:center;justify-content:space-between;padding:12px 14px 8px}
.rail-head h2,.section-title{font-size:12px;letter-spacing:.09em;text-transform:uppercase;color:var(--muted);font-weight:700}
.count{font:12px var(--mono);color:var(--lime)}
.alias-list{display:grid;gap:2px;padding:0 6px 8px;max-height:min(52vh,560px);overflow:auto}
.alias{display:flex;align-items:center;gap:8px;width:100%;min-height:36px;border:1px solid transparent;background:transparent;border-radius:8px;text-align:left;padding:6px 8px}
.alias:hover{background:#ffffff09}
.alias[aria-current=true]{background:var(--lime-wash);border-color:#b8f36b40}
.tag{flex:none;width:24px;text-align:center;font:700 10px/18px var(--mono);border-radius:4px;color:var(--muted);background:#ffffff0b}
.alias[aria-current=true] .tag{color:var(--lime);background:#b8f36b1c}
.alias-name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font:13px var(--mono);min-width:0}
.alias-meta{margin-left:auto;display:flex;align-items:center;gap:6px;color:var(--faint);font:12px var(--mono);flex:none}
.flag{width:7px;height:7px;border-radius:50%}
.flag.error{background:var(--red)}
.flag.warn{background:var(--amber)}
.alias-form{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:6px;padding:8px;border-top:1px solid var(--line)}
.rail-link{display:flex;align-items:center;justify-content:space-between;gap:8px;width:100%;min-height:40px;padding:8px 14px;background:var(--panel);border:1px solid var(--line);border-radius:12px;text-align:left;font-size:13px}
.rail-link:hover{border-color:var(--line-strong)}
.rail-link[aria-current=true]{border-color:#b8f36b40;background:var(--lime-wash)}
.rail-link span:last-child{color:var(--faint);font-size:12px}

input[type=text],input[type=password],input[type=number],input:not([type]),select,textarea{
  width:100%;min-width:0;min-height:36px;padding:7px 10px;border:1px solid #344039;border-radius:8px;background:var(--inset);outline:none;font-size:13px
}
textarea{resize:vertical;font-family:var(--mono);line-height:1.5}
input::placeholder,textarea::placeholder{color:var(--faint)}
input:focus,select:focus,textarea:focus{border-color:var(--lime-dim);box-shadow:0 0 0 3px #b8f36b1a}
[aria-invalid=true]{border-color:var(--red)!important;box-shadow:0 0 0 3px var(--red-wash)!important}
.model-input{font-family:var(--mono)}

.btn{min-height:36px;padding:6px 12px;border:1px solid var(--line-strong);border-radius:8px;background:#232c26;font-size:13px;font-weight:600;white-space:nowrap;transition:background .12s,border-color .12s}
.btn:hover:not(:disabled){background:#2c3730;border-color:#5f7265}
.btn:disabled{opacity:.5;cursor:not-allowed}
.btn.primary{background:var(--lime);border-color:var(--lime);color:#18210f;font-weight:750}
.btn.primary:hover:not(:disabled){background:#c9ff83;border-color:#c9ff83}
.btn.ghost{background:transparent}
.btn.danger{color:var(--red)}
.btn.danger:hover:not(:disabled){border-color:#8b4e47;background:var(--red-wash)}
.btn.small{min-height:28px;padding:2px 9px;font-size:12px}
.linkish{border:0;background:none;padding:0;color:var(--lime);font-size:12px;text-decoration:underline;text-underline-offset:2px}

.work{display:grid;gap:14px;min-width:0;animation:rise .22s ease-out}
@keyframes rise{from{opacity:0;transform:translateY(4px)}}
.head{padding:16px 18px}
.head-line{display:flex;align-items:flex-start;justify-content:space-between;gap:10px 16px;flex-wrap:wrap}
.head h2{font:600 21px/1.25 var(--mono);letter-spacing:-.02em;overflow-wrap:anywhere}
.head-actions{display:flex;gap:6px;flex:none}
.route{margin-top:12px;padding:9px 12px;border-radius:8px;background:var(--inset);border:1px solid var(--line);font:13px/1.7 var(--mono);overflow-wrap:anywhere}
.route b{font-weight:500;color:var(--lime)}
.route i{font-style:normal;color:var(--faint);padding:0 6px}
.route .none{color:var(--red)}
.route small{display:block;color:var(--muted);font:12px/1.5 var(--sans)}

.checks{display:grid;gap:6px;margin-top:10px}
.checks:empty{display:none;margin:0}
.check{display:flex;align-items:baseline;justify-content:space-between;gap:12px;padding:7px 11px;border-radius:8px;font-size:13px;border:1px solid}
.check.warn{color:var(--amber);background:var(--amber-wash);border-color:#5e5130}
.check.error{color:var(--red);background:var(--red-wash);border-color:#6d423b}

.section{padding:15px 18px 17px}
.section-head{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;margin-bottom:11px}
.section-note{color:var(--muted);font-size:12px}
.help{color:var(--muted);font-size:12px;margin-top:9px}

.strategies{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:7px}
.strategy{position:relative;display:block;padding:9px 11px;border:1px solid var(--line);border-radius:9px;background:var(--panel-2);cursor:pointer}
.strategy:hover{border-color:var(--line-strong)}
.strategy input{position:absolute;opacity:0;inset:0;width:100%;height:100%;margin:0;cursor:pointer}
.strategy strong{display:block;font-size:13px;font-weight:650}
.strategy span{display:block;color:var(--muted);font-size:12px;line-height:1.4;margin-top:2px}
.strategy:has(input:checked){border-color:var(--lime-dim);background:var(--lime-wash)}
.strategy:has(input:checked) strong{color:var(--lime)}
.strategy:has(input:focus-visible){outline:2px solid var(--lime);outline-offset:2px}

.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(210px,1fr));gap:12px;margin-top:13px}
.grid .wide{grid-column:1/-1}
.field{display:grid;gap:4px;align-content:start;min-width:0}
.field label{font-size:12px;font-weight:600;color:var(--muted)}
.field small{font-size:12px;color:var(--faint);line-height:1.4}
.toggle{display:flex;align-items:center;gap:9px;font-size:13px;cursor:pointer;min-height:36px}
.toggle input{accent-color:var(--lime);width:16px;height:16px;flex:none;margin:0}

.rows{display:grid;gap:7px}
.row{position:relative;display:grid;grid-template-columns:30px minmax(0,1fr) auto;gap:10px;align-items:start;padding:9px 10px;border:1px solid var(--line);border-radius:10px;background:var(--panel-2)}
.order{position:relative;width:30px;height:36px;display:grid;place-items:center;border-radius:7px;color:var(--lime);background:var(--lime-wash);font:12px var(--mono)}
.rows[data-flow=sequence] .row:not(:last-child) .order:after{content:"↓";position:absolute;left:0;right:0;top:38px;text-align:center;color:var(--faint);font-size:12px;line-height:1}
.rows[data-flow=parallel] .order{color:var(--text);background:#ffffff10}
.row-fields{display:grid;gap:4px;min-width:0}
.row details summary{cursor:pointer;color:var(--faint);font-size:12px;padding:2px 0;width:fit-content}
.row details summary:hover{color:var(--muted)}
.row details[open] summary{margin-bottom:6px}
.pair{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:8px}
.row-actions{display:flex;gap:3px}
.icon{width:32px;height:36px;border:1px solid var(--line);background:transparent;color:var(--muted);border-radius:7px;font-size:14px}
.icon:hover:not(:disabled){color:var(--text);border-color:#6d7f72}
.icon:disabled{opacity:.3;cursor:not-allowed}
.icon.remove:hover:not(:disabled){color:var(--red);border-color:#8b4e47}
.add{margin-top:8px;width:100%;border-style:dashed;background:transparent;color:var(--muted)}
.add:hover:not(:disabled){color:var(--text)}
.inner-empty{padding:18px;text-align:center;color:var(--muted);font-size:13px;border:1px dashed var(--line-strong);border-radius:10px}

.empty{padding:44px 24px;text-align:center}
.empty strong{display:block;font-size:16px;margin-bottom:6px}
.empty p{color:var(--muted);max-width:420px;margin:0 auto 14px}

.savebar{position:sticky;bottom:12px;z-index:3;display:flex;align-items:center;justify-content:space-between;gap:10px 14px;flex-wrap:wrap;padding:10px 14px;background:#171e19f5;backdrop-filter:blur(10px);border:1px solid var(--line-strong);border-radius:12px;box-shadow:0 10px 34px #0007;margin-top:14px}
.savebar[data-kind=error]{border-color:#8b4e47}
.savebar[data-kind=success]{border-color:var(--lime-dim)}
.message{font-size:13px;min-width:0;flex:1 1 240px}
.savebar[data-kind=error] .message{color:var(--red)}
.savebar[data-kind=success] .message{color:var(--lime)}
.save-actions{display:flex;gap:8px;flex:none;margin-left:auto}

.hide{display:none!important}
.gate{position:fixed;inset:0;z-index:5;display:none;place-items:center;padding:20px;background:#090d0be0;backdrop-filter:blur(6px)}
.gate.open{display:grid}
.gate-card{width:min(100%,420px);padding:22px}
.gate-card h2{font-size:19px;margin-bottom:6px}
.gate-card p{color:var(--muted);font-size:13px;margin-bottom:13px}
.gate-error{color:var(--red);font-size:13px;margin-top:8px}
.gate-actions{display:flex;justify-content:flex-end;margin-top:12px}
dialog{width:min(calc(100% - 32px),430px);padding:20px;color:var(--text);background:var(--panel);border:1px solid var(--line-strong);border-radius:13px;box-shadow:0 20px 60px #000a}
dialog::backdrop{background:#090d0bcc}
dialog h2{font-size:17px;margin-bottom:6px}
dialog p{color:var(--muted);font-size:13px;margin-bottom:12px;overflow-wrap:anywhere}
.dialog-error{color:var(--red);font-size:13px;margin-top:7px;min-height:18px}
.dialog-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:10px}

@media(max-width:860px){
  .shell{padding:14px 12px 20px}
  .layout{grid-template-columns:1fr;gap:12px}
  .rail{position:static}
  .alias-list{max-height:220px}
  .strategies{grid-template-columns:repeat(2,minmax(0,1fr))}
  .row{grid-template-columns:26px minmax(0,1fr);row-gap:6px}
  .order{width:26px}
  .row-actions{grid-column:2;justify-content:flex-end}
  .icon{width:40px;height:38px}
  .rows[data-flow=sequence] .row:not(:last-child) .order:after{display:none}
}
@media(max-width:460px){.strategies{grid-template-columns:1fr}.head-actions{width:100%}.head-actions .btn{flex:1}}
@media(prefers-reduced-motion:reduce){*,*:before,*:after{animation:none!important;transition:none!important}}
</style>
</head>
<body>
<div class="shell">
  <header class="top">
    <div class="brand"><div class="mark" aria-hidden="true">↗</div><div><h1>Combo Router</h1><small>CLIProxyAPI plugin</small></div></div>
    <div class="top-right">
      <label class="switch"><input type="checkbox" id="enabled"><i aria-hidden="true"></i><span>Combo routing</span></label>
      <div class="status" id="status" data-state="busy"><span class="dot" aria-hidden="true"></span><span id="statusText" role="status">Connecting to CPA…</span></div>
    </div>
  </header>
  <div class="banner hide" id="disabledBanner">Combo routing is off. These aliases are not served until you turn it on and save.</div>
  <main class="layout">
    <nav class="rail" aria-label="Combo aliases">
      <div class="card">
        <div class="rail-head"><h2>Model aliases</h2><span class="count" id="aliasCount">0</span></div>
        <div class="alias-list" id="aliasList"></div>
        <form class="alias-form" id="aliasForm" novalidate><input id="newAlias" type="text" autocomplete="off" spellcheck="false" class="mono" placeholder="combo-fast" aria-label="New combo alias"><button class="btn" type="submit">Add</button></form>
      </div>
      <button class="rail-link" id="settingsLink" type="button"><span>Global settings</span><span id="settingsMeta"></span></button>
    </nav>
    <div>
      <section class="work" id="main"><div class="card empty"><p>Loading saved combos…</p></div></section>
      <footer class="savebar hide" id="savebar"><div class="message" id="message" role="status" aria-live="polite"></div><div class="save-actions" id="saveActions"><button class="btn ghost" id="discardBtn" type="button">Discard edits</button><button class="btn primary" id="saveBtn" type="button">Save combo settings</button></div></footer>
    </div>
  </main>
</div>
<div class="gate" id="gate" role="dialog" aria-modal="true" aria-labelledby="gateTitle"><div class="gate-card card"><h2 id="gateTitle">Connect to your CPA</h2><p>Enter the CPA management key to load and save plugin settings. It stays in this page only.</p><form id="keyForm" novalidate><input id="managementKey" type="password" autocomplete="current-password" placeholder="CPA management key" aria-label="CPA management key" aria-describedby="gateError"><div class="gate-error hide" id="gateError" role="alert"></div><div class="gate-actions"><button class="btn primary" type="submit">Connect</button></div></form></div></div>
<dialog id="dialog" aria-labelledby="dialogTitle"><form id="dialogForm" method="dialog" novalidate><h2 id="dialogTitle"></h2><p id="dialogText"></p><input id="dialogInput" type="text" class="mono hide" autocomplete="off" spellcheck="false" aria-labelledby="dialogTitle"><div class="dialog-error hide" id="dialogError" role="alert"></div><div class="dialog-actions"><button class="btn ghost" id="dialogCancel" type="button">Cancel</button><button class="btn primary" id="dialogConfirm" type="submit">Confirm</button></div></form></dialog>
<datalist id="modelCatalog"></datalist>

<script>
(()=>{'use strict';
const PLUGIN='cpa-combo-router',CLIENT_KEY_STORAGE='cpa-combo-router.client-api-key',DEFAULT_RETRIES=[403,429,500,502,503,504],DEFAULT_ATTEMPTS=3,REQUEST_TIMEOUT_MS=30000;
const STRATEGIES=[
  {id:'fallback',label:'Fallback',tag:'FB',blurb:'Try models in order until one works.'},
  {id:'round-robin',label:'Round-robin',tag:'RR',blurb:'Rotate the starting model to spread load.'},
  {id:'smart',label:'Smart selection',tag:'SM',blurb:'A small model picks the best fit.'},
  {id:'fusion',label:'Fusion + judge',tag:'FX',blurb:'All answer, a judge merges. Costs more.'}
];
const PROTOCOLS=[['','Use incoming protocol'],['openai','OpenAI Chat Completions'],['openai-response','OpenAI Responses'],['claude','Claude'],['gemini','Gemini'],['codex','Codex'],['antigravity','Antigravity'],['interactions','Interactions']];

const byId=id=>document.getElementById(id);
const ui={main:byId('main'),aliases:byId('aliasList'),aliasCount:byId('aliasCount'),aliasForm:byId('aliasForm'),newAlias:byId('newAlias'),settingsLink:byId('settingsLink'),settingsMeta:byId('settingsMeta'),enabled:byId('enabled'),banner:byId('disabledBanner'),status:byId('status'),statusText:byId('statusText'),savebar:byId('savebar'),message:byId('message'),saveActions:byId('saveActions'),save:byId('saveBtn'),discard:byId('discardBtn'),gate:byId('gate'),gateError:byId('gateError'),key:byId('managementKey'),catalog:byId('modelCatalog'),dialog:byId('dialog')};

let config={},chains={},strategies={},selected='',view='alias';
let managementKey='',clientAPIKey='',loadedRaw='',savedState='';
let hasLoaded=false,dirty=false,saving=false,saveAttempted=false;
let retryText=DEFAULT_RETRIES.join(', '),attemptsText=String(DEFAULT_ATTEMPTS),firstByteText='',attemptTimeoutText='';
let availableModels=[],modelsRequest=0,modelStatusText='Client API key needed',modelStatusError=false;
let fieldSequence=0,messageTimer=0;
const openAdvanced=new WeakSet();

class AuthError extends Error{}

// ---- helpers ----------------------------------------------------------------
function h(tag,props,...children){
  const el=document.createElement(tag);
  for(const [key,value] of Object.entries(props||{})){
    if(value===undefined||value===null||value===false)continue;
    if(key==='class')el.className=value;
    else if(key==='text')el.textContent=value;
    else if(key.startsWith('on'))el.addEventListener(key.slice(2),value);
    else if(key==='for')el.htmlFor=value;
    else if(key.startsWith('data-')||key.startsWith('aria-')||key==='role'||key==='list'||key==='title')el.setAttribute(key,String(value));
    else el[key]=value;
  }
  for(const child of children.flat()){if(child!==null&&child!==undefined&&child!==false)el.append(child)}
  return el;
}
const own=(object,key)=>Object.prototype.hasOwnProperty.call(object,key);
const plural=(n,word)=>n+' '+word+(n===1?'':'s');
function canonical(value){
  if(Array.isArray(value))return '['+value.map(canonical).join(',')+']';
  if(value&&typeof value==='object')return '{'+Object.keys(value).sort().map(key=>JSON.stringify(key)+':'+canonical(value[key])).join(',')+'}';
  return JSON.stringify(value===undefined?null:value);
}
function setStatus(state,text){ui.status.dataset.state=state;ui.statusText.textContent=text}
function setMessage(text,kind=''){
  clearTimeout(messageTimer);
  ui.message.textContent=text;
  ui.savebar.dataset.kind=kind;
  updateSavebar();
  if(kind==='success')messageTimer=setTimeout(()=>{if(ui.savebar.dataset.kind==='success')setMessage('')},6000);
}
function updateSavebar(){
  const text=ui.message.textContent;
  if(dirty&&!text)ui.message.textContent='Unsaved changes.';
  if(!dirty&&text==='Unsaved changes.')ui.message.textContent='';
  ui.savebar.classList.toggle('hide',!dirty&&!ui.message.textContent);
  ui.saveActions.classList.toggle('hide',!dirty);
  ui.save.disabled=saving;
  ui.discard.disabled=saving;
  ui.save.textContent=saving?'Saving…':'Save combo settings';
}

// ---- storage and network ----------------------------------------------------
function discoverBase(){const path=location.pathname;const marker='/v0/resource/plugins/';const i=path.lastIndexOf(marker);return i>=0?location.origin+path.slice(0,i):location.origin}
const base=discoverBase();
function decodeStorage(value){if(!value||!value.startsWith('enc::v1::'))return value;try{const raw=atob(value.slice(9)),key=new TextEncoder().encode('cli-proxy-api-webui::secure-storage|'+location.host+'|'+navigator.userAgent),bytes=new Uint8Array(raw.length);for(let i=0;i<raw.length;i++)bytes[i]=raw.charCodeAt(i)^key[i%key.length];return new TextDecoder().decode(bytes)}catch{return ''}}
function rememberedKey(){for(const name of ['cli-proxy-auth','managementKey']){try{const value=localStorage.getItem(name);if(!value)continue;let parsed=JSON.parse(decodeStorage(value));if(typeof parsed==='string')parsed=JSON.parse(parsed);const key=parsed?.state?.managementKey||parsed?.managementKey;if(typeof key==='string'&&key.trim())return key.trim()}catch{}}return ''}
function readClientAPIKey(){try{return sessionStorage.getItem(CLIENT_KEY_STORAGE)||''}catch{return ''}}
function rememberClientAPIKey(value){clientAPIKey=value;try{if(value)sessionStorage.setItem(CLIENT_KEY_STORAGE,value);else sessionStorage.removeItem(CLIENT_KEY_STORAGE)}catch{}}
function apiURL(){return base+'/v0/management/plugins/'+encodeURIComponent(PLUGIN)+'/config'}
async function send(url,options){
  const controller=new AbortController();
  const timer=setTimeout(()=>controller.abort(),REQUEST_TIMEOUT_MS);
  try{return await fetch(url,{...options,signal:controller.signal})}
  catch(error){
    if(error&&error.name==='AbortError')throw new Error('CPA did not respond within '+REQUEST_TIMEOUT_MS/1000+' seconds.');
    throw new Error('Could not reach CPA. Check that it is running and try again.');
  }finally{clearTimeout(timer)}
}
async function managementRequest(options={}){
  const response=await send(apiURL(),{...options,headers:{'Authorization':'Bearer '+managementKey,'Content-Type':'application/json'}});
  if(response.status===401||response.status===403)throw new AuthError('CPA rejected the management key (HTTP '+response.status+').');
  if(!response.ok){const text=await response.text();throw new Error('CPA request failed (HTTP '+response.status+'): '+(text||response.statusText).slice(0,260))}
  return response;
}
async function fetchConfig(){
  const value=await (await managementRequest({method:'GET'})).json();
  return value&&typeof value==='object'&&!Array.isArray(value)?value:{};
}

// ---- normalizing saved settings ----------------------------------------------
function modelID(value){const model=String(value??'').trim();return model.toLowerCase()==='undefined'||model.toLowerCase()==='null'?'':model}
function isObject(value){return !!value&&typeof value==='object'&&!Array.isArray(value)}
// Unknown keys are kept, so saving here never strips settings this page does not edit.
function normalizeCandidate(item){return {...(isObject(item)?item:{}),model:modelID(item?.model),provider:String(item?.provider??''),protocol:String(item?.protocol??'')}}
function normalizeChains(value){const out={};if(!isObject(value))return out;for(const [alias,list] of Object.entries(value)){if(Array.isArray(list))out[alias]=list.map(normalizeCandidate)}return out}
function wholeNumber(value){const parsed=Number(value);return Number.isInteger(parsed)&&parsed>0?parsed:0}
function normalizeHints(value){const out={};if(isObject(value))for(const [model,hint] of Object.entries(value)){const name=String(model).trim(),text=String(hint??'').trim();if(name&&text)out[name]=text}return out}
// Mirrors the plugin's accepted spellings, so a hand-written "judge" is not downgraded to fallback.
function strategyType(value){
  const name=String(value??'').trim().toLowerCase();
  return {'':'fallback',fallback:'fallback','round-robin':'round-robin',round_robin:'round-robin',smart:'smart','smart-router':'smart',fusion:'fusion',judge:'fusion'}[name]||'fallback';
}
function normalizeStrategies(value,modelChains){
  const out={};
  for(const alias of Object.keys(modelChains||{})){
    const raw=isObject(value)&&own(value,alias)&&isObject(value[alias])?value[alias]:{};
    out[alias]={...raw,
      type:strategyType(raw.type),
      sticky_limit:Number.isInteger(Number(raw.sticky_limit))&&Number(raw.sticky_limit)>0?Number(raw.sticky_limit):1,
      selector:normalizeCandidate(raw.selector),
      judge:normalizeCandidate(raw.judge),
      confidence_threshold:Number.isFinite(Number(raw.confidence_threshold))&&Number(raw.confidence_threshold)>0?Number(raw.confidence_threshold):0.8,
      vision_enabled:raw.vision_enabled===true,
      vision_candidates:Array.isArray(raw.vision_candidates)?raw.vision_candidates.map(normalizeCandidate):[],
      vision_judge:normalizeCandidate(raw.vision_judge),
      panel_timeout_seconds:wholeNumber(raw.panel_timeout_seconds),
      quorum:wholeNumber(raw.quorum),
      quorum_grace_seconds:wholeNumber(raw.quorum_grace_seconds),
      read_only_tools:Array.isArray(raw.read_only_tools)?raw.read_only_tools.map(name=>String(name).trim()).filter(Boolean):[],
      rotate_per_request:raw.rotate_per_request===true,
      selector_timeout_seconds:wholeNumber(raw.selector_timeout_seconds),
      candidate_hints:normalizeHints(raw.candidate_hints)
    };
  }
  return out;
}
function strategyFor(alias){if(!strategies[alias])strategies[alias]=normalizeStrategies({},{[alias]:[]})[alias];return strategies[alias]}
function retryList(){return retryText.split(',').map(value=>value.trim()).filter(Boolean)}
function attemptLimit(){const attempts=Number(attemptsText);return Number.isInteger(attempts)&&attempts>=1?attempts:DEFAULT_ATTEMPTS}
function editableState(){return canonical({enabled:ui.enabled.checked,model_chains:chains,strategies:normalizeStrategies(strategies,chains),retry:retryList(),attempts:attemptsText.trim(),firstByte:firstByteText.trim(),attemptTimeout:attemptTimeoutText.trim()})}
function buildBody(){
  const body={...config,enabled:ui.enabled.checked,model_chains:chains,strategies:normalizeStrategies(strategies,chains),retry_statuses:retryList().map(Number),max_attempts:Number(attemptsText)};
  // Blank means "use the plugin's default", which only works if the key is absent.
  if(firstByteText.trim()==='')delete body.first_byte_timeout_seconds;else body.first_byte_timeout_seconds=Number(firstByteText);
  if(attemptTimeoutText.trim()==='')delete body.attempt_timeout_seconds;else body.attempt_timeout_seconds=Number(attemptTimeoutText);
  return body;
}

// ---- validation --------------------------------------------------------------
const wholeInRange=(value,min,max)=>Number.isInteger(value)&&value>=min&&value<=max;
function textIsTimeout(text){const trimmed=text.trim();if(trimmed==='')return true;const value=Number(trimmed);return value===0||wholeInRange(value,5,1800)}
function computeIssues(){
  const issues=[];
  const add=(level,alias,key,message,fix)=>issues.push({level,alias,key,message,fix});
  const names=Object.keys(chains);
  if(ui.enabled.checked&&names.length===0)add('error','','', 'Add at least one combo alias before turning combo routing on.');
  const codes=retryList().map(Number);
  if(!codes.length||codes.some(code=>!Number.isInteger(code)||(code!==403&&code!==429&&(code<500||code>599))))add('error','','retry','Retry statuses must be 403, 429, or 5xx codes, separated by commas.');
  else if(!codes.includes(500))add('warn','','retry','500 is not in your retry list. CPA reports dead providers and empty streams as 500, so those stop the chain.',{label:'Add 500',run:()=>{retryText=[...retryList().map(Number),500].sort((a,b)=>a-b).join(', ');renderAll()}});
  const attempts=Number(attemptsText);
  if(!wholeInRange(attempts,1,20))add('error','','attempts','Attempts per model must be a whole number from 1 to 20.');
  if(!textIsTimeout(firstByteText))add('error','','first-byte','First-byte timeout must be blank, 0, or 5 to 1800 seconds.');
  if(!textIsTimeout(attemptTimeoutText))add('error','','attempt-timeout','Attempt timeout must be blank, 0, or 5 to 1800 seconds.');

  for(const alias of names){
    const list=chains[alias],strategy=strategyFor(alias),type=strategy.type;
    if(!list.length)add('error',alias,'add-model','Add at least one model.');
    list.forEach((candidate,index)=>{if(!candidate.model.trim())add('error',alias,'candidate:'+index,'Model '+(index+1)+' needs a model ID.')});
    const seen=new Set();
    for(const candidate of list){
      const id=candidate.provider.trim()+'/'+candidate.model.trim();
      if(candidate.model.trim()&&seen.has(id))add('warn',alias,'','“'+candidate.model.trim()+'” is listed twice'+(type==='fusion'?', so it answers twice and costs double.':'.'));
      seen.add(id);
    }
    if(type==='round-robin'){
      if(!wholeInRange(strategy.sticky_limit,1,1000))add('error',alias,'sticky','Rotation count must be a whole number from 1 to 1000.');
      if(list.length===1)add('warn',alias,'','Round-robin needs at least two models to rotate.');
    }
    if(type==='smart'){
      if(!strategy.selector.model.trim())add('error',alias,'selector','Smart selection needs a selector model.');
      if(!Number.isFinite(strategy.confidence_threshold)||strategy.confidence_threshold<0.5||strategy.confidence_threshold>0.95)add('error',alias,'threshold','Minimum confidence must be from 0.5 to 0.95.');
      if(!wholeInRange(strategy.selector_timeout_seconds,0,120))add('error',alias,'selector-timeout','Selector timeout must be blank or a whole number up to 120.');
      if(list.length===1)add('warn',alias,'','With one model there is nothing to select, so the selector call is skipped.');
    }
    if(type==='fusion'){
      if(list.length>20)add('error',alias,'','Fusion supports at most 20 panel models.');
      if(!strategy.judge.model.trim()&&(strategy.judge.provider.trim()||strategy.judge.protocol.trim()))add('error',alias,'judge','Set a judge model before pinning its provider or protocol.');
      if(strategy.panel_timeout_seconds!==0&&!wholeInRange(strategy.panel_timeout_seconds,10,600))add('error',alias,'panel-timeout','Panel timeout must be blank or a whole number from 10 to 600.');
      if(!wholeInRange(strategy.quorum,0,20))add('error',alias,'quorum','Quorum must be blank or a whole number up to 20.');
      if(!wholeInRange(strategy.quorum_grace_seconds,0,300))add('error',alias,'grace','Quorum grace must be blank or a whole number up to 300.');
      if(list.length===1)add('warn',alias,'','Fusion with one model has no second opinion. Add another model or use Fallback.');
      if(strategy.quorum>list.length&&list.length)add('warn',alias,'quorum','Quorum is higher than the '+plural(list.length,'model')+' here, so it waits for all of them.');
    }
    if(strategy.vision_enabled){
      if(!strategy.vision_candidates.length)add('error',alias,'add-vision','Vision routing needs at least one vision model.');
      strategy.vision_candidates.forEach((candidate,index)=>{if(!candidate.model.trim())add('error',alias,'vision:'+index,'Vision model '+(index+1)+' needs a model ID.')});
      if(type==='fusion'&&strategy.vision_candidates.length>20)add('error',alias,'','Fusion supports at most 20 vision panel models.');
    }
    if(!strategy.vision_judge.model.trim()&&(strategy.vision_judge.provider.trim()||strategy.vision_judge.protocol.trim()))add('error',alias,'vision-judge','Set a vision judge model before pinning its provider or protocol.');
  }
  return issues;
}
// Settings hidden by the current strategy are still checked by the plugin, so a stale
// value left behind must not block a save the user cannot see the cause of.
function tidyBeforeSave(){
  const trim=candidate=>{candidate.model=candidate.model.trim();candidate.provider=candidate.provider.trim()};
  for(const [alias,list] of Object.entries(chains)){
    list.forEach(trim);
    const strategy=strategyFor(alias);
    trim(strategy.selector);trim(strategy.judge);trim(strategy.vision_judge);
    strategy.vision_candidates.forEach(trim);
    if(!strategy.vision_enabled)strategy.vision_candidates=strategy.vision_candidates.filter(candidate=>candidate.model);
    if(strategy.type!=='smart'&&(strategy.confidence_threshold<0.5||strategy.confidence_threshold>0.95))strategy.confidence_threshold=0.8;
    if(strategy.type!=='round-robin'&&!wholeInRange(strategy.sticky_limit,1,1000))strategy.sticky_limit=1;
  }
}

// ---- form pieces ---------------------------------------------------------------
function button(label,action,className='btn',extra={}){return h('button',{type:'button',class:className,text:label,onclick:action,...extra})}
function field({label,value='',placeholder='',type='text',key,hint,min,max,step,wide,rows,mono,onInput}){
  const id='field-'+(++fieldSequence),hintID=hint?id+'-hint':undefined;
  const input=rows
    ?h('textarea',{id,rows,placeholder,value,spellcheck:false,'aria-describedby':hintID,'data-field':key,oninput:()=>onInput(input.value)})
    :h('input',{type,id,placeholder,value,autocomplete:'off',spellcheck:false,min,max,step,class:mono?'model-input':undefined,'aria-describedby':hintID,'data-field':key,oninput:()=>onInput(input.value)});
  return h('div',{class:'field'+(wide?' wide':'')},h('label',{for:id,text:label}),input,hint?h('small',{id:hintID,text:hint}):null);
}
function modelPicker(candidate,{label='Model ID',placeholder='Search or type a model ID…',key,hint,wide,onChange}={}){
  candidate.model=modelID(candidate.model);
  const id='field-'+(++fieldSequence),hintID=hint?id+'-hint':undefined;
  const input=h('input',{type:'text',id,value:candidate.model,autocomplete:'off',spellcheck:false,class:'model-input',list:'modelCatalog',placeholder,'aria-describedby':hintID,'data-field':key,
    oninput:()=>{candidate.model=modelID(input.value);touched();if(onChange)onChange()},
    onchange:()=>{candidate.model=modelID(input.value);input.value=candidate.model}});
  return h('div',{class:'field'+(wide?' wide':'')},h('label',{for:id,text:label}),input,hint?h('small',{id:hintID,text:hint}):null);
}
function protocolSelect(candidate){
  const id='field-'+(++fieldSequence);
  const select=h('select',{id,onchange:()=>{candidate.protocol=select.value;touched()}},PROTOCOLS.map(([value,label])=>h('option',{value,text:label})));
  if(candidate.protocol&&!PROTOCOLS.some(([value])=>value===candidate.protocol))select.append(h('option',{value:candidate.protocol,text:candidate.protocol}));
  select.value=candidate.protocol;
  return h('div',{class:'field'},h('label',{for:id,text:'Protocol override'}),select);
}
function candidateRow(candidate,index,list,scope){
  const details=h('details',{ontoggle:()=>{if(details.open)openAdvanced.add(candidate);else openAdvanced.delete(candidate)}});
  if(candidate.provider||candidate.protocol)openAdvanced.add(candidate);
  details.open=openAdvanced.has(candidate);
  details.append(h('summary',{text:'Advanced routing'}),h('div',{class:'pair'},
    field({label:'Provider pin',value:candidate.provider,placeholder:'Blank lets CPA choose',onInput:value=>{candidate.provider=value;touched()}}),
    protocolSelect(candidate)));
  const label=(scope==='vision'?'Vision model ':'Model ')+(index+1);
  const move=delta=>{
    const next=index+delta;
    [list[index],list[next]]=[list[next],list[index]];
    touched();renderMain();
    const row=ui.main.querySelector('[data-row="'+scope+':'+next+'"]');
    const target=row&&(row.querySelector(delta<0?'[data-move=up]:not(:disabled)':'[data-move=down]:not(:disabled)')||row.querySelector('[data-move]:not(:disabled)'));
    if(target)target.focus();
  };
  return h('div',{class:'row','data-row':scope+':'+index},
    h('div',{class:'order',text:String(index+1),'aria-hidden':'true'}),
    h('div',{class:'row-fields'},modelPicker(candidate,{label,key:scope+':'+index}),details),
    h('div',{class:'row-actions'},
      button('↑',()=>move(-1),'icon',{disabled:index===0,'aria-label':'Move '+label+' up','data-move':'up',title:'Move up'}),
      button('↓',()=>move(1),'icon',{disabled:index===list.length-1,'aria-label':'Move '+label+' down','data-move':'down',title:'Move down'}),
      button('×',()=>{
        list.splice(index,1);touched();renderMain();
        const next=ui.main.querySelector('[data-row="'+scope+':'+Math.min(index,list.length-1)+'"] input')||ui.main.querySelector('[data-field="add-'+(scope==='vision'?'vision':'model')+'"]');
        if(next)next.focus();
      },'icon remove',{'aria-label':'Remove '+label,title:'Remove'})));
}
function addRow(list,scope,label,key){
  return button(label,()=>{
    list.push(normalizeCandidate({}));touched();renderMain();
    const input=ui.main.querySelector('[data-row="'+scope+':'+(list.length-1)+'"] input');
    if(input)input.focus();
  },'btn add',{'data-field':key});
}

// ---- dialogs -------------------------------------------------------------------
// Native prompt() and confirm() silently do nothing inside a sandboxed iframe.
function ask({title,text,confirm='Confirm',cancel='Cancel',danger=false,input=null,validate=null}){
  return new Promise(resolve=>{
    const dialog=ui.dialog,box=byId('dialogInput'),error=byId('dialogError'),confirmBtn=byId('dialogConfirm'),cancelBtn=byId('dialogCancel'),form=byId('dialogForm');
    const opener=document.activeElement;
    byId('dialogTitle').textContent=title;
    byId('dialogText').textContent=text;
    confirmBtn.textContent=confirm;
    cancelBtn.textContent=cancel;
    confirmBtn.className='btn '+(danger?'danger':'primary');
    box.classList.toggle('hide',input===null);
    box.value=input??'';
    error.classList.add('hide');
    const finish=result=>{
      form.removeEventListener('submit',onSubmit);
      cancelBtn.removeEventListener('click',onCancel);
      dialog.removeEventListener('cancel',onEscape);
      dialog.close();
      if(opener&&opener.isConnected)opener.focus();
      resolve(result);
    };
    const onSubmit=event=>{
      event.preventDefault();
      if(input!==null){
        const problem=validate?validate(box.value):'';
        if(problem){error.textContent=problem;error.classList.remove('hide');box.setAttribute('aria-invalid','true');box.focus();return}
        box.removeAttribute('aria-invalid');
        finish(box.value);return;
      }
      finish(true);
    };
    const onCancel=()=>finish(input!==null?null:false);
    const onEscape=event=>{event.preventDefault();onCancel()};
    form.addEventListener('submit',onSubmit);
    cancelBtn.addEventListener('click',onCancel);
    dialog.addEventListener('cancel',onEscape);
    box.removeAttribute('aria-invalid');
    dialog.showModal();
    if(input!==null){box.focus();box.select()}else cancelBtn.focus();
  });
}
function aliasProblem(value,currentName=''){
  if(!value.trim())return 'Alias name cannot be empty.';
  if(/\s/.test(value))return 'Alias names cannot contain spaces.';
  if(value==='__proto__')return 'That name is reserved. Choose another.';
  if(value!==currentName&&own(chains,value))return 'That alias already exists.';
  return '';
}

// ---- rendering -----------------------------------------------------------------
function routeLine(alias){
  const list=chains[alias],strategy=strategyFor(alias);
  const name=(candidate,className)=>h('span',{class:candidate.model.trim()?className:'none',text:candidate.model.trim()||'(no model)'});
  const join=(items,separator)=>items.flatMap((item,index)=>index?[h('i',{text:separator}),item]:[item]);
  const out=[];
  if(!list.length){out.push(h('span',{class:'none',text:'No models yet'}));return out}
  if(strategy.type==='fusion'){
    out.push(...join(list.map(candidate=>name(candidate)),'+'),h('i',{text:'▸ judge'}));
    out.push(strategy.judge.model.trim()?h('b',{text:strategy.judge.model.trim()}):h('b',{text:(list[0].model.trim()||'first model')+' (first model)'}));
    if(strategy.read_only_tools.length)out.push(h('small',{text:'Read-only tool calls come straight from '+(list[0].model.trim()||'the first model')+', with no panel or judge.'}));
    out.push(h('small',{text:'If every panel fails, models answer directly, without the panel step.'}));
  }else if(strategy.type==='smart'){
    out.push(h('b',{text:strategy.selector.model.trim()||'(no selector)'}),h('i',{text:'▸ picks from'}),...join(list.map(candidate=>name(candidate)),'/'));
    out.push(h('small',{text:'Chooses once per user message, then the other models act as fallbacks.'}));
  }else if(strategy.type==='round-robin'){
    out.push(...join(list.map(candidate=>name(candidate)),'↻'));
    out.push(h('small',{text:strategy.rotate_per_request?'Starting model changes on every request, including tool steps.':'Starting model changes per user message. Tool steps stay on one model.'}));
  }else{
    out.push(...join(list.map(candidate=>name(candidate)),'→'));
    out.push(h('small',{text:'Each model gets up to '+plural(attemptLimit(),'try').replace('trys','tries')+', so up to '+plural(attemptLimit()*list.length,'attempt')+' in total.'}));
  }
  return out;
}
function checksFor(scope){
  const issues=computeIssues().filter(issue=>issue.alias===scope&&(issue.level==='warn'||saveAttempted));
  return issues.sort((a,b)=>(a.level==='error'?0:1)-(b.level==='error'?0:1)).map(issue=>h('div',{class:'check '+issue.level},h('span',{text:issue.message}),issue.fix?button(issue.fix.label,issue.fix.run,'btn small'):null));
}
function refreshLive(){
  const scope=view==='settings'?'':selected;
  const route=byId('route');
  if(route&&selected)route.replaceChildren(...routeLine(selected));
  const checks=byId('checks');
  if(checks)checks.replaceChildren(...checksFor(scope));
  const errors=new Set(saveAttempted?computeIssues().filter(issue=>issue.level==='error'&&issue.alias===scope).map(issue=>issue.key):[]);
  for(const el of ui.main.querySelectorAll('[data-field]')){
    if(errors.has(el.dataset.field))el.setAttribute('aria-invalid','true');else el.removeAttribute('aria-invalid');
  }
  renderRail();
}
function touched(){
  dirty=editableState()!==savedState;
  if(ui.savebar.dataset.kind==='success'||(ui.savebar.dataset.kind==='error'&&!saveAttempted))setMessage('');
  if(saveAttempted&&ui.savebar.dataset.kind==='error'&&!computeIssues().some(issue=>issue.level==='error'))setMessage('');
  updateSavebar();
  refreshLive();
}
function renderRail(){
  const focusedAlias=document.activeElement&&document.activeElement.dataset?document.activeElement.dataset.alias:undefined;
  const issues=computeIssues();
  const names=Object.keys(chains).sort((a,b)=>a.localeCompare(b));
  ui.aliasCount.textContent=String(names.length);
  ui.aliases.replaceChildren(...names.map(name=>{
    const strategy=STRATEGIES.find(item=>item.id===strategyFor(name).type)||STRATEGIES[0];
    const own_=issues.filter(issue=>issue.alias===name);
    const level=saveAttempted&&own_.some(issue=>issue.level==='error')?'error':own_.some(issue=>issue.level==='warn')?'warn':'';
    const current=view==='alias'&&name===selected;
    return h('button',{type:'button',class:'alias','aria-current':current?'true':undefined,'data-alias':name,title:name,onclick:()=>{selected=name;view='alias';if(ui.savebar.dataset.kind!=='error')setMessage('');renderAll();window.scrollTo({top:0});const again=ui.aliases.querySelector('[aria-current=true]');if(again)again.focus()}},
      h('span',{class:'tag',text:strategy.tag,title:strategy.label}),
      h('span',{class:'alias-name',text:name}),
      h('span',{class:'alias-meta'},level?h('span',{class:'flag '+level,role:'img','aria-label':level==='error'?'Has errors':'Has warnings'}):null,h('span',{text:String(chains[name].length),'aria-label':plural(chains[name].length,'model')})));
  }));
  if(focusedAlias){const again=[...ui.aliases.children].find(el=>el.dataset.alias===focusedAlias);if(again)again.focus()}
  ui.settingsLink.setAttribute('aria-current',view==='settings'?'true':'false');
  const globalIssues=issues.filter(issue=>issue.alias==='');
  ui.settingsMeta.textContent=globalIssues.length?plural(globalIssues.length,'note'):'';
  ui.banner.classList.toggle('hide',!hasLoaded||ui.enabled.checked);
}
function strategySection(alias){
  const strategy=strategyFor(alias);
  const radios=h('div',{class:'strategies',role:'radiogroup','aria-label':'Routing strategy'},STRATEGIES.map(item=>h('label',{class:'strategy'},
    h('input',{type:'radio',name:'strategy',value:item.id,checked:strategy.type===item.id,onchange:()=>{
      strategy.type=item.id;touched();renderMain();
      const checked=ui.main.querySelector('input[name=strategy]:checked');if(checked)checked.focus();
    }}),h('strong',{text:item.label}),h('span',{text:item.blurb}))));
  return h('section',{class:'card section'},h('div',{class:'section-head'},h('h3',{class:'section-title',text:'Routing strategy'})),radios,strategyOptions(alias,strategy));
}
function strategyOptions(alias,strategy){
  if(strategy.type==='round-robin'){
    return h('div',{class:'grid'},
      field({label:strategy.rotate_per_request?'Requests starting at each model':'User messages starting at each model',value:String(strategy.sticky_limit),type:'number',min:1,max:1000,placeholder:'1–1000',key:'sticky',onInput:value=>{strategy.sticky_limit=Number(value);touched()}}),
      h('label',{class:'toggle',style:'align-self:end'},h('input',{type:'checkbox',checked:strategy.rotate_per_request,onchange:event=>{strategy.rotate_per_request=event.target.checked;touched();renderMain()}}),h('span',{text:'Rotate on every request, including agent tool steps'})));
  }
  if(strategy.type==='smart'){
    return h('div',{class:'grid'},
      modelPicker(strategy.selector,{label:'Selector model ID',placeholder:'Search or type a selector model ID…',key:'selector',hint:'A fast, cheap model is enough.'}),
      field({label:'Selector provider pin',value:strategy.selector.provider,placeholder:'Optional CPA provider ID',onInput:value=>{strategy.selector.provider=value.trim();touched()}}),
      field({label:'Minimum confidence',value:String(strategy.confidence_threshold),type:'number',min:0.5,max:0.95,step:0.05,placeholder:'0.5–0.95',key:'threshold',hint:'Below this, the configured order is used.',onInput:value=>{strategy.confidence_threshold=Number(value);touched()}}),
      field({label:'Selector timeout (seconds)',value:strategy.selector_timeout_seconds?String(strategy.selector_timeout_seconds):'',type:'number',min:1,max:120,placeholder:'Blank uses 10',key:'selector-timeout',onInput:value=>{strategy.selector_timeout_seconds=Number(value);touched()}}));
  }
  if(strategy.type==='fusion'){
    return h('div',{class:'grid'},
      modelPicker(strategy.judge,{label:'Judge model ID (optional)',placeholder:'Blank uses the first model',key:'judge'}),
      field({label:'Judge provider pin (optional)',value:strategy.judge.provider,placeholder:'Optional CPA provider ID',onInput:value=>{strategy.judge.provider=value.trim();touched()}}),
      field({label:'Panel timeout (seconds)',value:strategy.panel_timeout_seconds?String(strategy.panel_timeout_seconds):'',type:'number',min:10,max:600,placeholder:'Blank uses 180',key:'panel-timeout',hint:'Long conversations can take minutes without streaming.',onInput:value=>{strategy.panel_timeout_seconds=Number(value);touched()}}),
      field({label:'Quorum (panels to wait for)',value:strategy.quorum?String(strategy.quorum):'',type:'number',min:0,max:20,placeholder:'Blank waits for all',key:'quorum',onInput:value=>{strategy.quorum=Number(value);touched()}}),
      field({label:'Quorum grace (seconds)',value:strategy.quorum_grace_seconds?String(strategy.quorum_grace_seconds):'',type:'number',min:0,max:300,placeholder:'Blank uses 15',key:'grace',hint:'How long slower panels get once quorum is met.',onInput:value=>{strategy.quorum_grace_seconds=Number(value);touched()}}));
  }
  return null;
}
function modelsSection(alias){
  const list=chains[alias],strategy=strategyFor(alias);
  const parallel=strategy.type==='fusion';
  return h('section',{class:'card section'},
    h('div',{class:'section-head'},
      h('h3',{class:'section-title',text:parallel?'Fusion panel models':'Candidate models'}),
      h('span',{class:'section-note'},h('span',{id:'catalogNote',text:modelStatusText}),' · ',button('Model catalog',openSettings,'linkish'))),
    list.length?h('div',{class:'rows','data-flow':parallel?'parallel':'sequence'},list.map((candidate,index)=>candidateRow(candidate,index,list,'candidate'))):h('div',{class:'inner-empty',text:'Add the first model for this alias.'}),
    addRow(list,'candidate','＋ Add candidate model','add-model'));
}
function visionSection(alias){
  const strategy=strategyFor(alias),list=strategy.vision_candidates;
  const section=h('section',{class:'card section'},
    h('div',{class:'section-head',style:'margin-bottom:4px'},h('h3',{class:'section-title',text:'Image-triggered vision routing'})),
    h('label',{class:'toggle'},h('input',{type:'checkbox',checked:strategy.vision_enabled,onchange:event=>{strategy.vision_enabled=event.target.checked;touched();renderMain()}}),h('span',{text:'Route image requests to vision models'})));
  if(!strategy.vision_enabled)return section;
  section.append(h('p',{class:'help',style:'margin:2px 0 10px',text:'Requests containing an image use this pool instead of the models above. Text-only requests are unchanged.'}));
  section.append(list.length?h('div',{class:'rows','data-flow':strategy.type==='fusion'?'parallel':'sequence'},list.map((candidate,index)=>candidateRow(candidate,index,list,'vision'))):h('div',{class:'inner-empty',text:'Add a model that accepts images.'}));
  section.append(addRow(list,'vision','＋ Add vision model','add-vision'));
  if(strategy.type==='fusion')section.append(h('div',{class:'grid'},
    modelPicker(strategy.vision_judge,{label:'Vision judge model ID (optional)',placeholder:'Blank uses the first vision model',key:'vision-judge',hint:'Must accept images.'}),
    field({label:'Vision judge provider pin (optional)',value:strategy.vision_judge.provider,placeholder:'Optional CPA provider ID',onInput:value=>{strategy.vision_judge.provider=value.trim();touched()}})));
  return section;
}
function aliasHead(alias){
  return h('section',{class:'card head'},
    h('div',{class:'head-line'},
      h('h2',{text:alias}),
      h('div',{class:'head-actions'},button('Rename',renameAlias,'btn small ghost'),button('Delete',deleteAlias,'btn small ghost danger'))),
    h('div',{class:'route',id:'route','aria-live':'off'},routeLine(alias)),
    h('div',{class:'checks',id:'checks'}));
}
function renderSettings(){
  ui.main.replaceChildren(
    h('section',{class:'card head'},h('div',{class:'head-line'},h('h2',{text:'Global settings'})),h('p',{class:'help',text:'These apply to every alias.'}),h('div',{class:'checks',id:'checks'})),
    h('section',{class:'card section'},
      h('h3',{class:'section-title',text:'Retry behavior'}),
      h('div',{class:'grid'},
        field({label:'Retry on HTTP status codes',value:retryText,placeholder:'403, 429, 500, 502, 503, 504',mono:true,key:'retry',hint:'Only 403, 429, and 5xx are allowed. A context overflow always moves on.',onInput:value=>{retryText=value;touched()}}),
        field({label:'Attempts per model',value:attemptsText,type:'number',min:1,max:20,placeholder:'1–20',key:'attempts',hint:'Each model gets this many tries before the next one starts. A request a model cannot accept moves on or stops right away.',onInput:value=>{attemptsText=value;touched()}}))),
    h('section',{class:'card section'},
      h('h3',{class:'section-title',text:'Silent model limits'}),
      h('div',{class:'grid'},
        field({label:'First-byte timeout (seconds)',value:firstByteText,type:'number',min:0,max:1800,placeholder:'Blank uses 120',key:'first-byte',hint:'Streaming. Skips a silent model when another is left. 0 turns this off.',onInput:value=>{firstByteText=value;touched()}}),
        field({label:'Attempt timeout (seconds)',value:attemptTimeoutText,type:'number',min:0,max:1800,placeholder:'Blank means off',key:'attempt-timeout',hint:'Non-streaming. Measured over the whole response.',onInput:value=>{attemptTimeoutText=value;touched()}}))),
    catalogSection());
  refreshLive();
}
function catalogSection(){
  const input=h('input',{type:'password',id:'clientAPIKey',autocomplete:'new-password',placeholder:'CPA client API key',value:clientAPIKey,'aria-describedby':'catalogStatus'});
  return h('section',{class:'card section'},
    h('h3',{class:'section-title',text:'Model catalog'}),
    h('p',{class:'help',text:'Optional. Loads model IDs so the pickers can search them. You can always type an ID. The key stays in this tab until you forget it or close the tab.'}),
    h('form',{class:'grid',novalidate:true,onsubmit:event=>{event.preventDefault();rememberClientAPIKey(input.value.trim());void loadModels()}},
      h('div',{class:'field'},h('label',{for:'clientAPIKey',text:'Client API key'}),input),
      h('div',{class:'save-actions',style:'align-self:end;margin:0'},
        h('button',{class:'btn primary',type:'submit',text:'Load models'}),
        button('Forget key',()=>{modelsRequest++;rememberClientAPIKey('');input.value='';availableModels=[];syncCatalog();setModelStatus('Client API key forgotten',false)},'btn ghost'))),
    h('p',{class:'help',id:'catalogStatus',role:'status',text:modelStatusText,style:modelStatusError?'color:var(--red)':undefined}));
}
function renderEmpty(){
  ui.main.replaceChildren(h('section',{class:'card empty'},h('strong',{text:'Build your first combo'}),h('p',{text:'A combo is one model name your clients use, backed by several real models. Name one to begin.'}),button('Name a combo',()=>ui.newAlias.focus(),'btn primary')));
}
function renderLoadError(error){
  ui.main.replaceChildren(h('section',{class:'card empty'},h('strong',{text:'Could not load saved combos'}),h('p',{text:error instanceof Error?error.message:'Check that CPA management is available.'}),button('↻ Try again',()=>void load().catch(handleLoadError),'btn')));
}
function renderMain(){
  ui.main.style.animation='none';
  if(view==='settings'){renderSettings();return}
  if(!selected||!own(chains,selected)){renderEmpty();refreshLive();return}
  ui.main.replaceChildren(aliasHead(selected),strategySection(selected),modelsSection(selected),visionSection(selected));
  refreshLive();
}
function renderAll(){dirty=hasLoaded&&editableState()!==savedState;renderMain();renderRail();updateSavebar()}
function openSettings(){view='settings';renderAll();window.scrollTo({top:0});ui.settingsLink.focus()}

// ---- model catalog -------------------------------------------------------------
function setModelStatus(text,isError){
  modelStatusText=text;modelStatusError=isError;
  const note=byId('catalogNote');if(note)note.textContent=text;
  const status=byId('catalogStatus');if(status){status.textContent=text;status.style.color=isError?'var(--red)':''}
}
// Filling one shared list means a late response cannot disturb a field being edited.
function syncCatalog(){
  ui.catalog.replaceChildren(...availableModels.map(model=>h('option',{value:model.id,label:model.providers.length?model.providers.join(', '):undefined})));
}
async function loadModels(){
  const request=++modelsRequest;
  if(!clientAPIKey){setModelStatus('Client API key needed',false);return}
  setModelStatus('Loading models…',false);
  try{
    const response=await send(base+'/v1/models',{headers:{'Authorization':'Bearer '+clientAPIKey}});
    if(request!==modelsRequest)return;
    if(response.status===401||response.status===403){rememberClientAPIKey('');const box=byId('clientAPIKey');if(box)box.value='';throw new Error('CPA rejected the client API key.')}
    if(!response.ok)throw new Error('Model catalog request failed (HTTP '+response.status+').');
    const payload=await response.json();
    if(request!==modelsRequest)return;
    const modelsById=new Map();
    for(const item of Array.isArray(payload?.data)?payload.data:[]){
      const id=String(item?.id??'').trim();if(!id)continue;
      if(!modelsById.has(id))modelsById.set(id,{id,providers:new Set()});
      const provider=String(item?.owned_by??'').trim();if(provider)modelsById.get(id).providers.add(provider);
    }
    availableModels=Array.from(modelsById.values()).map(model=>({id:model.id,providers:Array.from(model.providers).sort()})).sort((a,b)=>a.id.localeCompare(b.id));
    syncCatalog();
    setModelStatus(availableModels.length?plural(availableModels.length,'model')+' loaded':'No models found',false);
  }catch(error){
    if(request===modelsRequest)setModelStatus(error instanceof Error?error.message:'Model catalog unavailable',true);
  }
}

// ---- actions -------------------------------------------------------------------
function createAlias(value){
  const name=value.trim(),problem=aliasProblem(name);
  if(problem){setMessage(problem,'error');ui.newAlias.setAttribute('aria-invalid','true');ui.newAlias.focus();return}
  ui.newAlias.removeAttribute('aria-invalid');
  chains[name]=[normalizeCandidate({})];
  strategyFor(name);
  selected=name;view='alias';ui.newAlias.value='';
  setMessage('');
  renderAll();
  const input=ui.main.querySelector('[data-row="candidate:0"] input');if(input)input.focus();
}
async function renameAlias(){
  const current=selected;
  const next=await ask({title:'Rename alias',text:'Clients must use the new name after you save.',confirm:'Rename',input:current,validate:value=>aliasProblem(value,current)});
  if(next===null||next===current)return;
  chains[next]=chains[current];strategies[next]=strategyFor(current);
  delete chains[current];delete strategies[current];
  selected=next;renderAll();
}
async function deleteAlias(){
  const current=selected;
  if(!await ask({title:'Delete “'+current+'”?',text:'Clients using this name will stop working after you save.',confirm:'Delete alias',danger:true}))return;
  delete chains[current];delete strategies[current];
  selected=Object.keys(chains).sort((a,b)=>a.localeCompare(b))[0]||'';
  renderAll();
}
function focusIssue(issue){
  view=issue.alias===''?'settings':'alias';
  if(issue.alias)selected=issue.alias;
  renderAll();
  const el=issue.key&&ui.main.querySelector('[data-field="'+issue.key+'"]');
  if(el){el.focus();el.scrollIntoView({block:'center'})}
}
async function save(){
  if(saving||!hasLoaded)return;
  saveAttempted=true;
  tidyBeforeSave();
  const errors=computeIssues().filter(issue=>issue.level==='error');
  if(errors.length){
    const first=errors.find(issue=>issue.alias===(view==='settings'?'':selected))||errors[0];
    focusIssue(first);
    setMessage((first.alias?'“'+first.alias+'”: ':'')+first.message+(errors.length>1?' ('+(errors.length-1)+' more)':''),'error');
    return;
  }
  saving=true;updateSavebar();setStatus('busy','Saving…');setMessage('');
  try{
    const stored=await fetchConfig();
    if(canonical(stored)!==loadedRaw){
      saving=false;updateSavebar();
      const overwrite=await ask({title:'Settings changed elsewhere',text:'Someone changed these settings after this page loaded. Saving replaces their changes with yours.',confirm:'Overwrite',cancel:'Keep editing',danger:true});
      if(!overwrite){setStatus('ok','Connected to CPA');setMessage('Not saved. Discard your edits to load the latest settings.','error');return}
      saving=true;updateSavebar();
    }
    // Captured now: anything typed while the request runs must stay marked as unsaved.
    const snapshot=editableState(),body=buildBody();
    await managementRequest({method:'PUT',body:JSON.stringify(body)});
    config=JSON.parse(JSON.stringify(body));
    savedState=snapshot;
    saveAttempted=false;
    try{loadedRaw=canonical(await fetchConfig())}catch{loadedRaw=canonical(body)}
    saving=false;
    dirty=editableState()!==savedState;
    setStatus('ok','Settings saved');
    setMessage('Saved. CPA is applying the plugin configuration.','success');
    if(!dirty)renderAll();else refreshLive();
  }catch(error){
    if(error instanceof AuthError){openGate(error.message);setStatus('error','Management key rejected');setMessage('Not saved. Reconnect to try again; your edits are still here.','error')}
    else{setStatus('error','Save failed');setMessage(error instanceof Error?error.message:'Unable to save combo settings.','error')}
  }finally{saving=false;updateSavebar()}
}
async function load(){
  setStatus('busy','Reading saved settings…');
  const stored=await fetchConfig();
  config=stored;
  loadedRaw=canonical(stored);
  chains=normalizeChains(config.model_chains);
  strategies=normalizeStrategies(config.strategies,chains);
  if(!selected||!own(chains,selected))selected=Object.keys(chains).sort((a,b)=>a.localeCompare(b))[0]||'';
  retryText=Array.isArray(config.retry_statuses)&&config.retry_statuses.length?config.retry_statuses.join(', '):DEFAULT_RETRIES.join(', ');
  attemptsText=String(config.max_attempts??DEFAULT_ATTEMPTS);
  firstByteText=config.first_byte_timeout_seconds===undefined?'':String(config.first_byte_timeout_seconds);
  attemptTimeoutText=config.attempt_timeout_seconds===undefined?'':String(config.attempt_timeout_seconds);
  ui.enabled.checked=config.enabled===true;
  ui.enabled.disabled=false;ui.newAlias.disabled=false;ui.aliasForm.querySelector('button').disabled=false;ui.settingsLink.disabled=false;
  hasLoaded=true;saveAttempted=false;
  savedState=editableState();
  setMessage('');
  renderAll();
  setStatus('ok','Connected to CPA');
  void loadModels();
}
// Nothing can be edited until settings load, because saving from an empty form would
// replace the stored configuration with nothing.
function lockEditing(){ui.enabled.disabled=true;ui.newAlias.disabled=true;ui.aliasForm.querySelector('button').disabled=true;ui.settingsLink.disabled=true}
function openGate(message){
  ui.gate.classList.add('open');
  ui.gateError.textContent=message||'';
  ui.gateError.classList.toggle('hide',!message);
  ui.key.focus();
}
function handleLoadError(error){
  if(error instanceof AuthError){setStatus('error','Management key rejected');openGate(error.message);return}
  setStatus('error','Cannot reach CPA');
  renderLoadError(error);
}

// ---- wiring --------------------------------------------------------------------
ui.aliasForm.addEventListener('submit',event=>{event.preventDefault();createAlias(ui.newAlias.value)});
ui.newAlias.addEventListener('input',()=>{ui.newAlias.removeAttribute('aria-invalid');if(ui.savebar.dataset.kind==='error'&&!saveAttempted)setMessage('')});
ui.settingsLink.addEventListener('click',openSettings);
ui.enabled.addEventListener('change',touched);
ui.save.addEventListener('click',()=>void save());
ui.discard.addEventListener('click',async()=>{
  if(!await ask({title:'Discard unsaved edits?',text:'This reloads the saved settings from CPA.',confirm:'Discard edits',cancel:'Keep editing',danger:true}))return;
  try{await load()}catch(error){if(error instanceof AuthError)openGate(error.message);else setMessage(error instanceof Error?error.message:'Unable to reload settings.','error')}
});
byId('keyForm').addEventListener('submit',async event=>{
  event.preventDefault();
  const key=ui.key.value.trim();
  if(!key){openGate('Enter the CPA management key.');return}
  managementKey=key;
  ui.gateError.classList.add('hide');
  try{
    // Reconnecting must not throw away edits made before the key was rejected.
    if(hasLoaded&&dirty)await fetchConfig();else await load();
    ui.gate.classList.remove('open');
    ui.key.value='';
    setStatus('ok','Connected to CPA');
  }catch(error){
    if(error instanceof AuthError)openGate(error.message);
    else{ui.gate.classList.remove('open');handleLoadError(error)}
  }
});
window.addEventListener('beforeunload',event=>{if(dirty){event.preventDefault();event.returnValue=''}});

lockEditing();
updateSavebar();
managementKey=rememberedKey();
clientAPIKey=readClientAPIKey();
if(managementKey)void load().catch(handleLoadError);
else{setStatus('error','Management key required');openGate('')}
})();
</script>
</body>
</html>`
