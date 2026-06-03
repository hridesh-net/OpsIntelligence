/* ============================================================
   SCRUN — Agent configuration modal
   role · capabilities · personal context · memory · guardrails
   ============================================================ */
let acKey=null, acDraft=null;

function openAgentConfig(key){
  acKey=key;
  acDraft=JSON.parse(JSON.stringify(DB.AGENTS[key]));   // deep working copy
  renderAgentConfig();
  document.getElementById("agentOverlay").classList.add("open");
}
function closeAgentConfig(){ document.getElementById("agentOverlay").classList.remove("open"); acKey=null; acDraft=null; }

function acSetPath(path,val){
  const parts=path.split("."); let o=acDraft;
  for(let i=0;i<parts.length-1;i++) o=o[parts[i]];
  o[parts[parts.length-1]]=val;
}
function acSeg(field,opts,cur){
  return `<div class="segrow" data-segrow>${opts.map(o=>`<button data-seg="${field}" data-val="${o[0]}" class="${o[0]===cur?'on':''}">${o[1]}</button>`).join("")}</div>`;
}

function renderAgentConfig(){
  const d=acDraft, a=DB.AGENTS[acKey];
  const modal=document.getElementById("agentModal");
  const memDesc={persistent:"Remembers across sessions & tasks",session:"Remembers only within a session",none:"Stateless — no memory between runs"}[d.memory.mode];

  modal.innerHTML=`
    <div class="p-head">
      <div class="p-top">
        <span class="aav" style="width:34px;height:34px;border-radius:10px;background:${a.color};display:grid;place-items:center;font-family:var(--font-mono);font-weight:700;color:#fff;font-size:12px">${a.ini}</span>
        <div style="flex:1;min-width:0">
          <input class="inp" data-bind="name" value="${d.name}" style="font-size:16px;font-weight:700;padding:4px 8px;background:transparent;border-color:transparent" />
          <span class="modal-sub" style="padding-left:8px">${d.model} · ${d.provider}</span>
        </div>
        <span class="p-close" id="acClose"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6 6 18M6 6l12 12"/></svg></span>
      </div>
      <div class="p-title" style="font-size:13px;color:var(--text-faint);font-weight:600;text-transform:uppercase;letter-spacing:.08em;margin-bottom:14px">Agent configuration</div>
    </div>
    <div class="p-body">
      <div class="cfg-grid">
        <div class="cfg-col">
          <div class="fld"><label class="fld-l">Role / assignment</label>
            <input class="inp" data-bind="role" value="${d.role}" placeholder="e.g. Infrastructure Engineer" /></div>

          <div class="fld"><label class="fld-l">Capabilities <span class="tip">enter to add</span></label>
            <div class="capedit" id="acCaps">
              ${d.caps.map((c,i)=>`<span class="cap">${c}<span class="x" data-rmcap="${i}">✕</span></span>`).join("")}
              <input id="acCapInput" placeholder="add capability…" />
            </div>
            <div class="fld-hint">Defines what work the orchestrator routes to this agent.</div></div>

          <div class="fld"><label class="fld-l">Model</label>
            <select class="inp" data-bind="model">
              ${["claude-opus-4.7","claude-sonnet-4.6","gpt-4-turbo","gpt-4.1","llama-3.3-70b","gemini-2.0-pro"].map(m=>`<option ${m===d.model?'selected':''}>${m}</option>`).join("")}
            </select></div>

          <div class="fld"><label class="fld-l">Autonomy level</label>
            ${acSeg("autonomy",[["supervised","Supervised"],["auto","Auto-validate"],["full","Full auto"]],d.autonomy)}
            <div class="fld-hint">${({supervised:"Pauses for human approval at gates.",auto:"Self-validates when tests pass; humans gate risk.",full:"Acts end-to-end without human gates."})[d.autonomy]}</div></div>

          <div class="fld"><label class="fld-l">Daily spend cap</label>
            <div class="rng-row"><span>Budget / day</span><b>$${d.spendCap}</b></div>
            <input type="range" class="rng" data-bind="spendCap" data-fmt="$" min="5" max="100" step="1" value="${d.spendCap}" /></div>

          <div class="fld"><label class="fld-l">Max parallel tasks</label>
            <div class="rng-row"><span>Concurrency</span><b data-out="maxParallel">${d.maxParallel}</b></div>
            <input type="range" class="rng" data-bind="maxParallel" min="1" max="6" step="1" value="${d.maxParallel}" /></div>
        </div>

        <div class="cfg-col">
          <div class="fld"><label class="fld-l">Personal context · system instructions</label>
            <textarea class="inp" data-bind="instructions" rows="6">${d.instructions}</textarea>
            <div class="fld-hint">The agent's persona and standing rules — prepended to every task it runs.</div></div>

          <div class="fld"><label class="fld-l">Knowledge sources</label>
            <div class="know" id="acKnow">
              ${d.knowledge.map((k,i)=>`<div class="ksrc"><span class="kico"><svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 19V5a2 2 0 0 1 2-2h9l5 5v11a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2Z"/><path d="M14 3v5h5"/></svg></span><span class="kname">${k[0]}</span><span class="kmeta">${k[1]}</span><span class="x" data-rmknow="${i}"><svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6 6 18M6 6l12 12"/></svg></span></div>`).join("")}
            </div>
            <div class="know-add" id="acKnowAdd">+ Connect knowledge source</div></div>

          <div class="fld"><label class="fld-l">Memory</label>
            ${acSeg("memory.mode",[["persistent","Persistent"],["session","Session"],["none","None"]],d.memory.mode)}
            <div class="fld-hint" id="acMemDesc">${memDesc}</div></div>

          <div class="fld" id="acMemDetail" style="${d.memory.mode==='none'?'display:none':''}">
            <label class="fld-l">Memory scope</label>
            ${acSeg("memory.scope",[["project","Per-project"],["global","Global"],["task","Per-task"]],d.memory.scope)}
            <div style="height:14px"></div>
            <label class="fld-l">Working context window</label>
            <div class="rng-row"><span>Tokens held in context</span><b data-out="memory.contextK">${d.memory.contextK}K</b></div>
            <input type="range" class="rng" data-bind="memory.contextK" data-suffix="K" min="16" max="256" step="16" value="${d.memory.contextK}" />
            <div style="height:14px"></div>
            <label class="fld-l">Retention</label>
            ${acSeg("memory.retention",[["7d","7 days"],["30d","30 days"],["90d","90 days"],["forever","Forever"]],d.memory.retention)}</div>
        </div>
      </div>
    </div>
    <div class="p-actions">
      <button class="btn" id="acCancel">Cancel</button>
      <button class="btn btn-primary" id="acSave">Save configuration</button>
    </div>`;

  wireAgentConfig();
}

