/* ============================================================
   SCRUN — Board setup wizard logic
   ============================================================ */
const SU_WORKFLOWS={
  dev:{name:"Software delivery",desc:"Build, test and ship features with review gates.",
    stages:[["backlog","Backlog","#586675"],["todo","To Do","#2898da"],["inprogress","In Progress","#4db0ef"],["testing","Testing","#a78bfa","auto"],["review","Review","#f5b042","human"],["done","Done","#34d399"]]},
  research:{name:"Research spike",desc:"Investigate, benchmark and synthesise a recommendation.",
    stages:[["intake","Intake","#586675"],["analyse","Analyse","#2898da"],["synth","Synthesise","#a78bfa"],["review","Report Review","#f5b042","human"],["done","Done","#34d399"]]},
  support:{name:"Triage & resolve",desc:"Intake issues, triage, fix and verify before close.",
    stages:[["inbox","Inbox","#586675"],["triage","Triage","#f5b042"],["fix","Fix","#2898da"],["verify","Verify","#a78bfa","auto"],["closed","Closed","#34d399"]]},
  ops:{name:"Ops & incident",desc:"Detect, mitigate and post-mortem operational work.",
    stages:[["detected","Detected","#f4685f"],["mitigating","Mitigating","#f5b042"],["monitoring","Monitoring","#4db0ef"],["postmortem","Post-mortem","#a78bfa","human"],["resolved","Resolved","#34d399"]]},
};
const SU_COLORS=["#2898da","#2dd4bf","#a78bfa","#f5b042","#34d399","#f4685f","#60a5fa"];

const SU={ step:0, name:"AI Workforce Board", key:"AI", desc:"", color:"#2898da",
  preset:"dev", stages:null, agents:Object.keys(DB.AGENTS).slice(0,5) };
const SU_PANES=["project","workflow","agents","review"];

function suStages(){ return (SU.stages||SU_WORKFLOWS[SU.preset].stages).map(s=>({id:s[0],name:s[1],dot:s[2],gate:s[3]||null})); }

function autoKey(name){ return (name.split(/\s+/).map(w=>w[0]).join("").replace(/[^a-z]/gi,"")||"BD").slice(0,4).toUpperCase(); }

/* -------- render -------- */
function renderSetup(){
  document.querySelectorAll(".su-step").forEach((el,i)=>{
    el.classList.toggle("active",i===SU.step); el.classList.toggle("done",i<SU.step);
    const d=el.querySelector(".sdot"); d.innerHTML=i<SU.step?'<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><path d="m5 12 5 5L20 6"/></svg>':(i+1);
  });
  document.querySelectorAll(".su-pane").forEach((p,i)=>{
    const on=i===SU.step;
    if(on && !p.classList.contains("on")){ p.classList.add("on","anim"); setTimeout(()=>p.classList.remove("anim"),300); }
    else if(!on){ p.classList.remove("on","anim"); }
    else { p.classList.add("on"); }
  });
  document.getElementById("suProg").textContent=`Step ${SU.step+1} of 4`;
  document.getElementById("suBack").style.visibility=SU.step===0?"hidden":"visible";
  const next=document.getElementById("suNext");
  next.innerHTML=SU.step===3?'Launch board <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M5 12h14M13 6l6 6-6 6"/></svg>':'Continue <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M9 6l6 6-6 6"/></svg>';
  if(SU.step===0) renderSuProject();
  if(SU.step===1) renderSuWorkflow();
  if(SU.step===2) renderSuAgents();
  if(SU.step===3) renderSuReview();
}

