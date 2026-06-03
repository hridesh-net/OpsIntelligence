/* ============================================================
   SCRUN — Workflows builder · Agent Manager · Activity feed
   ============================================================ */

// Server IDs of stages the user has removed since the last Save. The
// Save handler ships these in `deleted` so the backend can DELETE the
// corresponding board_columns rows in one round trip; we keep it on
// the module scope so individual delete clicks don't have to thread
// state through the render closure.
let _pendingDeletedColumnIds = [];

function workflowGateName(g){
  if (g === "human") return "human";
  if (g === "auto") return "auto-validate";
  return "none";
}

async function saveWorkflowToServer(){
  const demoMode = localStorage.getItem("scrunDemoMode") === "1";
  if (demoMode || !window.ScrunAPI || !window.currentBoardID) {
    toast("Workflow saved");
    return;
  }
  const cols = DB.WORKFLOW.map((s, idx) => ({
    id:         s._serverId || undefined, // omit for new stages
    name:       s.name,
    position:   idx,
    color:      s.dot,
    wip_limit:  s.wip ? s.wip : null,
    gate:       workflowGateName(s.gate),
    automation: s.rules || {},
  }));
  try {
    const res = await window.ScrunAPI.saveWorkflow(window.currentBoardID, {
      columns: cols,
      deleted: _pendingDeletedColumnIds,
    });
    // Re-sync the local IDs so future drag-renames target the right rows.
    if (res && res.columns) {
      res.columns.forEach((c, i) => {
        if (DB.WORKFLOW[i]) {
          DB.WORKFLOW[i]._serverId = c.id;
          DB.WORKFLOW[i].id = c.id;
        }
      });
    }
    _pendingDeletedColumnIds = [];
    toast("Workflow saved");
  } catch (e) {
    if (e && e.blocked && e.blocked.length) {
      toast("Cannot delete stages that still hold cards");
    } else {
      toast("Save failed: " + (e && e.message ? e.message : "network error"));
    }
  }
}

