/* ============================================================
   SCRUN — Analytics screen renderer (charts in pure SVG/CSS)
   Derives live numbers from the board + a little synthetic history.
   ============================================================ */
function anDerive(){
  const cards=DB.CARDS;
  const done=cards.filter(k=>k.status==="done");
  const running=cards.filter(k=>k.status==="running");
  const awaiting=cards.filter(k=>k.status==="awaiting");
  const queued=cards.filter(k=>k.status==="queued");
  const spend=cards.reduce((s,k)=>s+(k.cost||0),0)+8.4;
  const tokens=cards.reduce((s,k)=>s+(k.tokens||0),0);
  const conf=cards.filter(k=>k.conf).reduce((s,k,_,a)=>s+k.conf/a.length,0);
  return {cards,done,running,awaiting,queued,spend,tokens,conf};
}

function anKPIs(d){
  const items=[
    {l:"Tasks shipped",v:128+d.done.length,d:"+18%",dir:"up",ic:'<path d="m5 12 5 5L20 6"/>'},
    {l:"Avg cycle time",v:"4.2h",d:"−12%",dir:"up",ic:'<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>'},
    {l:"Autonomy rate",v:"86%",d:"+5%",dir:"up",ic:'<path d="M12 2v4M12 18v4M2 12h4M18 12h4"/><circle cx="12" cy="12" r="3"/>'},
    {l:"Spend today",v:"$"+d.spend.toFixed(2),d:"on budget",dir:"flat",ic:'<path d="M12 1v22M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/>'},
  ];
  return `<div class="an-kpis">${items.map(k=>`
    <div class="kpi">
      <div class="kl"><span class="ki"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">${k.ic}</svg></span>${k.l}</div>
      <div class="kv">${k.v}</div>
      <div class="kd ${k.dir}">${k.dir==="up"?"▲":k.dir==="down"?"▼":"●"} ${k.d}<span style="color:var(--text-faint)">vs last week</span></div>
    </div>`).join("")}</div>`;
}

/* throughput — synthetic 7-day completed/in-progress */
function anThroughput(){
  const days=["Mon","Tue","Wed","Thu","Fri","Sat","Sun"];
  const data=[[6,2],[9,3],[7,4],[11,3],[13,4],[5,2],[8,3]];
  const max=Math.max(...data.map(d=>d[0]+d[1]));
  return `<div class="panel-card">
    <div class="pc-h"><b>Throughput</b><span class="sub">tasks / day</span>
      <span class="leg"><span><i style="background:var(--accent)"></i>Shipped</span><span><i style="background:var(--accent-soft)"></i>In progress</span></span></div>
    <div class="barchart">${data.map((d,i)=>{
      const dn=d[0]/max*100, wp=d[1]/max*100;
      return `<div class="barcol"><div class="bv">${d[0]+d[1]}</div>
        <div class="stack" style="height:${dn+wp}%"><i class="done" style="height:${dn/(dn+wp)*100}%"></i><i class="wip" style="height:${wp/(dn+wp)*100}%"></i></div>
        <div class="bx">${days[i]}</div></div>`;
    }).join("")}</div></div>`;
}

/* status donut */
function anStatus(d){
  const segs=[
    {nm:"Done",v:d.done.length,c:"var(--success)"},
    {nm:"Running",v:d.running.length,c:"var(--accent)"},
    {nm:"Awaiting human",v:d.awaiting.length,c:"var(--warning)"},
    {nm:"Queued",v:d.queued.length,c:"var(--text-faint)"},
  ].filter(s=>s.v>0);
  const total=segs.reduce((s,x)=>s+x.v,0)||1;
  const R=58, CIRC=2*Math.PI*R; let off=0;
  const rings=segs.map(s=>{
    const len=s.v/total*CIRC;
    const seg=`<circle cx="70" cy="70" r="${R}" fill="none" stroke="${s.c}" stroke-width="16" stroke-dasharray="${len.toFixed(2)} ${(CIRC-len).toFixed(2)}" stroke-dashoffset="${(-off).toFixed(2)}" transform="rotate(-90 70 70)" stroke-linecap="butt"/>`;
    off+=len; return seg;
  }).join("");
  return `<div class="panel-card">
    <div class="pc-h"><b>Work distribution</b><span class="sub">${total} active</span></div>
    <div class="donut-wrap">
      <div class="donut"><svg width="140" height="140" viewBox="0 0 140 140"><circle cx="70" cy="70" r="58" fill="none" stroke="var(--surface-2)" stroke-width="16"/>${rings}</svg>
        <div class="dc"><b>${d.running.length+d.awaiting.length}</b><small>in flight</small></div></div>
      <div class="donut-legend">${segs.map(s=>`<div class="dl"><span class="sw" style="background:${s.c}"></span><span class="nm">${s.nm}</span><span class="vv">${s.v}</span></div>`).join("")}</div>
    </div></div>`;
}