function renderSuProject(){
  const pane=document.getElementById("suPane0");
  pane.innerHTML=`
    <div class="su-eyebrow">Project</div>
    <h1 class="su-h">Name your board</h1>
    <p class="su-sub">This board organises the work your agent workforce will pick up. Give it a clear name and a short key — the key prefixes every ticket (e.g. <b>${SU.key}-128</b>).</p>
    <div class="su-row">
      <div class="fld"><label class="fld-l">Board name</label><input class="inp" id="suName" value="${esc(SU.name)}" placeholder="e.g. Platform Workforce" /></div>
      <div class="fld"><label class="fld-l">Ticket key</label><input class="inp" id="suKey" value="${SU.key}" maxlength="4" style="text-align:center;font-family:var(--font-mono);font-weight:700;text-transform:uppercase" /></div>
    </div>
    <div class="fld"><label class="fld-l">Description <span class="tip">optional</span></label>
      <textarea class="inp" id="suDesc" rows="2" placeholder="What this board is for and who it serves.">${esc(SU.desc)}</textarea></div>
    <div class="fld"><label class="fld-l">Accent colour</label>
      <div class="su-iconpick">${SU_COLORS.map(c=>`<span class="su-sw ${c===SU.color?'on':''}" data-sucolor="${c}" style="background:${c};color:${c}"></span>`).join("")}</div></div>
    <div class="fld"><label class="fld-l">Preview</label>
      <div class="su-preview-card" id="suPrev"></div></div>`;
  suProjectPreview();
  const nm=pane.querySelector("#suName"), ky=pane.querySelector("#suKey");
  let keyEdited=SU.key!==autoKey(SU.name);
  nm.addEventListener("input",()=>{ SU.name=nm.value; if(!keyEdited){ SU.key=autoKey(nm.value); ky.value=SU.key; } suProjectPreview(); });
  ky.addEventListener("input",()=>{ keyEdited=true; SU.key=ky.value.toUpperCase().replace(/[^A-Z]/g,"").slice(0,4)||"BD"; ky.value=SU.key; suProjectPreview(); });
  pane.querySelector("#suDesc").addEventListener("input",e=>SU.desc=e.target.value);
  pane.querySelectorAll("[data-sucolor]").forEach(s=>s.onclick=()=>{ SU.color=s.dataset.sucolor;
    pane.querySelectorAll(".su-sw").forEach(x=>x.classList.toggle("on",x===s)); applySetupAccent(SU.color); suProjectPreview(); });
  applySetupAccent(SU.color);
}
function suProjectPreview(){
  const p=document.getElementById("suPrev"); if(!p)return;
  p.innerHTML=`<div class="pc-top"><span class="pc-ico" style="background:${SU.color}">${(SU.name[0]||"B").toUpperCase()}</span>
    <div><div class="pc-name">${esc(SU.name||"Untitled board")}</div><div class="pc-key">${SU.key} · ${SU_WORKFLOWS[SU.preset].name}</div></div></div>
    <div class="pc-demo">${SU.key}-128 · first ticket will look like this</div>`;
}

function renderSuWorkflow(){
  const pane=document.getElementById("suPane1");
  const presets=Object.entries(SU_WORKFLOWS).map(([id,w])=>`
    <div class="su-pre ${id===SU.preset?'on':''}" data-supre="${id}">
      <span class="pre-check"><svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><path d="m5 12 5 5L20 6"/></svg></span>
      <b>${w.name}</b><p>${w.desc}</p>
      <div class="pre-flow">${w.stages.map(s=>`<span class="pchip"><span class="cd" style="background:${s[2]}"></span>${s[1]}</span>`).join("")}</div>
    </div>`).join("");
  pane.innerHTML=`
    <div class="su-eyebrow">Workflow</div>
    <h1 class="su-h">Choose your stages</h1>
    <p class="su-sub">Pick a starting template, then rename, reorder or add stages. Gates (🔒 human approval, ✓ auto-validate) control how agents hand work between stages. You can refine this any time in the Workflow builder.</p>
    <div class="su-presets">${presets}</div>
    <div class="su-stagelist" id="suStages"></div>
    <div class="su-stage-add" id="suStageAdd">+ Add a custom stage</div>`;
  pane.querySelectorAll("[data-supre]").forEach(c=>c.onclick=()=>{ SU.preset=c.dataset.supre; SU.stages=null; renderSuWorkflow(); });
  renderSuStages();
  document.getElementById("suStageAdd").onclick=()=>{ SU.stages=suStages().map(s=>[s.id,s.name,s.dot,s.gate]);
    SU.stages.splice(SU.stages.length-1,0,["s"+Date.now(),"New Stage","#2898da",null]); renderSuWorkflow(); };
}
function renderSuStages(){
  const host=document.getElementById("suStages"); const stages=suStages();
  host.innerHTML=stages.map((s,i)=>`
    <div class="su-stage" draggable="true" data-i="${i}">
      <span class="grip"><svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><circle cx="9" cy="6" r="1.5"/><circle cx="15" cy="6" r="1.5"/><circle cx="9" cy="12" r="1.5"/><circle cx="15" cy="12" r="1.5"/><circle cx="9" cy="18" r="1.5"/><circle cx="15" cy="18" r="1.5"/></svg></span>
      <span class="cd" style="background:${s.dot}"></span>
      <span class="nm" contenteditable="true" data-nm="${i}">${esc(s.name)}</span>
      <span class="badge2 ${s.gate?'gate':''}" data-gate="${i}">${s.gate==='human'?'🔒 approval':s.gate==='auto'?'✓ auto':'no gate'}</span>
      <span class="rm" data-rm="${i}"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6 6 18M6 6l12 12"/></svg></span>
    </div>`).join("");
  const ensure=()=>{ if(!SU.stages) SU.stages=suStages().map(s=>[s.id,s.name,s.dot,s.gate]); return SU.stages; };
  host.querySelectorAll("[data-nm]").forEach(el=>el.addEventListener("blur",()=>{ const a=ensure(); a[+el.dataset.nm][1]=el.textContent.trim()||a[+el.dataset.nm][1]; }));
  host.querySelectorAll("[data-nm]").forEach(el=>el.addEventListener("keydown",e=>{if(e.key==="Enter"){e.preventDefault();el.blur();}}));
  host.querySelectorAll("[data-gate]").forEach(b=>b.onclick=()=>{ const a=ensure(); const g=a[+b.dataset.gate][3];
    a[+b.dataset.gate][3]=g==='human'?'auto':g==='auto'?null:'human'; renderSuStages(); });
  host.querySelectorAll("[data-rm]").forEach(x=>x.onclick=()=>{ const a=ensure(); if(a.length<=2)return toast("Keep at least two stages"); a.splice(+x.dataset.rm,1); renderSuStages(); });
  // drag reorder
  let di=null;
  host.querySelectorAll(".su-stage").forEach(st=>{
    st.addEventListener("dragstart",()=>{di=+st.dataset.i;st.classList.add("dragging");});
    st.addEventListener("dragend",()=>{st.classList.remove("dragging");host.querySelectorAll(".su-stage").forEach(x=>x.classList.remove("drag-over"));});
    st.addEventListener("dragover",e=>{e.preventDefault();if(+st.dataset.i!==di)st.classList.add("drag-over");});
    st.addEventListener("dragleave",()=>st.classList.remove("drag-over"));
    st.addEventListener("drop",e=>{e.preventDefault();const a=ensure();const to=+st.dataset.i;const[m]=a.splice(di,1);a.splice(to,0,m);renderSuStages();});
  });
}

