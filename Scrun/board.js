/* ============================================================
   SCRUN — Board rendering: columns / compact / swimlanes + drag
   ============================================================ */

function av(key, stack){
  const a=DB.AGENTS[key];
  return `<span class="av${stack?' stack':''}" style="background:${a.color}" title="${a.name}">${a.ini}</span>`;
}
const STATUS_LABEL={running:"Running",awaiting:"Awaiting",queued:"Queued",blocked:"Blocked",done:"Done"};
const TYPE_LABEL={feat:"feat",fix:"fix",chore:"chore",infra:"infra",sec:"sec",research:"rsch"};

function cardVisible(k){
  const f=STATE.filters;
  if(f.agent!=="all" && !k.agents.includes(f.agent)) return false;
  if(f.prio!=="all" && k.prio!==f.prio) return false;
  if(f.type!=="all" && k.type!==f.type) return false;
  if(f.q){
    const q=f.q.toLowerCase();
    const hit=k.title.toLowerCase().includes(q)||k.id.toLowerCase().includes(q)||
      (k.branch||"").toLowerCase().includes(q)||k.agents.some(a=>DB.AGENTS[a].name.toLowerCase().includes(q));
    if(!hit) return false;
  }
  return true;
}

function termHTML(logs){
  const last=(logs||[]).slice(-3);
  const cls={ok:"ok",wr:"wr",ac:"ac"};
  const rows=last.map(l=>{
    const k=cls[l.k]||"";
    const pre=l.k==="ok"?"✓":l.k==="wr"?"!":l.k==="ac"?"›":"·";
    return `<div class="tline"><span class="ts">${l.t}</span> <span class="${k}">${pre} ${l.x}</span></div>`;
  }).join("");
  return `<div class="terminal">${rows}<div class="tline"><span class="ts">${last.length?'':'awaiting stream'}</span><span class="cursor"></span></div></div>`;
}

function buildCardHTML(k){
  const a0=DB.AGENTS[k.agents[0]];
  let agentsHtml;
  if(k.agents.length===1) agentsHtml=av(k.agents[0])+`<span class="aname">${a0.name}</span>`;
  else agentsHtml=k.agents.map((a,i)=>av(a,i>0)).join("")+`<span class="aname">${k.agents.length} agents</span>`;

  let mid="";
  if(k.status==="running" && k.progress!=null)
    mid+=`<div class="bar"><i style="width:${k.progress}%"></i></div>`;
  if(k.status==="running" && k.logs && k.logs.length)
    mid+=termHTML(k.logs);
  if(k.tests)
    mid+=`<div class="codeline"><span class="act test">TESTS</span><span class="path">${k.tests}</span></div>`;
  if(k.hitl){
    mid+=`<div class="hitl"><div class="q"><svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 9v4M12 17h.01"/><path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z"/></svg>${k.hitl.q}</div>`+
      k.hitl.opts.map((o,i)=>`<div class="opt ${i===0?'pri':''}" data-hitl="${i}"><span class="n">${i+1}</span>${o}</div>`).join("")+`</div>`;
  }

  let foot;
  if(k.branch){
    const cost=k.cost!=null?`<span class="cost">$${k.cost.toFixed(2)}</span>`:"";
    foot=`<div class="cfoot"><span class="branch">⎇ ${k.branch}</span>`+
      (k.add!=null?`<span class="add">+${k.add}</span><span class="del">-${k.del}</span>`:"")+
      cost+`<span class="when">${k.when}</span></div>`;
  } else {
    foot=`<div class="cfoot"><span class="branch">no branch</span><span class="when">${k.when}</span></div>`;
  }

  return `
    <div class="crow">
      <span class="ttag ${k.type}">${TYPE_LABEL[k.type]||k.type}</span>
      <span class="cid">#${k.id}</span>
      <span class="prio ${k.prio}">${k.prio}</span>
    </div>
    <div class="ctitle2">${k.title}</div>
    <div class="agents">${agentsHtml}</div>
    <div class="statusrow">
      <span class="pill ${k.status}"><span class="pd"></span>${STATUS_LABEL[k.status]}</span>
      <span class="model">${a0.model}</span>
    </div>
    ${mid}${foot}`;
}