function wireAgentConfig(){
  const modal=document.getElementById("agentModal");
  document.getElementById("acClose").onclick=closeAgentConfig;
  document.getElementById("acCancel").onclick=closeAgentConfig;
  document.getElementById("acSave").onclick=saveAgentConfig;

  // text / textarea / select binds
  modal.querySelectorAll("[data-bind]").forEach(el=>{
    if(el.type==="range") return;
    el.addEventListener("input",()=>acSetPath(el.dataset.bind, el.value));
  });
  // range binds + live output
  modal.querySelectorAll("input[type=range][data-bind]").forEach(el=>{
    el.addEventListener("input",()=>{
      const v=+el.value; acSetPath(el.dataset.bind,v);
      const out=modal.querySelector(`[data-out="${el.dataset.bind}"]`);
      if(out) out.textContent=v+(el.dataset.suffix||"");
      const rowB=el.parentElement.querySelector(".rng-row b[data-fmt]")||el.parentElement.querySelector(".rng-row b");
      if(el.dataset.fmt && rowB && !rowB.dataset.out) rowB.textContent=el.dataset.fmt+v;
    });
  });
  // segmented
  modal.querySelectorAll("[data-seg]").forEach(btn=>btn.addEventListener("click",()=>{
    const field=btn.dataset.seg;
    btn.parentElement.querySelectorAll("button").forEach(b=>b.classList.remove("on"));
    btn.classList.add("on");
    acSetPath(field,btn.dataset.val);
    if(field==="memory.mode"){
      document.getElementById("acMemDesc").textContent={persistent:"Remembers across sessions & tasks",session:"Remembers only within a session",none:"Stateless — no memory between runs"}[btn.dataset.val];
      document.getElementById("acMemDetail").style.display=btn.dataset.val==="none"?"none":"";
    }
    if(field==="autonomy"){ renderAgentConfig(); } // refresh hint text
  }));
  // capabilities
  const capInput=document.getElementById("acCapInput");
  capInput.addEventListener("keydown",e=>{
    if(e.key==="Enter" && capInput.value.trim()){ acDraft.caps.push(capInput.value.trim().toLowerCase()); renderAgentConfig(); document.getElementById("acCapInput").focus(); }
    else if(e.key==="Backspace" && !capInput.value && acDraft.caps.length){ acDraft.caps.pop(); renderAgentConfig(); document.getElementById("acCapInput").focus(); }
  });
  modal.querySelectorAll("[data-rmcap]").forEach(x=>x.onclick=()=>{ acDraft.caps.splice(+x.dataset.rmcap,1); renderAgentConfig(); });
  // knowledge
  modal.querySelectorAll("[data-rmknow]").forEach(x=>x.onclick=()=>{ acDraft.knowledge.splice(+x.dataset.rmknow,1); renderAgentConfig(); });
  document.getElementById("acKnowAdd").onclick=()=>{ acDraft.knowledge.push(["New source","linked"]); renderAgentConfig(); };
}

async function saveAgentConfig(){
  // Stage the changes locally so the UI updates feel instant.
  Object.assign(DB.AGENTS[acKey], acDraft);
  const nm=DB.AGENTS[acKey].name;

  // In live mode persist to the server. The agent map is keyed by
  // server id (set in api.js#loadFirstBoard), so acKey is the id we
  // PUT against. acDraft carries the editable fields; the rest of
  // BoardAgent (id, board_id, created_at) is owned by the server.
  const demoMode = localStorage.getItem("scrunDemoMode") === "1";
  if (!demoMode && window.ScrunAPI && window.currentBoardID) {
    const cfgPatch = {
      role:         acDraft.role,
      instructions: acDraft.instructions,
      capabilities: acDraft.caps,
      knowledge:    acDraft.knowledge,
      memory:       acDraft.memory,
      autonomy:     acDraft.autonomy,
      spend_cap_daily: acDraft.spendCap,
      max_parallel: acDraft.maxParallel,
      model:        acDraft.model,
      provider:     acDraft.provider,
      color:        acDraft.color,
      ini:          acDraft.ini,
    };
    try {
      await window.ScrunAPI.updateAgent(window.currentBoardID, acKey, {
        name: acDraft.name,
        config: cfgPatch,
      });
      toast(nm+" configuration saved");
    } catch (e) {
      toast("Save failed: " + (e && e.message ? e.message : "network error"));
    }
  } else {
    toast(nm+" configuration saved");
  }

  closeAgentConfig();
  if(STATE.screen==="agents") renderAgents();
  if(STATE.screen==="board") renderBoard();
}