function renderSuAgents(){
  const pane=document.getElementById("suPane2");
  const cards=Object.entries(DB.AGENTS).map(([k,a])=>{
    const on=SU.agents.includes(k);
    return `<div class="su-ag ${on?'on':''}" data-suag="${k}">
      <span class="aav" style="background:${a.color}">${a.ini}</span>
      <div class="ai"><b>${a.name}</b><small>${a.role||a.model}</small></div>
      <span class="chk"><svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><path d="m5 12 5 5L20 6"/></svg></span></div>`;
  }).join("");
  pane.innerHTML=`
    <div class="su-eyebrow">Workforce</div>
    <h1 class="su-h">Connect your agents</h1>
    <p class="su-sub">Select which autonomous agents this board can route work to. The orchestrator assigns tickets to the best-matched agent by capability. You can connect more and fine-tune each agent later.</p>
    <div class="su-selall"><span>${SU.agents.length} of ${Object.keys(DB.AGENTS).length} connected</span><span class="lnk" id="suAll">Select all</span><span class="lnk" id="suNone">Clear</span></div>
    <div class="su-agrid">${cards}</div>`;
  pane.querySelectorAll("[data-suag]").forEach(c=>c.onclick=()=>{ const k=c.dataset.suag; const i=SU.agents.indexOf(k);
    if(i>=0)SU.agents.splice(i,1);else SU.agents.push(k); renderSuAgents(); });
  document.getElementById("suAll").onclick=()=>{ SU.agents=Object.keys(DB.AGENTS); renderSuAgents(); };
  document.getElementById("suNone").onclick=()=>{ SU.agents=[]; renderSuAgents(); };
}