/* cycle time per stage */
function anCycle(){
  const rows=DB.WORKFLOW.map((s,i)=>{
    const base=[0.4,1.1,3.8,1.6,2.2,0][i%6]+Math.random()*0.4;
    return {nm:s.name,dot:s.dot,h:base};
  });
  const max=Math.max(...rows.map(r=>r.h))||1;
  return `<div class="panel-card">
    <div class="pc-h"><b>Avg time in stage</b><span class="sub">hours</span></div>
    <div class="cyclelist">${rows.map(r=>`
      <div class="cyclerow"><div class="cn"><span class="cd" style="background:${r.dot}"></span>${r.nm}</div>
        <div class="ctrack"><i style="width:${(r.h/max*100).toFixed(0)}%;background:${r.dot}"></i></div>
        <div class="cval">${r.h.toFixed(1)}h</div></div>`).join("")}</div></div>`;
}

/* spend area chart (synthetic 14 pts) */
function anSpend(d){
  const pts=[]; let v=2.2;
  for(let i=0;i<14;i++){ v+=(Math.random()-0.35)*0.6; v=Math.max(0.8,v); pts.push(v); }
  pts[13]=d.spend/14*3.1;
  const max=Math.max(...pts), W=560, H=150, P=6;
  const x=i=>P+i*(W-2*P)/(pts.length-1);
  const y=v=>H-P-(v/max)*(H-2*P);
  const line=pts.map((v,i)=>`${i?"L":"M"}${x(i).toFixed(1)} ${y(v).toFixed(1)}`).join(" ");
  const area=`${line} L${x(pts.length-1).toFixed(1)} ${H-P} L${x(0).toFixed(1)} ${H-P} Z`;
  return `<div class="panel-card">
    <div class="pc-h"><b>Token spend trend</b><span class="sub">14-day · $${d.spend.toFixed(2)} today</span></div>
    <svg class="area-chart" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none">
      <defs><linearGradient id="spendg" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="var(--accent)" stop-opacity=".34"/><stop offset="1" stop-color="var(--accent)" stop-opacity="0"/></linearGradient></defs>
      <path d="${area}" fill="url(#spendg)"/>
      <path d="${line}" fill="none" stroke="var(--accent)" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>
      <circle cx="${x(13).toFixed(1)}" cy="${y(pts[13]).toFixed(1)}" r="4" fill="var(--accent-bright)" stroke="var(--surface)" stroke-width="2"/>
    </svg>
    <div class="spend-foot"><span>14 days ago</span><span>today</span></div></div>`;
}

/* agent leaderboard */
function anLeaderboard(){
  const rows=Object.entries(DB.AGENTS).map(([k,a])=>{
    const st=DB.AGENT_STATS[k];
    const active=DB.CARDS.filter(c=>c.agents.includes(k)&&(c.status==="running"||c.status==="awaiting")).length;
    const util=Math.min(100,Math.round(active/(a.maxParallel||3)*100)+ (st.tasks%30));
    return {k,a,st,active,util:Math.min(100,util)};
  }).sort((x,y)=>y.st.tasks-x.st.tasks);
  return `<div class="panel-card">
    <div class="pc-h"><b>Agent leaderboard</b><span class="sub">all-time</span></div>
    <table class="lb"><thead><tr><th>Agent</th><th class="r">Tasks</th><th class="r">Success</th><th class="r">Spend</th><th class="r">Utilisation</th></tr></thead>
    <tbody>${rows.map(r=>`<tr>
      <td><div class="ag"><span class="av" style="background:${r.a.color}">${r.a.ini}</span><div><b>${r.a.name}</b><small>${r.a.model}</small></div></div></td>
      <td class="r mono">${r.st.tasks}</td>
      <td class="r succ">${r.st.success}%</td>
      <td class="r mono">$${r.st.spend.toFixed(0)}</td>
      <td class="r"><div class="util"><div class="ut"><i style="width:${r.util}%"></i></div><span class="mono" style="width:34px">${r.util}%</span></div></td>
    </tr>`).join("")}</tbody></table></div>`;
}

function renderAnalytics(){
  const host=document.getElementById("analyticsBody");
  const d=anDerive();
  host.innerHTML=`
    <div class="shead">
      <div><h1>Analytics</h1><p>How your autonomous workforce is performing — throughput, cycle time, spend and per-agent productivity across <span data-boardname>${SU&&SU.name||"the board"}</span>.</p></div>
      <div class="right"><button class="btn">Last 7 days ▾</button><button class="btn">Export</button></div>
    </div>
    ${anKPIs(d)}
    <div class="an-grid">${anThroughput()}${anStatus(d)}</div>
    <div class="an-grid">${anSpend(d)}${anCycle()}</div>
    ${anLeaderboard()}`;
}