function cardEl(k){
  const el=document.createElement("div");
  el.className=`card lvl lvl-${k.prio}`+(STATE.selectedId===k.id?" sel":"");
  el.dataset.id=k.id; el.draggable=true;
  el.innerHTML=buildCardHTML(k);
  wireCard(el,k);
  return el;
}

function wireCard(el,k){
  el.addEventListener("click",e=>{
    if(e.target.closest("[data-hitl]")){ resolveHitl(k,+e.target.closest("[data-hitl]").dataset.hitl); return; }
    openPanel(k.id);
  });
  el.addEventListener("dragstart",e=>{e.dataTransfer.setData("id",k.id);el.classList.add("dragging");STATE.dragging=true;});
  el.addEventListener("dragend",()=>{el.classList.remove("dragging");STATE.dragging=false;});
}

/* patch dynamic bits in place (used by live sim, no flicker) */
function patchCard(k){
  const el=document.querySelector(`.card[data-id="${k.id}"]`);
  if(!el) { updateCompactRow&&updateCompactRow(k); return; }
  const bar=el.querySelector(".bar i"); if(bar) bar.style.width=(k.progress||0)+"%";
  const pill=el.querySelector(".pill");
  if(pill){ pill.className="pill "+k.status; pill.innerHTML=`<span class="pd"></span>${STATUS_LABEL[k.status]}`; }
  const term=el.querySelector(".terminal");
  if(term && k.logs){ const nt=document.createElement("div"); nt.innerHTML=termHTML(k.logs);
    term.replaceWith(nt.firstElementChild); }
  else if(!term && k.status==="running" && k.logs && k.logs.length){
    // insert terminal after bar
    const b=el.querySelector(".bar"); if(b){ const nt=document.createElement("div"); nt.innerHTML=termHTML(k.logs); b.after(nt.firstElementChild);} }
  const cost=el.querySelector(".cfoot .cost"); if(cost&&k.cost!=null) cost.textContent="$"+k.cost.toFixed(2);
  const when=el.querySelector(".cfoot .when"); if(when) when.textContent=k.when;
}

/* ============================================================ COLUMNS LAYOUT */
function renderColumns(host){
  const wrap=document.createElement("div");
  wrap.className="board"; wrap.dataset.density=STATE.density;
  DB.WORKFLOW.forEach((c,i)=>{
    const cards=DB.CARDS.filter(k=>k.col===c.id && cardVisible(k));
    const col=document.createElement("div");
    col.className="col"; col.dataset.col=c.id;
    const wipTxt=c.wip?`WIP ${cards.length}/${c.wip}`:"";
    col.innerHTML=`
      <div class="col-head">
        <span class="idx">0${i+1}</span>
        <span class="cdot" style="background:${c.dot}"></span>
        <span class="ctitle">${c.name}</span>
        <span class="count">${cards.length}</span>
        ${c.gate?`<span class="gate" title="${c.gate==='human'?'Human approval gate':'Auto-validate gate'}"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/></svg></span>`:""}
        <span class="wip">${wipTxt}</span>
        <span class="cog" title="Configure stage" data-cfg="${c.id}"><svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="2.4"/><path d="M12 4v2M12 18v2M4 12h2M18 12h2"/></svg></span>
      </div>
      <div class="col-body" data-body="${c.id}"></div>
      <div class="col-add">+ Add task</div>`;
    wrap.appendChild(col);
    const body=col.querySelector(".col-body");
    cards.forEach(k=>body.appendChild(cardEl(k)));
    if(c.wip && cards.length>=c.wip) col.classList.add("wip-over");
    wireColDrop(col,c.id);
    col.querySelector(".cog").addEventListener("click",e=>{e.stopPropagation();go("workflows");});
    col.querySelector(".col-add").addEventListener("click",()=>addTask(c.id));
  });
  const ghost=document.createElement("div");
  ghost.className="col-ghost"; ghost.innerHTML="＋ Add stage";
  ghost.addEventListener("click",()=>go("workflows"));
  wrap.appendChild(ghost);
  host.appendChild(wrap);
}