function renderSuReview(){
  const pane=document.getElementById("suPane3");
  const stages=suStages();
  pane.innerHTML=`
    <div class="su-eyebrow">Review</div>
    <h1 class="su-h">Ready to launch</h1>
    <p class="su-sub">Confirm your setup. Everything here stays fully editable inside the board.</p>
    <div class="su-review">
      <div class="su-rev-card"><div class="rc-h">Project <span class="edit" data-goto="0">Edit</span></div>
        <div class="pc-top" style="display:flex;align-items:center;gap:11px">
          <span class="pc-ico" style="width:34px;height:34px;border-radius:10px;background:${SU.color};display:grid;place-items:center;color:#fff;font-weight:800">${(SU.name[0]||"B").toUpperCase()}</span>
          <div><div style="font-weight:700;font-size:15px">${esc(SU.name)}</div><div style="font-family:var(--font-mono);font-size:11.5px;color:var(--text-faint)">${SU.key} · tickets like ${SU.key}-128</div></div></div>
        ${SU.desc?`<p style="font-size:12.5px;color:var(--text-dim);margin:11px 0 0;line-height:1.5">${esc(SU.desc)}</p>`:""}
      </div>
      <div class="su-rev-card"><div class="rc-h">Workflow · ${stages.length} stages <span class="edit" data-goto="1">Edit</span></div>
        <div class="su-rev-grid">${stages.map(s=>`<span class="pchip" style="font-family:var(--font-mono);font-size:11px;padding:4px 9px;border-radius:7px;background:var(--card);border:1px solid var(--border);display:inline-flex;align-items:center;gap:6px"><span class="cd" style="width:7px;height:7px;border-radius:50%;background:${s.dot}"></span>${s.name}${s.gate?(s.gate==='human'?' 🔒':' ✓'):''}</span>`).join("")}</div></div>
      <div class="su-rev-card"><div class="rc-h">Workforce · ${SU.agents.length} agents <span class="edit" data-goto="2">Edit</span></div>
        <div class="su-rev-grid">${SU.agents.map(k=>{const a=DB.AGENTS[k];return `<span class="pchip" style="padding:4px 10px 4px 4px;border-radius:20px;background:var(--card);border:1px solid var(--border);display:inline-flex;align-items:center;gap:7px;font-size:12px"><span class="av" style="width:18px;height:18px;border-radius:5px;background:${a.color};color:#fff;font-size:9px">${a.ini}</span>${a.name}</span>`;}).join("")||'<span style="color:var(--text-faint);font-size:12.5px">No agents — orchestrator will auto-assign on demand</span>'}</div></div>
      <div class="su-launch"><div class="li"><b>Launch ${esc(SU.name)}</b><small>Your agents start picking up work the moment the board opens.</small></div>
        <svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="1.8"><path d="M5 12h14M13 6l6 6-6 6"/></svg></div>
    </div>`;
  pane.querySelectorAll("[data-goto]").forEach(e=>e.onclick=()=>{ SU.step=+e.dataset.goto; renderSetup(); });
}

function applySetupAccent(hex){
  if(window.ACCENTS && ACCENTS[hex]){ applyAccent(hex); return; }
  const r=document.documentElement.style;
  r.setProperty("--accent",hex); r.setProperty("--accent-bright",hex);
}

/* -------- nav -------- */
function suNext(){
  if(SU.step===0){ if(!SU.name.trim()){ toast("Name your board"); return; } }
  if(SU.step<3){ SU.step++; renderSetup(); document.querySelector(".su-scroll").scrollTop=0; }
  else launchBoard();
}
function suBack(){ if(SU.step>0){ SU.step--; renderSetup(); document.querySelector(".su-scroll").scrollTop=0; } }

function applyBoardConfig(){
  const stages=suStages();
  const keepFirst=stages[0].id, keepDone=stages[stages.length-1].id;
  DB.WORKFLOW.length=0;
  stages.forEach((s,i)=>DB.WORKFLOW.push({id:s.id,name:s.name,dot:s.dot,wip:0,gate:s.gate,
    rules:{autoAssign:i===1?"auto":(s.gate==="auto"?"review":null),autoValidate:s.gate==="auto"}}));
  DB.CARDS.forEach(k=>{ if(!DB.WORKFLOW.some(s=>s.id===k.col)) k.col=keepFirst; if(k.status==="done")k.col=keepDone; });
  DB.setPrefix(SU.key);
  STATE.boardAgents=SU.agents.slice();
  document.querySelectorAll("[data-boardname]").forEach(e=>e.textContent=SU.name);
  document.querySelectorAll("[data-boardkey]").forEach(e=>e.textContent=SU.key);
  applySetupAccent(SU.color);
}

function launchBoard(){
  applyBoardConfig();
  try{ localStorage.setItem("scrun_setup", JSON.stringify({done:true,name:SU.name,key:SU.key,color:SU.color,desc:SU.desc,preset:SU.preset,stages:SU.stages,agents:SU.agents})); }catch(e){}
  document.getElementById("setup").classList.remove("on");
  document.getElementById("appRoot").style.display="";
  bootApp();
  toast(SU.name+" is live");
}

function startSetup(){
  document.getElementById("setup").classList.add("on");
  document.getElementById("appRoot").style.display="none";
  SU.step=0; renderSetup();
  document.getElementById("suNext").onclick=suNext;
  document.getElementById("suBack").onclick=suBack;
}
function reconfigureBoard(){
  const saved=loadSetup();
  if(saved){ Object.assign(SU,{name:saved.name,key:saved.key,color:saved.color,desc:saved.desc||"",preset:saved.preset||"dev",stages:saved.stages||null,agents:saved.agents||Object.keys(DB.AGENTS).slice(0,5)}); }
  startSetup();
}
function loadSetup(){ try{ return JSON.parse(localStorage.getItem("scrun_setup")||"null"); }catch(e){ return null; } }
