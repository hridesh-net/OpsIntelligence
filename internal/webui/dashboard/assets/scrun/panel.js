/* ============================================================
   SCRUN — Task detail (centered modal)
   ============================================================ */
const PANEL_TABS=["details","conversation","artifacts","timeline","metrics"];

function openPanel(id){
  STATE.selectedId=id; STATE.panelTab="details";
  document.querySelectorAll(".card.sel,tr.row.sel").forEach(e=>e.classList.remove("sel"));
  const el=document.querySelector(`.card[data-id="${id}"],tr.row[data-id="${id}"]`); if(el)el.classList.add("sel");
  renderPanel();
  document.getElementById("overlay").classList.add("open");
}
function closePanel(){
  document.getElementById("overlay").classList.remove("open"); STATE.selectedId=null;
  document.querySelectorAll(".card.sel,tr.row.sel").forEach(e=>e.classList.remove("sel"));
}

function pTermHTML(logs){
  if(!logs||!logs.length) return `<div class="p-term"><div class="tline"><span class="ts">— no stream yet —</span></div></div>`;
  const rows=logs.map(l=>{
    const cls={ok:"ok",wr:"wr",ac:"ac"}[l.k]||"";
    const pre=l.k==="ok"?"✓":l.k==="wr"?"!":l.k==="ac"?"›":"·";
    return `<div class="tline"><span class="ts">${l.t}</span> <span class="${cls}">${pre} ${l.x}</span></div>`;
  }).join("");
  return `<div class="p-term">${rows}</div>`;
}

