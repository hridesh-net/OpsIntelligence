/* ============================================================
   SCRUN — Live simulation engine + activity log + stats
   ============================================================ */

/* ---------- activity log ---------- */
function nowTime(){ const d=new Date(); return String(d.getHours()).padStart(2,"0")+":"+String(d.getMinutes()).padStart(2,"0")+":"+String(d.getSeconds()).padStart(2,"0"); }
function logActivity(k,tag,text,meta){
  DB.ACTIVITY.unshift({id:k.id,agent:k.agents[0],tag,text,meta:meta||"",time:nowTime()});
  if(DB.ACTIVITY.length>120) DB.ACTIVITY.pop();
  if(STATE.screen==="activity") renderActivity();
  renderLiveRail();
}
function totalSpend(){ return DB.CARDS.reduce((s,k)=>s+(k.cost||0),0)+8.4; }

/* ---------- log line templates ---------- */
const LOG_TPL={
  infra:[["ac","terraform plan: %d to add"],["ok","apply complete"],["","provisioning IAM roles"],["ac","kubectl rollout status"],["ok","cluster healthy"]],
  fix:[["ac","reproduce in test harness"],["wr","null deref at line %d"],["ok","patch applied"],["ac","running regression suite"]],
  feat:[["ac","scaffold component"],["","wiring state + props"],["ok","unit tests green"],["ac","tsc --noEmit"]],
  research:[["ac","collecting benchmarks"],["","p99 latency sampled"],["ok","analysis complete"],["ac","drafting recommendation"]],
  sec:[["ac","scanning IAM policies"],["wr","over-broad permission found"],["","checking network rules"],["ok","no critical findings"]],
  chore:[["ac","rotating credentials"],["ok","secrets re-sealed"],["","updating manifests"]],
};
function nextLogTime(k){
  const last=k.logs&&k.logs.length?k.logs[k.logs.length-1].t:"00:00:00";
  let [h,m,s]=last.split(":").map(Number); s+=18+Math.floor(Math.random()*40);
  if(s>=60){m+=Math.floor(s/60);s%=60;} if(m>=60){h+=Math.floor(m/60);m%=60;}
  return [h,m,s].map(n=>String(n).padStart(2,"0")).join(":");
}
function pushLog(k){
  const tpl=(LOG_TPL[k.type]||LOG_TPL.feat);
  const pick=tpl[Math.floor(Math.random()*tpl.length)];
  const x=pick[1].replace("%d",10+Math.floor(Math.random()*200));
  k.logs=k.logs||[]; k.logs.push({t:nextLogTime(k),k:pick[0],x});
  if(k.logs.length>14) k.logs.shift();
}

/* ---------- engine ---------- */
function tick(){
  if(!STATE.simRunning || STATE.dragging) return;
  let structural=false;
  STATE.tickN=(STATE.tickN||0)+1;

  DB.CARDS.forEach(k=>{
    if(k.status!=="running") return;
    const inc=4+Math.floor(Math.random()*9);
    k.progress=Math.min(100,(k.progress||0)+inc);
    k.cost=(k.cost||0)+Math.round((0.004+Math.random()*0.02)*100)/100;
    k.tokens=(k.tokens||0)+200+Math.floor(Math.random()*600);
    k.when="now";
    if(Math.random()<0.7) pushLog(k);

    if(k.progress>=100){
      const stage=DB.stage(k.col);
      const idx=DB.WORKFLOW.findIndex(s=>s.id===k.col);
      if(stage.gate==="human"){
        k.status="awaiting";
        if(!k.hitl) k.hitl={q:`Approve ${k.type==="sec"?"security findings":"changes"} for #${k.id}?`,opts:["Human approval","Auto-validate"]};
        logActivity(k,"hitl","completed work — awaiting human approval");
        structural=true;
      } else if(idx<DB.WORKFLOW.length-1){
        const next=DB.WORKFLOW[idx+1];
        if(k.tests==null && (next.id==="testing"||k.type!=="research")) k.tests="45/45 pass";
        k.col=next.id; k.progress=next.id==="done"?100:8; k.logs=k.logs.slice(-2);
        if(next.id==="done"){ k.status="done"; k.eta="—"; if(k.hitl)delete k.hitl;
          logActivity(k,"done","merged & deployed",`${k.branch||""} · $${(k.cost||0).toFixed(2)}`); }
        else { logActivity(k,"move",`advanced to <b>${next.name}</b>`);
          if(next.rules.autoAssign==="review" && !k.agents.includes("review")) k.agents.push("review"); }
        structural=true;
      } else { k.status="done"; }
    } else {
      if(Math.random()<0.25) logActivity(k,k.type==="infra"?"commit":"run",
        k.type==="infra"?`pushed to <b>${k.branch||"branch"}</b>`:`progress <b>${k.progress}%</b>`);
      if(!structural) patchCard(k);
    }
  });

  // orchestrator picks up queued work if capacity allows
  if(STATE.tickN%2===0){
    const ip=DB.stage("inprogress"); const ipCount=DB.CARDS.filter(k=>k.col==="inprogress").length;
    const cap=ip&&ip.wip?ip.wip:5;
    if(ipCount<cap){
      const cand=DB.CARDS.find(k=>(k.col==="todo")&&k.status==="queued");
      if(cand){ cand.col="inprogress"; cand.status="running"; cand.progress=6;
        cand.branch=cand.branch||`${cand.type}/${cand.id.toLowerCase()}`; cand.add=cand.add||0; cand.del=cand.del||0;
        cand.cost=cand.cost||0; cand.tokens=cand.tokens||0; cand.eta="~"+(6+Math.floor(Math.random()*16))+"m"; cand.logs=[];
        pushLog(cand);
        logActivity(cand,"run",`picked up by <b>${DB.AGENTS[cand.agents[0]].name}</b>`);
        structural=true;
      } else {
        // refill todo from backlog occasionally
        const b=DB.CARDS.find(k=>k.col==="backlog"&&k.status==="queued");
        if(b && Math.random()<0.5){ b.col="todo"; logActivity(b,"move","pulled into <b>To Do</b>"); structural=true; }
      }
    }
  }

  if(structural && STATE.screen==="board") renderBoard();
  updateStrip();
  if(STATE.screen==="agents" && STATE.tickN%4===0) renderAgents();
}