function wireColDrop(col,colId){
  col.addEventListener("dragover",e=>{e.preventDefault();col.classList.add("drag-over");});
  col.addEventListener("dragleave",()=>col.classList.remove("drag-over"));
  col.addEventListener("drop",e=>{
    e.preventDefault();col.classList.remove("drag-over");
    const id=e.dataTransfer.getData("id"); moveCard(id,colId,true);
  });
}

/* ============================================================ COMPACT TABLE */
function renderCompact(host){
  const wrap=document.createElement("div"); wrap.className="compact";
  let rows="";
  DB.WORKFLOW.forEach(c=>{
    const cards=DB.CARDS.filter(k=>k.col===c.id && cardVisible(k));
    if(!cards.length) return;
    rows+=`<tr class="grp"><td colspan="6"><span class="cdot" style="background:${c.dot}"></span>${c.name} · ${cards.length}</td></tr>`;
    cards.forEach(k=>{
      const a0=DB.AGENTS[k.agents[0]];
      rows+=`<tr class="row${STATE.selectedId===k.id?' sel':''}" data-id="${k.id}">
        <td><div class="c-task"><span class="ttag ${k.type}">${TYPE_LABEL[k.type]||k.type}</span><span class="cid">#${k.id}</span><span>${k.title}</span></div></td>
        <td><div class="agents" style="margin:0">${k.agents.map((a,i)=>av(a,i>0)).join("")}</div></td>
        <td><span class="pill ${k.status}"><span class="pd"></span>${STATUS_LABEL[k.status]}</span></td>
        <td><span class="minibar"><i style="width:${k.progress||0}%"></i></span></td>
        <td><span class="prio ${k.prio}" style="display:inline-grid">${k.prio}</span></td>
        <td class="cost">${k.cost!=null?"$"+k.cost.toFixed(2):"—"}</td>
      </tr>`;
    });
  });
  wrap.innerHTML=`<table class="ctbl">
    <thead><tr><th>Task</th><th>Agents</th><th>Status</th><th>Progress</th><th>Pri</th><th>Cost</th></tr></thead>
    <tbody>${rows}</tbody></table>`;
  host.appendChild(wrap);
  wrap.querySelectorAll("tr.row").forEach(tr=>tr.addEventListener("click",()=>openPanel(tr.dataset.id)));
}
function updateCompactRow(k){
  const tr=document.querySelector(`tr.row[data-id="${k.id}"]`); if(!tr) return;
  const mb=tr.querySelector(".minibar i"); if(mb) mb.style.width=(k.progress||0)+"%";
  const pill=tr.querySelector(".pill"); if(pill){pill.className="pill "+k.status;pill.innerHTML=`<span class="pd"></span>${STATUS_LABEL[k.status]}`;}
  const c=tr.querySelector(".cost"); if(c) c.textContent=k.cost!=null?"$"+k.cost.toFixed(2):"—";
}