function renderPanel(){
  const k=DB.CARDS.find(x=>x.id===STATE.selectedId); if(!k) return;
  const modal=document.getElementById("modal");
  const statusPill=`<span class="pill ${k.status}"><span class="pd"></span>${STATUS_LABEL[k.status]}</span>`;
  const tab=STATE.panelTab;
  let content="";

  const agentsSec=`<div class="p-sec"><div class="p-lbl">Assigned agents</div>
    <div class="p-agents">${k.agents.map(a=>`<div class="p-agent">${av(a)}<span class="aname">${DB.AGENTS[a].name}</span></div>`).join("")}
      <div class="p-agent" style="cursor:pointer" id="addAgentBtn"><span class="av" style="background:var(--surface);color:var(--text-dim)">+</span><span class="aname">Assign</span></div></div></div>`;
  const metricsSec=`<div class="p-sec"><div class="p-lbl">Run metrics</div>
    <div class="metrics">
      <div class="metric"><div class="ml">Cost</div><div class="mv">${k.cost!=null?"$"+k.cost.toFixed(2):"—"}</div></div>
      <div class="metric"><div class="ml">Tokens</div><div class="mv">${k.tokens?(k.tokens/1000).toFixed(1)+"K":"—"}</div></div>
      <div class="metric"><div class="ml">Duration</div><div class="mv">${k.duration||"—"}</div></div>
      <div class="metric"><div class="ml">Confidence</div><div class="mv ok">${k.conf?k.conf+"%":"—"}</div></div>
    </div></div>`;
  const propsSec=`<div class="p-sec"><div class="p-lbl">Properties</div>
    <div class="kv"><span class="k">Priority</span><span class="v">${({H:"High",M:"Medium",L:"Low"})[k.prio]}</span></div>
    <div class="kv"><span class="k">Workflow stage</span><span class="v">${DB.stage(k.col).name}</span></div>
    <div class="kv"><span class="k">Branch</span><span class="v">${k.branch||"—"}</span></div>
    <div class="kv"><span class="k">Lead model</span><span class="v">${DB.AGENTS[k.agents[0]].model}</span></div></div>`;
  const labelsSec=`<div class="p-sec"><div class="p-lbl">Labels</div><div class="labels">${(k.labels||[]).map(l=>`<span class="lab">${l}</span>`).join("")||'<span class="lab">none</span>'}</div></div>`;

  if(tab==="details"){
    const hitlSec=k.hitl?`<div class="p-sec"><div class="p-lbl">Human decision required</div>
      <div class="hitl"><div class="q"><svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 9v4M12 17h.01"/><path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z"/></svg>${k.hitl.q}</div>
      ${k.hitl.opts.map((o,i)=>`<div class="opt ${i===0?'pri':''}" data-hitl="${i}"><span class="n">${i+1}</span>${o}</div>`).join("")}</div></div>`:"";
    const acSec=(k.ac&&k.ac.length)?`<div class="p-sec"><div class="p-lbl">Acceptance criteria</div>
      <div class="ac-read">${k.ac.map((a,i)=>{const met=k.status==="done"||(k.progress||0)>=(i+1)/k.ac.length*100;
        return `<div class="acr ${met?'met':''}"><span class="b">${met?'<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><path d="m5 12 5 5L20 6"/></svg>':''}</span><span class="t">${a}</span></div>`;}).join("")}</div></div>`:"";
    content=`<div class="p-grid">
      <div class="pg-col">
        <div class="p-sec"><div class="p-lbl">Description</div><div class="p-desc">${k.desc||'<span style="color:var(--text-faint)">No description yet.</span>'}</div></div>
        ${acSec}
        ${hitlSec}
        <div class="p-sec"><div class="p-lbl">Progress</div>
          <div class="p-prog-row"><span>${k.status==="done"?"Complete":k.status==="queued"?"Queued":k.status==="awaiting"?"Awaiting human":"Working"}</span><span>${k.progress??0}% · ETA ${k.eta||"—"}</span></div>
          <div class="p-bar"><i style="width:${k.progress??0}%"></i></div></div>
        <div class="p-sec"><div class="p-lbl">Run log</div>${pTermHTML((k.logs||[]).slice(-6))}</div>
      </div>
      <div class="pg-col">${agentsSec}${metricsSec}${propsSec}${labelsSec}</div>
    </div>`;
  }
  else if(tab==="conversation"){
    const a0=DB.AGENTS[k.agents[0]];
    content=`<div class="p-sec"><div class="chat">
      <div class="msg">${av("review")}<div><div class="mname">Orchestrator</div><div class="mbody">Picked up <b>#${k.id}</b> and routed it to ${a0.name} based on capability match (${a0.caps.slice(0,2).join(", ")}).</div></div></div>
      <div class="msg">${av(k.agents[0])}<div><div class="mname">${a0.name}</div><div class="mbody">${k.desc}</div></div></div>
      <div class="msg">${av(k.agents[0])}<div><div class="mname">${a0.name}</div><div class="mbody">${k.status==="awaiting"?"I need a human decision before I can safely proceed — see the gate on the details tab.":k.status==="done"?"Task complete and merged. Closing out.":"Working through the plan now — streaming progress to the run log."}</div></div></div>
    </div></div>`;
  }
  else if(tab==="artifacts"){
    content=`<div class="p-grid">
      <div class="pg-col"><div class="p-sec"><div class="p-lbl">Run log</div>${pTermHTML(k.logs)}</div></div>
      <div class="pg-col"><div class="p-sec"><div class="p-lbl">Artifacts</div>
        ${k.branch?`<div class="kv"><span class="k">Branch</span><span class="v">${k.branch}</span></div>
        <div class="kv"><span class="k">Diff</span><span class="v"><span style="color:var(--success)">+${k.add??0}</span> / <span style="color:var(--danger)">-${k.del??0}</span></span></div>
        <div class="kv"><span class="k">Pull request</span><span class="v">${k.status==="done"?"#"+(1200+(+k.id.slice(3)))+" merged":"draft"}</span></div>`:'<div class="p-desc" style="color:var(--text-faint)">No artifacts produced yet.</div>'}
        ${k.tests?`<div class="kv"><span class="k">Tests</span><span class="v" style="color:var(--success)">${k.tests}</span></div>`:""}
      </div></div></div>`;
  }
  else if(tab==="timeline"){
    content=`<div class="p-sec"><div class="timeline">
      <div class="tl"><div class="tt">Task created</div><div class="ts">Queued into ${DB.stage(k.col).name}</div><div class="tm">${k.when} ago</div></div>
      <div class="tl"><div class="tt">${DB.AGENTS[k.agents[0]].name} assigned</div><div class="ts">Routed by orchestrator via capability match</div><div class="tm">${k.when} ago</div></div>
      ${k.branch?`<div class="tl"><div class="tt">Branch ${k.branch}</div><div class="ts">${k.add!=null?`+${k.add} / -${k.del} lines`:"working tree initialised"}</div><div class="tm">${k.duration||"—"} elapsed</div></div>`:""}
      ${k.tests?`<div class="tl ok"><div class="tt">Tests passed</div><div class="ts">${k.tests}</div><div class="tm">—</div></div>`:""}
      ${k.hitl?`<div class="tl warn"><div class="tt">Awaiting human decision</div><div class="ts">${k.hitl.q}</div><div class="tm">now</div></div>`:""}
      ${k.status==="done"?`<div class="tl ok"><div class="tt">Merged & closed</div><div class="ts">Deployed via CI/CD pipeline</div><div class="tm">${k.when} ago</div></div>`:""}
    </div></div>`;
  }
  else {
    content=`<div class="p-sec"><div class="p-lbl">Run metrics</div>
      <div class="metrics" style="grid-template-columns:repeat(3,1fr)">
        <div class="metric"><div class="ml">Token spend</div><div class="mv">${k.cost!=null?"$"+k.cost.toFixed(2):"—"}</div></div>
        <div class="metric"><div class="ml">Total tokens</div><div class="mv">${k.tokens?(k.tokens/1000).toFixed(1)+"K":"—"}</div></div>
        <div class="metric"><div class="ml">Wall time</div><div class="mv">${k.duration||"—"}</div></div>
        <div class="metric"><div class="ml">Confidence</div><div class="mv ok">${k.conf?k.conf+"%":"—"}</div></div>
        <div class="metric"><div class="ml">Lines +</div><div class="mv ok">${k.add!=null?"+"+k.add:"—"}</div></div>
        <div class="metric"><div class="ml">Lines −</div><div class="mv">${k.del!=null?"-"+k.del:"—"}</div></div>
      </div></div>`;
  }

  modal.innerHTML=`
    <div class="p-head">
      <div class="p-top"><span class="cid">#${k.id}</span>${statusPill}
        <span class="model" style="margin-left:0">${DB.AGENTS[k.agents[0]].model}</span>
        <span class="p-edit" id="pEdit" title="Edit ticket"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>Edit</span>
        <span class="p-close" id="pClose"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6 6 18M6 6l12 12"/></svg></span></div>
      <div class="p-title">${k.title}</div>
      <div class="p-tabs">${PANEL_TABS.map(t=>`<div class="p-tab ${t===tab?'on':''}" data-tab="${t}">${t[0].toUpperCase()+t.slice(1)}</div>`).join("")}</div>
    </div>
    <div class="p-body">${content}</div>
    <div class="p-actions">
      ${k.hitl?`<button class="btn" data-act="reject">Reject</button><button class="btn btn-primary" data-act="approve">Approve & continue</button>`
        :k.status==="done"?`<button class="btn" data-act="reopen">Reopen</button><button class="btn btn-primary" data-act="view">Open full view</button>`
        :`<button class="btn" data-act="reassign">Reassign</button><button class="btn btn-primary" data-act="advance">Advance stage</button>`}
    </div>`;

  document.getElementById("pClose").onclick=closePanel;
  const pe=document.getElementById("pEdit"); if(pe) pe.onclick=()=>{ closePanel(); openTaskForm(k.id); };
  modal.querySelectorAll(".p-tab").forEach(t=>t.onclick=()=>{STATE.panelTab=t.dataset.tab;renderPanel();});
  modal.querySelectorAll("[data-hitl]").forEach(o=>o.onclick=()=>resolveHitl(k,+o.dataset.hitl));
  modal.querySelectorAll("[data-act]").forEach(b=>b.onclick=()=>panelAction(k,b.dataset.act));
}