/* ---------- live rail (board right side) ---------- */
function renderLiveRail(){
  const body=document.getElementById("liveRailBody"); if(!body) return;
  const items=DB.ACTIVITY.slice(0,14);
  body.innerHTML=items.map(e=>{
    const a=DB.AGENTS[e.agent];
    return `<div class="lr-item"><span class="lav" style="background:${a.color}">${a.ini}</span>
      <div class="lc"><div class="ln">${a.name}<span class="ftime">${e.time}</span></div>
      <div class="lt"><span class="cid" data-open="${e.id}" style="cursor:pointer">#${e.id}</span> ${e.text}</div></div></div>`;
  }).join("")||`<div class="lt" style="color:var(--text-faint);padding:14px 2px">Waiting for agent activity…</div>`;
  body.querySelectorAll("[data-open]").forEach(c=>c.onclick=()=>{ if(STATE.screen!=="board")go("board"); openPanel(c.dataset.open); });
}

/* ---------- stats strip ---------- */
function updateStrip(){
  const vis=DB.CARDS.filter(cardVisible);
  const agents=new Set(); let run=0,aw=0;
  vis.forEach(k=>{ if(k.status==="running"){k.agents.forEach(a=>agents.add(a));run++;} if(k.status==="awaiting")aw++; });
  set("sActive",agents.size); set("sRun",run); set("sAwait",aw); set("sSpend","$"+totalSpend().toFixed(2));
  // column counts
  DB.WORKFLOW.forEach(c=>{
    const n=DB.CARDS.filter(k=>k.col===c.id&&cardVisible(k)).length;
    const el=document.querySelector(`.col[data-col="${c.id}"] .count`); if(el)el.textContent=n;
  });
}
function set(id,v){ const el=document.getElementById(id); if(el)el.textContent=v; }

/* ---------- seed initial history so feed/rail are never empty ---------- */
function seedActivity(){
  if(DB.ACTIVITY.length) return;
  const seed=[
    {id:"AI-338",agent:"devops",tag:"done",text:"merged & deployed",meta:"infra/cicd · $0.27",time:"08:41:12"},
    {id:"AI-339",agent:"obs",tag:"done",text:"12 monitors live — merged",meta:"feat/alerts",time:"08:39:55"},
    {id:"AI-350",agent:"sec",tag:"hitl",text:"flagged 2 IAM findings — awaiting approval",time:"09:02:18"},
    {id:"AI-358",agent:"frontend",tag:"commit",text:"pushed to <b>feat/stripe-tests</b>",meta:"+210 −18",time:"09:06:40"},
    {id:"AI-345",agent:"research",tag:"run",text:"benchmark complete — redis 2.1ms vs memorydb 3.4ms",time:"09:11:03"},
    {id:"AI-348",agent:"devops",tag:"run",text:"applied 3 managed node groups",meta:"infra/eks-cluster",time:"09:18:22"},
    {id:"AI-351",agent:"review",tag:"hitl",text:"review complete — 3 suggestions, awaiting merge",time:"09:24:50"},
    {id:"AI-355",agent:"frontend",tag:"hitl",text:"blocked on HEIC conversion decision",time:"09:30:11"},
  ];
  seed.forEach(s=>DB.ACTIVITY.push(s));
}

let SIM_TIMER=null;
function startSim(){ if(SIM_TIMER)return; SIM_TIMER=setInterval(tick,STATE.simSpeed||2200); }
function stopSim(){ clearInterval(SIM_TIMER); SIM_TIMER=null; }
function restartSim(){ stopSim(); if(STATE.simRunning) startSim(); }

/* ---------- tweak bridges ---------- */
const ACCENTS={
  "#2898da":{a:"#2898da",b:"#4db0ef",rgb:"40,152,218"},
  "#2dd4bf":{a:"#2dd4bf",b:"#5fe6d4",rgb:"45,212,191"},
  "#a78bfa":{a:"#a78bfa",b:"#c0acff",rgb:"167,139,250"},
  "#f5b042":{a:"#f5b042",b:"#ffc66b",rgb:"245,176,66"},
};
function applyAccent(hex){
  const p=ACCENTS[hex]||ACCENTS["#2898da"]; const r=document.documentElement.style;
  r.setProperty("--accent",p.a); r.setProperty("--accent-bright",p.b);
  r.setProperty("--running",p.a); r.setProperty("--accent-rgb",p.rgb);
  r.setProperty("--accent-soft",`rgba(${p.rgb},.16)`); r.setProperty("--accent-line",`rgba(${p.rgb},.45)`);
}
function applyDensity(v){ STATE.density=v; const b=document.querySelector(".board"); if(b)b.dataset.density=v; if(STATE.screen==="board")renderBoard(); }