/* ---------------- WORKFLOWS ---------------- */
function renderWorkflows(){
  const host=document.getElementById("workflowsBody");
  let stagesHTML="";
  DB.WORKFLOW.forEach((s,i)=>{
    const count=DB.CARDS.filter(k=>k.col===s.id).length;
    const rules=[];
    if(s.rules.autoAssign==="auto") rules.push(`<div class="rl"><span class="ri"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2v4M12 18v4M2 12h4M18 12h4"/><circle cx="12" cy="12" r="3"/></svg></span>Auto-assign best agent on entry</div>`);
    if(s.rules.autoAssign==="review") rules.push(`<div class="rl"><span class="ri"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m9 11 3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/></svg></span>Route to <b>Code Review Agent</b></div>`);
    if(s.rules.autoAssign==="keep") rules.push(`<div class="rl"><span class="ri"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M5 12h14M12 5v14"/></svg></span>Keep current agent working</div>`);
    if(s.rules.autoValidate) rules.push(`<div class="rl"><span class="ri"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m9 11 3 3L22 4"/></svg></span>Auto-validate when tests pass</div>`);
    if(!rules.length) rules.push(`<div class="rl" style="color:var(--text-faint)">No automation rules</div>`);

    stagesHTML+=`
      <div class="stage" draggable="true" data-stage="${s.id}">
        <div class="stage-top">
          <span class="grip"><svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><circle cx="9" cy="6" r="1.6"/><circle cx="15" cy="6" r="1.6"/><circle cx="9" cy="12" r="1.6"/><circle cx="15" cy="12" r="1.6"/><circle cx="9" cy="18" r="1.6"/><circle cx="15" cy="18" r="1.6"/></svg></span>
          <span class="cdot" style="background:${s.dot}"></span>
          <span class="sname" contenteditable="true" data-sid="${s.id}">${s.name}</span>
          <span class="sidx">0${i+1}</span>
        </div>
        <div class="stage-meta">
          <span class="chip ${s.wip?'on':''}" data-wip="${s.id}"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12h18M3 6h18M3 18h18"/></svg>${s.wip?'WIP '+s.wip:'No WIP'}</span>
          <span class="chip ${s.gate==='human'?'on':''}" data-gate="${s.id}">${s.gate==='human'?'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/></svg>Human gate':s.gate==='auto'?'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m9 11 3 3L22 4"/></svg>Auto gate':'No gate'}</span>
        </div>
        <div class="stage-rule">${rules.join("")}</div>
        <div class="stage-foot">
          <span class="sf-count">${count} task${count!==1?'s':''}</span>
          <span class="sf-actions">
            <span data-dye="${s.id}" title="Colour"><svg width="13" height="13" viewBox="0 0 24 24" fill="${s.dot}" stroke="${s.dot}"><circle cx="12" cy="12" r="7"/></svg></span>
            <span data-del="${s.id}" title="Delete stage"><svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14"/></svg></span>
          </span>
        </div>
      </div>`;
    if(i<DB.WORKFLOW.length-1){
      const nextGate=DB.WORKFLOW[i+1].gate;
      stagesHTML+=`<div class="transition ${nextGate?'gated':''}"><div class="glabel">${nextGate==='human'?'approve':nextGate==='auto'?'validate':'auto'}</div><div class="arrow"></div></div>`;
    }
  });
  stagesHTML+=`<div class="stage-add" id="addStage">+</div>`;

  host.innerHTML=`
    <div class="flow">${stagesHTML}</div>
    <div class="wf-legend">
      <div class="lg"><span class="sw" style="background:var(--border-strong)"></span>Auto transition</div>
      <div class="lg"><span class="sw" style="background:var(--warning)"></span>Gated transition (approval / validate)</div>
      <div class="lg"><span class="sw" style="background:var(--accent)"></span>Automation rule active</div>
    </div>
    <div style="margin-top:26px">
      <div class="p-lbl" style="margin-bottom:10px">Start from a preset</div>
      <div class="wf-presets">
        <div class="preset" data-preset="dev"><b>Software delivery</b><small>backlog → todo → build → test → review → done</small></div>
        <div class="preset" data-preset="research"><b>Research spike</b><small>intake → analyse → synthesise → report</small></div>
        <div class="preset" data-preset="support"><b>Triage & resolve</b><small>inbox → triage → fix → verify → closed</small></div>
      </div>
    </div>`;

  wireWorkflows(host);

  // The Save button lives outside #workflowsBody (in the shead) so its
  // click handler is wired once per renderWorkflows() call. Idempotent
  // — re-assigning .onclick replaces the previous binding.
  const saveBtn = document.getElementById("workflowSave");
  if (saveBtn) saveBtn.onclick = saveWorkflowToServer;
}

function wireWorkflows(host){
  // editable names
  host.querySelectorAll(".sname").forEach(el=>{
    el.addEventListener("blur",()=>{const s=DB.stage(el.dataset.sid); if(s){s.name=el.textContent.trim()||s.name;} });
    el.addEventListener("keydown",e=>{if(e.key==="Enter"){e.preventDefault();el.blur();}});
  });
  // toggle WIP / gate
  host.querySelectorAll("[data-wip]").forEach(c=>c.addEventListener("click",()=>{
    const s=DB.stage(c.dataset.wip); s.wip=s.wip?0:4; renderWorkflows(); }));
  host.querySelectorAll("[data-gate]").forEach(c=>c.addEventListener("click",()=>{
    const s=DB.stage(c.dataset.gate); s.gate=s.gate==='human'?'auto':s.gate==='auto'?null:'human'; renderWorkflows(); }));
  host.querySelectorAll("[data-dye]").forEach(c=>c.addEventListener("click",()=>{
    const s=DB.stage(c.dataset.dye); const palette=["#586675","#2898da","#4db0ef","#a78bfa","#f5b042","#34d399","#f4685f","#2dd4bf"];
    s.dot=palette[(palette.indexOf(s.dot)+1)%palette.length]; renderWorkflows(); }));
  host.querySelectorAll("[data-del]").forEach(c=>c.addEventListener("click",()=>{
    if(DB.WORKFLOW.length<=2) return toast("Keep at least two stages");
    const id=c.dataset.del; const idx=DB.WORKFLOW.findIndex(s=>s.id===id);
    const fallback=DB.WORKFLOW[idx-1]||DB.WORKFLOW[idx+1];
    DB.CARDS.forEach(k=>{if(k.col===id)k.col=fallback.id;});
    const removed = DB.WORKFLOW.splice(idx,1)[0];
    // Track for the next Save round-trip so the backend can DELETE
    // the column row. Stages added in this session that never made it
    // to the server (no _serverId) just disappear locally.
    if (removed && removed._serverId) {
      _pendingDeletedColumnIds.push(removed._serverId);
    }
    renderWorkflows(); toast("Stage removed"); }));
  document.getElementById("addStage").onclick=()=>{
    const id="stage"+Date.now();
    DB.WORKFLOW.splice(DB.WORKFLOW.length-1,0,{id,name:"New Stage",dot:"#2898da",wip:0,gate:null,rules:{autoAssign:null,autoValidate:false}});
    renderWorkflows(); toast("Stage added");
  };
  host.querySelectorAll(".preset").forEach(p=>p.addEventListener("click",()=>applyPreset(p.dataset.preset)));
  // drag reorder
  let dragId=null;
  host.querySelectorAll(".stage").forEach(st=>{
    st.addEventListener("dragstart",e=>{dragId=st.dataset.stage;st.classList.add("dragging");e.dataTransfer.effectAllowed="move";});
    st.addEventListener("dragend",()=>{st.classList.remove("dragging");host.querySelectorAll(".stage").forEach(x=>x.classList.remove("drag-over"));});
    st.addEventListener("dragover",e=>{e.preventDefault();if(st.dataset.stage!==dragId)st.classList.add("drag-over");});
    st.addEventListener("dragleave",()=>st.classList.remove("drag-over"));
    st.addEventListener("drop",e=>{e.preventDefault();
      const from=DB.WORKFLOW.findIndex(s=>s.id===dragId), to=DB.WORKFLOW.findIndex(s=>s.id===st.dataset.stage);
      if(from<0||to<0||from===to)return;
      const[m]=DB.WORKFLOW.splice(from,1); DB.WORKFLOW.splice(to,0,m); renderWorkflows();});
  });
}

