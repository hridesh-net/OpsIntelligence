/* ============================================================
   SCRUN — Task create / edit form (centered modal)
   title · type · priority · stage · agents · description ·
   acceptance criteria · labels
   ============================================================ */
let tfDraft=null, tfIsNew=false;

const TYPE_OPTS=[["feat","Feature"],["fix","Fix"],["infra","Infra"],["sec","Security"],["research","Research"],["chore","Chore"]];
const PRIO_OPTS=[["H","High"],["M","Medium"],["L","Low"]];

function openTaskForm(id, colId){
  tfIsNew=!id;
  if(id){
    const k=DB.CARDS.find(x=>x.id===id);
    tfDraft=JSON.parse(JSON.stringify(k));
  } else {
    tfDraft={id:DB.nextId(),col:colId||"backlog",type:"feat",prio:"M",title:"",agents:[],
      status:"queued",labels:[],desc:"",ac:[],when:"now",logs:[],progress:0};
  }
  if(!tfDraft.ac) tfDraft.ac=[];
  renderTaskForm();
  document.getElementById("taskOverlay").classList.add("open");
}
function closeTaskForm(){ document.getElementById("taskOverlay").classList.remove("open"); tfDraft=null; }

function tfSeg(field,opts){
  return `<div class="segrow">${opts.map(o=>`<button data-tfseg="${field}" data-val="${o[0]}" class="${o[0]===tfDraft[field]?'on':''}">${o[1]}</button>`).join("")}</div>`;
}

function renderTaskForm(){
  const d=tfDraft, modal=document.getElementById("taskModal");
  const stageOpts=DB.WORKFLOW.map(s=>`<option value="${s.id}" ${s.id===d.col?'selected':''}>${s.name}</option>`).join("");
  const agentChips=Object.entries(DB.AGENTS).map(([k,a])=>{
    const on=d.agents.includes(k);
    return `<span class="apick ${on?'on':''}" data-agpick="${k}"><span class="av" style="background:${a.color}">${a.ini}</span>${a.name}${on?' <span class="chk">✓</span>':''}</span>`;
  }).join("");

  modal.innerHTML=`
    <div class="p-head">
      <div class="p-top">
        <span class="cid">${tfIsNew?'New ticket':'#'+d.id}</span>
        <span class="ttag ${d.type}" id="tfTypePreview" style="margin-left:2px">${(TYPE_OPTS.find(t=>t[0]===d.type)||['',''])[1]}</span>
        <span class="p-close" id="tfClose"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6 6 18M6 6l12 12"/></svg></span>
      </div>
      <div class="p-title" style="font-size:13px;color:var(--text-faint);font-weight:600;text-transform:uppercase;letter-spacing:.08em;margin-bottom:14px">${tfIsNew?'Create task':'Edit task'}</div>
    </div>
    <div class="p-body">
      <div class="fld"><label class="fld-l">Title <span class="tip">required</span></label>
        <input class="inp" id="tfTitle" value="${esc(d.title)}" placeholder="Short, action-oriented summary of the work" /></div>

      <div class="cfg-grid">
        <div class="cfg-col">
          <div class="fld"><label class="fld-l">Work type</label>${tfSeg("type",TYPE_OPTS)}</div>
          <div class="fld"><label class="fld-l">Priority</label>${tfSeg("prio",PRIO_OPTS)}</div>
          <div class="fld"><label class="fld-l">Workflow stage</label>
            <select class="inp" id="tfStage">${stageOpts}</select></div>
        </div>
        <div class="cfg-col">
          <div class="fld"><label class="fld-l">Assign agents</label>
            <div class="apick-grid" id="tfAgents">${agentChips}</div>
            <div class="fld-hint">Leave empty to let the orchestrator auto-assign on pickup.</div></div>
        </div>
      </div>

      <div class="fld"><label class="fld-l">Description</label>
        <textarea class="inp" id="tfDesc" rows="4" placeholder="What needs to be done and why. Context the agent should know.">${esc(d.desc)}</textarea></div>

      <div class="fld"><label class="fld-l">Acceptance criteria <span class="tip">enter to add</span></label>
        <div class="ac-list" id="tfAcList">
          ${d.ac.map((a,i)=>acRowHTML(a,i)).join("")}
        </div>
        <div class="ac-add"><span class="ac-box">＋</span><input id="tfAcInput" placeholder="Add a criterion the agent must satisfy…" /></div>
        <div class="fld-hint">The agent treats these as a definition-of-done checklist before requesting review.</div></div>

      <div class="fld"><label class="fld-l">Labels <span class="tip">enter to add</span></label>
        <div class="capedit" id="tfLabels">
          ${d.labels.map((l,i)=>`<span class="cap">${l}<span class="x" data-rmlab="${i}">✕</span></span>`).join("")}
          <input id="tfLabelInput" placeholder="add label…" />
        </div></div>
    </div>
    <div class="p-actions">
      ${tfIsNew?'':'<button class="btn" data-tfdel style="margin-right:auto;color:var(--danger);border-color:var(--danger-soft)">Delete</button>'}
      <button class="btn" id="tfCancel">Cancel</button>
      <button class="btn btn-primary" id="tfSave">${tfIsNew?'Create task':'Save changes'}</button>
    </div>`;

  wireTaskForm();
}