/* ============================================================ SWIMLANES BY AGENT */
function renderLanes(host){
  const wrap=document.createElement("div"); wrap.className="lanes";
  const activeAgents=Object.keys(DB.AGENTS).filter(ak=>DB.CARDS.some(k=>k.agents.includes(ak)&&cardVisible(k)));
  activeAgents.forEach(ak=>{
    const a=DB.AGENTS[ak];
    const mine=DB.CARDS.filter(k=>k.agents.includes(ak)&&cardVisible(k));
    const running=mine.filter(k=>k.status==="running").length;
    const spend=mine.reduce((s,k)=>s+(k.cost||0),0);
    const lane=document.createElement("div"); lane.className="lane";
    let cols="";
    DB.WORKFLOW.forEach(c=>{
      cols+=`<div class="lane-col" data-col="${c.id}" data-agent="${ak}">
        <div class="lc-head"><span class="cdot" style="background:${c.dot}"></span>${c.name}</div>
        <div class="lc-body" data-lbody="${c.id}"></div></div>`;
    });
    lane.innerHTML=`
      <div class="lane-head">
        <span class="av" style="width:24px;height:24px;background:${a.color}">${a.ini}</span>
        <span class="lname">${a.name}</span>
        <span class="lmeta">${a.model}</span>
        <span class="lspark"><span><b>${mine.length}</b> tasks</span><span><b>${running}</b> running</span><span><b>$${spend.toFixed(2)}</b> today</span></span>
      </div>
      <div class="lane-cols"></div>`;
    const lc=lane.querySelector(".lane-cols"); lc.innerHTML=cols;
    wrap.appendChild(lane);
    DB.WORKFLOW.forEach(c=>{
      const body=lane.querySelector(`[data-lbody="${c.id}"]`);
      mine.filter(k=>k.col===c.id).forEach(k=>body.appendChild(cardEl(k)));
    });
    lane.querySelectorAll(".lane-col").forEach(lcol=>{
      lcol.addEventListener("dragover",e=>{e.preventDefault();lcol.classList.add("drag-over");});
      lcol.addEventListener("dragleave",()=>lcol.classList.remove("drag-over"));
      lcol.addEventListener("drop",e=>{e.preventDefault();lcol.classList.remove("drag-over");
        moveCard(e.dataTransfer.getData("id"),lcol.dataset.col,true);});
    });
  });
  host.appendChild(wrap);
}

/* ============================================================ DISPATCH */
function renderBoard(){
  const host=document.getElementById("boardHost");
  // preserve scroll positions across the rebuild
  const scrolls={};
  host.querySelectorAll("[data-body]").forEach(b=>scrolls[b.dataset.body]=b.scrollTop);
  const oldScroller=host.querySelector(".board,.compact,.lanes");
  const sx=oldScroller?oldScroller.scrollLeft:0, sy=oldScroller?oldScroller.scrollTop:0;
  const laneScrolls=[];
  host.querySelectorAll(".lane-cols").forEach(lc=>laneScrolls.push(lc.scrollLeft));
  host.innerHTML="";
  if(STATE.layout==="columns") renderColumns(host);
  else if(STATE.layout==="compact") renderCompact(host);
  else renderLanes(host);
  // restore scroll
  const ns=host.querySelector(".board,.compact,.lanes"); if(ns){ ns.scrollLeft=sx; ns.scrollTop=sy; }
  host.querySelectorAll("[data-body]").forEach(b=>{ if(scrolls[b.dataset.body]!=null) b.scrollTop=scrolls[b.dataset.body]; });
  host.querySelectorAll(".lane-cols").forEach((lc,i)=>{ if(laneScrolls[i]!=null) lc.scrollLeft=laneScrolls[i]; });
  updateStrip();
}

function moveCard(id,colId,user){
  const k=DB.CARDS.find(x=>x.id===id); if(!k||k.col===colId) return;
  const from=DB.stage(k.col), to=DB.stage(colId);
  k.col=colId;
  // apply stage rules
  if(colId==="done"){ k.status="done"; k.progress=100; k.eta="—"; if(k.hitl)delete k.hitl; }
  else if(to.gate==="human"){ if(k.progress>=100&&!k.hitl) k.status="awaiting"; }
  else if(colId==="backlog"){ k.status="queued"; }
  else if(k.status==="done"){ k.status="running"; }
  if(user) logActivity(k,"move",`moved to <b>${to.name}</b>`);
  renderBoard();
  if(STATE.selectedId===id) renderPanel();
  const el=document.querySelector(`.card[data-id="${id}"]`); if(el) el.classList.add("just-moved");
}

function addTask(colId){ openTaskForm(null, colId); }