function applyPreset(p){
  const sets={
    dev:[["backlog","Backlog","#586675"],["todo","To Do","#2898da"],["build","Build","#4db0ef"],["test","Test","#a78bfa"],["review","Review","#f5b042"],["done","Done","#34d399"]],
    research:[["intake","Intake","#586675"],["analyse","Analyse","#2898da"],["synth","Synthesise","#a78bfa"],["report","Report","#34d399"]],
    support:[["inbox","Inbox","#586675"],["triage","Triage","#f5b042"],["fix","Fix","#2898da"],["verify","Verify","#a78bfa"],["closed","Closed","#34d399"]],
  };
  const def=sets[p]; DB.WORKFLOW.length=0;
  def.forEach((d,i)=>DB.WORKFLOW.push({id:d[0],name:d[1],dot:d[2],wip:0,
    gate:i===def.length-2?'human':null,rules:{autoAssign:i===1?'auto':null,autoValidate:false}}));
  DB.CARDS.forEach(k=>{if(!DB.WORKFLOW.some(s=>s.id===k.col))k.col=DB.WORKFLOW[0].id;});
  renderWorkflows(); toast("Preset applied"); refreshBoardCounts();
}

/* ---------------- AGENT MANAGER ---------------- */
function renderAgents(){
  const host=document.getElementById("agentsBody");
  let cards="";
  Object.entries(DB.AGENTS).forEach(([ak,a])=>{
    const cur=DB.CARDS.find(k=>k.agents&&k.agents.includes(ak)&&(k.status==="running"||k.status==="awaiting"));
    const st=DB.AGENT_STATS[ak]||{tasks:0,success:0,spend:0};
    const state=cur?(cur.status==="awaiting"?"working":"working"):(st.tasks%7===0?"offline":"idle");
    const busy=!!cur;
    cards+=`
      <div class="acard ${busy?'busy':''}">
        <div class="acard-top">
          <span class="aav" style="background:${a.color}">${a.ini}</span>
          <div class="ainfo"><b>${a.name}</b><span class="amodel">${a.role}</span></div>
          <span class="astate ${state}"><span class="d"></span>${state==='working'?'Working':state==='idle'?'Idle':'Offline'}</span>
        </div>
        <div class="acur ${cur?'':'idle-task'}">
          ${cur?`<span class="cid">#${cur.id}</span> ${cur.title}`:`Idle — waiting for the orchestrator to route work`}
        </div>
        <div class="acap">${a.caps.map(c=>`<span class="cap">${c}</span>`).join("")}</div>
        <div class="acap" style="margin-top:-4px">
          <span class="cap" style="color:var(--text-dim)">${a.model}</span>
          <span class="cap" style="color:var(--text-dim)" title="Memory">⌬ ${a.memory.mode} · ${a.memory.contextK}K</span>
          <span class="cap" style="color:var(--text-dim)" title="Autonomy">${({supervised:'supervised',auto:'auto-validate',full:'full auto'})[a.autonomy]}</span>
        </div>
        <div class="astats">
          <div class="as"><div class="v">${st.tasks}</div><div class="l">Tasks</div></div>
          <div class="as"><div class="v" style="color:var(--success)">${st.success}%</div><div class="l">Success</div></div>
          <div class="as"><div class="v">$${st.spend.toFixed(0)}</div><div class="l">Spend</div></div>
        </div>
        <div class="acard-foot">
          <button class="btn" data-acfg="${ak}">Configure</button>
          <button class="btn ${state==='offline'?'btn-primary':''}" data-atog="${ak}">${state==='offline'?'Connect':'Pause'}</button>
        </div>
      </div>`;
  });
  cards+=`<div class="acard add-agent" id="addAgent"><div class="plus">+</div><b style="color:var(--text)">Connect agent</b><small style="display:block;color:var(--text-faint);font-size:11px;margin-top:4px;font-family:var(--font-mono)">OpenAI · Anthropic · Meta · custom</small></div>`;
  host.innerHTML=`<div class="agrid">${cards}</div>`;
  host.querySelectorAll("[data-acfg]").forEach(b=>b.onclick=()=>openAgentConfig(b.dataset.acfg));
  host.querySelectorAll("[data-atog]").forEach(b=>b.onclick=()=>toast(b.textContent+"d "+DB.AGENTS[b.dataset.atog].name));
  document.getElementById("addAgent").onclick=()=>toast("Connect a new agent endpoint");
}

/* ---------------- ACTIVITY FEED ---------------- */
function renderActivity(){
  const host=document.getElementById("activityBody");
  const items=DB.ACTIVITY.slice(0,40);
  const list=items.map(activityItemHTML).join("")||`<div class="p-desc" style="color:var(--text-faint)">No activity yet — the agents are warming up.</div>`;
  const running=DB.CARDS.filter(k=>k.status==="running");
  host.innerHTML=`
    <div class="feed-main">
      <div class="feed-day">Today · live</div>
      <div class="feed-list" id="feedList">${list}</div>
    </div>
    <aside class="feed-side">
      <h3>Live summary</h3>
      <div class="sb-stat"><span class="l">Agents active</span><span class="v">${new Set(DB.CARDS.flatMap(k=>k.status==="running"?k.agents:[])).size}</span></div>
      <div class="sb-stat"><span class="l">Tasks running</span><span class="v">${running.length}</span></div>
      <div class="sb-stat"><span class="l">Awaiting human</span><span class="v" style="color:var(--warning)">${DB.CARDS.filter(k=>k.status==="awaiting").length}</span></div>
      <div class="sb-stat"><span class="l">Spend today</span><span class="v">$${totalSpend().toFixed(2)}</span></div>
      <h3 style="margin-top:22px">In flight</h3>
      ${running.map(k=>`<div class="mini-agent">${av(k.agents[0])}<span class="mname">#${k.id}</span><span class="mbar"><i style="width:${k.progress||0}%"></i></span><span class="mpct">${k.progress||0}%</span></div>`).join("")||'<div class="p-desc" style="color:var(--text-faint);font-size:12px">Nothing running</div>'}
    </aside>`;
}

function activityItemHTML(e){
  const a=DB.AGENTS[e.agent];
  return `<div class="fitem">
    <span class="fdot" style="background:${a.color}">${a.ini}</span>
    <div class="ftop"><span class="fname">${a.name}</span><span class="ftag ${e.tag}">${e.tag}</span><span class="ftime">${e.time}</span></div>
    <div class="fbody"><span class="cid" data-open="${e.id}">#${e.id}</span> ${e.text}</div>
    ${e.meta?`<div class="fmeta">${e.meta}</div>`:""}
  </div>`;
}
function refreshBoardCounts(){ if(STATE.screen==="board") renderBoard(); }