function panelAction(k,act){
  if(act==="approve"){ resolveHitl(k,0); }
  else if(act==="reject"){ resolveHitl(k,k.hitl?k.hitl.opts.length-1:1,true); }
  else if(act==="advance"){ const i=DB.WORKFLOW.findIndex(s=>s.id===k.col); if(i<DB.WORKFLOW.length-1) moveCard(k.id,DB.WORKFLOW[i+1].id,true); }
  else if(act==="reopen"){ moveCard(k.id,"inprogress",true); k.status="running"; k.progress=60; renderBoard(); renderPanel(); }
  else if(act==="reassign"){ toast("Reassignment routed to orchestrator"); }
  else if(act==="view"){ toast("Opening full task view…"); }
}

function resolveHitl(k,optIdx,reject){
  const choice=k.hitl?k.hitl.opts[optIdx]:"approved";
  delete k.hitl;
  if(reject){ k.status="running"; k.progress=Math.max(40,(k.progress||60)-20); logActivity(k,"hitl",`decision rejected — sent back for rework`); moveCard(k.id,"inprogress"); }
  else {
    logActivity(k,"hitl",`approved — <b>${choice}</b>`);
    const i=DB.WORKFLOW.findIndex(s=>s.id===k.col);
    if(k.col==="review"){ moveCard(k.id,"done"); }
    else if(i<DB.WORKFLOW.length-1){ k.status="running"; moveCard(k.id,DB.WORKFLOW[i+1].id); }
    else { k.status="running"; }
  }
  renderBoard(); if(STATE.selectedId===k.id) renderPanel();
  toast(reject?"Sent back for rework":"Decision approved");
}