function acRowHTML(a,i){
  return `<div class="ac-row"><span class="ac-box"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><path d="m5 12 5 5L20 6"/></svg></span>
    <span class="ac-txt">${esc(a)}</span><span class="ac-x" data-rmac="${i}"><svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6 6 18M6 6l12 12"/></svg></span></div>`;
}
function esc(s){ return (s||"").replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;"); }

function wireTaskForm(){
  const modal=document.getElementById("taskModal");
  document.getElementById("tfClose").onclick=closeTaskForm;
  document.getElementById("tfCancel").onclick=closeTaskForm;
  document.getElementById("tfSave").onclick=saveTaskForm;
  const del=modal.querySelector("[data-tfdel]"); if(del) del.onclick=()=>deleteTask(tfDraft.id);

  document.getElementById("tfTitle").addEventListener("input",e=>tfDraft.title=e.target.value);
  document.getElementById("tfDesc").addEventListener("input",e=>tfDraft.desc=e.target.value);
  document.getElementById("tfStage").addEventListener("change",e=>tfDraft.col=e.target.value);

  modal.querySelectorAll("[data-tfseg]").forEach(b=>b.onclick=()=>{
    const f=b.dataset.tfseg; tfDraft[f]=b.dataset.val;
    b.parentElement.querySelectorAll("button").forEach(x=>x.classList.remove("on")); b.classList.add("on");
    if(f==="type"){ const p=document.getElementById("tfTypePreview"); p.className="ttag "+b.dataset.val; p.textContent=(TYPE_OPTS.find(t=>t[0]===b.dataset.val)||['',''])[1]; }
  });

  modal.querySelectorAll("[data-agpick]").forEach(c=>c.onclick=()=>{
    const k=c.dataset.agpick; const i=tfDraft.agents.indexOf(k);
    if(i>=0) tfDraft.agents.splice(i,1); else tfDraft.agents.push(k);
    renderTaskForm();
  });

  // acceptance criteria
  const acIn=document.getElementById("tfAcInput");
  acIn.addEventListener("keydown",e=>{ if(e.key==="Enter"&&acIn.value.trim()){ tfDraft.ac.push(acIn.value.trim()); renderTaskForm(); document.getElementById("tfAcInput").focus(); } });
  modal.querySelectorAll("[data-rmac]").forEach(x=>x.onclick=()=>{ tfDraft.ac.splice(+x.dataset.rmac,1); renderTaskForm(); });

  // labels
  const labIn=document.getElementById("tfLabelInput");
  labIn.addEventListener("keydown",e=>{
    if(e.key==="Enter"&&labIn.value.trim()){ tfDraft.labels.push(labIn.value.trim().toLowerCase()); renderTaskForm(); document.getElementById("tfLabelInput").focus(); }
    else if(e.key==="Backspace"&&!labIn.value&&tfDraft.labels.length){ tfDraft.labels.pop(); renderTaskForm(); document.getElementById("tfLabelInput").focus(); }
  });
  modal.querySelectorAll("[data-rmlab]").forEach(x=>x.onclick=()=>{ tfDraft.labels.splice(+x.dataset.rmlab,1); renderTaskForm(); });
}

function saveTaskForm(){
  if(!tfDraft.title.trim()){ toast("Give the ticket a title"); document.getElementById("tfTitle").focus(); return; }
  if(tfIsNew){
    if(!tfDraft.agents.length) tfDraft.agents=["devops"];
    const created=tfDraft;
    DB.CARDS.push(created);
    logActivity(created,"move",`created in <b>${DB.stage(created.col).name}</b>`);
    closeTaskForm(); if(STATE.screen!=="board")go("board"); renderBoard();
    openPanel(created.id);
    toast("Ticket #"+created.id+" created");
  } else {
    const k=DB.CARDS.find(x=>x.id===tfDraft.id);
    Object.assign(k,tfDraft);
    closeTaskForm(); renderBoard();
    if(STATE.selectedId===k.id) renderPanel();
    toast("Ticket updated");
  }
}
function deleteTask(id){
  const i=DB.CARDS.findIndex(x=>x.id===id);
  if(i>=0) DB.CARDS.splice(i,1);
  closeTaskForm(); if(STATE.selectedId===id) closePanel(); renderBoard();
  toast("Ticket deleted");
}
