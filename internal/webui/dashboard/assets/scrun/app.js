/* ============================================================
   SCRUN — App shell: state, navigation, topbar, theme, init
   ============================================================ */
const STATE={
  screen:"board",
  layout:"columns",       // columns | compact | lanes
  density:"rich",         // rich | balanced | lean
  simSpeed:2200,
  filters:{agent:"all",prio:"all",type:"all",q:""},
  selectedId:null,
  panelTab:"details",
  simRunning:true,
  dragging:false,
  showRail:true,
  tickN:0,
};
window.STATE=STATE;

/* ---------- persistence ---------- */
function saveState(){ try{ localStorage.setItem("scrun",JSON.stringify({
  screen:STATE.screen,layout:STATE.layout,density:STATE.density,simRunning:STATE.simRunning,
  theme:document.documentElement.dataset.theme,showRail:STATE.showRail})); }catch(e){} }
function loadState(){ try{ const s=JSON.parse(localStorage.getItem("scrun")||"{}");
  if(s.layout)STATE.layout=s.layout; if(s.density)STATE.density=s.density;
  if(s.simRunning!=null)STATE.simRunning=s.simRunning;
  if(s.showRail!=null)STATE.showRail=s.showRail;
  if(s.theme)document.documentElement.dataset.theme=s.theme; }catch(e){} }

/* ---------- navigation ---------- */
const SCREEN_TITLES={board:"AI Workforce Board",workflows:"Workflow Builder",agents:"Agent Manager",activity:"Agent Activity",analytics:"Analytics"};
function go(screen){
  STATE.screen=screen;
  document.querySelectorAll(".nav-item").forEach(n=>n.classList.toggle("on",n.dataset.go===screen));
  document.querySelectorAll(".screen").forEach(s=>s.classList.toggle("on",s.id==="screen-"+screen));
  document.getElementById("boardToolbar").style.display=screen==="board"?"flex":"none";
  document.getElementById("screenTitleWrap").style.display=screen==="board"?"none":"flex";
  document.getElementById("screenTitle").textContent=SCREEN_TITLES[screen];
  if(screen==="board"){ renderBoard(); renderLiveRail(); }
  else if(screen==="workflows") renderWorkflows();
  else if(screen==="agents") renderAgents();
  else if(screen==="activity") renderActivity();
  else if(screen==="analytics") renderAnalytics();
  saveState();
}

/* ---------- theme ---------- */
const _sunIco=`<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5 19 19M19 5l-1.5 1.5M6.5 17.5 5 19"/>`;
const _moonIco=`<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z"/>`;
function syncThemeIco(){const i=document.getElementById("themeIco"); if(i)i.innerHTML=document.documentElement.dataset.theme==="dark"?_sunIco:_moonIco;}
function applyTheme(v){ document.documentElement.dataset.theme=v; syncThemeIco(); document.dispatchEvent(new CustomEvent("scrun-theme")); saveState(); }

/* ---------- toast ---------- */
let toastTimer=null;
function toast(msg){
  let t=document.getElementById("toast");
  if(!t){ t=document.createElement("div"); t.id="toast"; t.className="toast"; t.innerHTML='<span class="ti"></span><span class="tm"></span>'; document.body.appendChild(t); }
  t.querySelector(".tm").textContent=msg; t.classList.add("show");
  clearTimeout(toastTimer); toastTimer=setTimeout(()=>t.classList.remove("show"),2200);
}

/* ---------- menus ---------- */
function buildMenus(){
  const am=document.getElementById("agentMenu");
  am.innerHTML=`<div class="mlabel">Filter by agent</div>
    <div class="mi sel" data-agent="all">All agents<span class="ck">✓</span></div>`+
    Object.entries(DB.AGENTS).map(([k,a])=>`<div class="mi" data-agent="${k}"><span class="av" style="background:${a.color}">${a.ini}</span>${a.name}<span class="ck">✓</span></div>`).join("");
  am.querySelectorAll(".mi").forEach(mi=>mi.onclick=()=>setFilter("agent",mi.dataset.agent,am,"agentBtn"));
}
function setFilter(key,val,menu,btnId){
  STATE.filters[key]=val;
  menu.querySelectorAll(".mi").forEach(x=>x.classList.remove("sel"));
  menu.querySelector(`[data-${key}="${val}"]`).classList.add("sel");
  document.getElementById(btnId).classList.toggle("on",val!=="all");
  menu.classList.remove("open");
  renderBoard();
  document.getElementById("filtNote").style.display=anyFilter()?"":"none";
}
function anyFilter(){const f=STATE.filters;return f.agent!=="all"||f.prio!=="all"||f.type!=="all"||!!f.q;}
function wireMenu(btnId,menuId,key){
  const btn=document.getElementById(btnId),menu=document.getElementById(menuId);
  btn.onclick=e=>{e.stopPropagation();const r=btn.getBoundingClientRect();menu.style.left="";menu.style.right="";
    closeMenus(menu);menu.classList.toggle("open");
    // align under button
    menu.style.left=Math.min(r.left,window.innerWidth-210)+"px";};
  menu.querySelectorAll(".mi").forEach(mi=>{ if(mi.dataset[key]!==undefined) mi.onclick=()=>setFilter(key,mi.dataset[key],menu,btnId); });
}
function closeMenus(except){document.querySelectorAll(".menu").forEach(m=>{if(m!==except)m.classList.remove("open");});}

/* ---------- topbar wiring ---------- */
function wireTopbar(){
  document.getElementById("agentBtn").onclick=e=>{e.stopPropagation();const b=e.currentTarget.getBoundingClientRect();
    const m=document.getElementById("agentMenu");closeMenus(m);m.classList.toggle("open");m.style.left=Math.min(b.left,window.innerWidth-210)+"px";};
  document.getElementById("agentMenu").querySelectorAll(".mi").forEach(mi=>mi.onclick=()=>setFilter("agent",mi.dataset.agent,document.getElementById("agentMenu"),"agentBtn"));
  wireMenu("prioBtn","prioMenu","prio");
  wireMenu("typeBtn","typeMenu","type");

  document.getElementById("search").addEventListener("input",e=>{STATE.filters.q=e.target.value;renderBoard();document.getElementById("filtNote").style.display=anyFilter()?"":"none";});
  document.getElementById("clearFilt").onclick=()=>{
    STATE.filters={agent:"all",prio:"all",type:"all",q:""};document.getElementById("search").value="";
    document.querySelectorAll(".menu .mi").forEach(mi=>mi.classList.toggle("sel",mi.dataset.agent==="all"||mi.dataset.prio==="all"||mi.dataset.type==="all"));
    ["agentBtn","prioBtn","typeBtn"].forEach(b=>document.getElementById(b).classList.remove("on"));
    document.getElementById("filtNote").style.display="none"; renderBoard();
  };

  // layout segmented
  document.querySelectorAll("#layoutSeg button").forEach(b=>b.onclick=()=>{
    STATE.layout=b.dataset.layout;
    document.querySelectorAll("#layoutSeg button").forEach(x=>x.classList.toggle("on",x===b));
    renderBoard(); saveState();
  });

  // theme
  syncThemeIco();
  document.getElementById("themeSw").onclick=()=>{
    applyTheme(document.documentElement.dataset.theme==="dark"?"light":"dark");
  };

  // sim toggle
  document.getElementById("simToggle").onclick=()=>{
    STATE.simRunning=!STATE.simRunning;
    document.getElementById("simToggle").classList.toggle("paused",!STATE.simRunning);
    document.getElementById("simLabel").textContent=STATE.simRunning?"Live":"Paused";
    if(STATE.simRunning)startSim();else stopSim(); saveState();
  };

  // rail toggle
  document.getElementById("railToggleBtn").onclick=()=>{
    STATE.showRail=!STATE.showRail;
    document.getElementById("liveRail").classList.toggle("hidden",!STATE.showRail);
    document.getElementById("railToggleBtn").classList.toggle("on",STATE.showRail);
    saveState();
  };

  document.getElementById("newTaskBtn").onclick=()=>{ if(STATE.screen!=="board")go("board"); addTask(STATE.layout==="columns"?"backlog":"todo"); };
  document.getElementById("railNewBtn").onclick=()=>addTask("backlog");

  // nav
  document.querySelectorAll(".nav-item").forEach(n=>n.onclick=()=>go(n.dataset.go));
  document.getElementById("collapseBtn").onclick=()=>document.getElementById("rail").classList.toggle("collapsed");

  // board picker menu
  const bp=document.getElementById("boardPick");
  bp.addEventListener("click",e=>{ if(e.target.closest(".mi"))return; e.stopPropagation();
    const m=document.getElementById("boardMenu"); closeMenus(m); m.classList.toggle("open"); });
  document.getElementById("boardReconfig").onclick=()=>{ document.getElementById("boardMenu").classList.remove("open"); reconfigureBoard(); };
  document.getElementById("boardWorkflow").onclick=()=>{ document.getElementById("boardMenu").classList.remove("open"); go("workflows"); };

  // close modals on backdrop click
  document.getElementById("overlay").addEventListener("click",e=>{ if(e.target.id==="overlay") closePanel(); });
  document.getElementById("agentOverlay").addEventListener("click",e=>{ if(e.target.id==="agentOverlay") closeAgentConfig(); });
  document.getElementById("taskOverlay").addEventListener("click",e=>{ if(e.target.id==="taskOverlay") closeTaskForm(); });

  // global close + open-from-feed
  document.addEventListener("click",e=>{ closeMenus(); const o=e.target.closest("[data-open]");
    if(o){ if(STATE.screen!=="board")go("board"); openPanel(o.dataset.open); } });
  document.addEventListener("keydown",e=>{ if(e.key==="Escape"){closePanel();if(window.closeAgentConfig)closeAgentConfig();if(window.closeTaskForm)closeTaskForm();}
    if((e.metaKey||e.ctrlKey)&&e.key==="k"){e.preventDefault();document.getElementById("search").focus();} });
}

/* ---------- init ---------- */
function bootApp(){
  if(window.__booted){ go("board"); renderLiveRail(); return; }
  window.__booted=true;
  seedActivity();
  buildMenus();
  wireTopbar();
  // restore controls
  document.querySelectorAll("#layoutSeg button").forEach(x=>x.classList.toggle("on",x.dataset.layout===STATE.layout));
  document.getElementById("simToggle").classList.toggle("paused",!STATE.simRunning);
  document.getElementById("simLabel").textContent=STATE.simRunning?"Live":"Paused";
  document.getElementById("liveRail").classList.toggle("hidden",!STATE.showRail);
  document.getElementById("railToggleBtn").classList.toggle("on",STATE.showRail);
  go("board");
  renderLiveRail();
  if(STATE.simRunning) startSim();
}

async function init(){
  loadState();
  syncThemeIco();
  const setupEl=document.getElementById("setup");
  if(setupEl) setupEl.classList.remove("on");
  const root=document.getElementById("appRoot"); if(root) root.style.display="";

  // v1.0.51 live mode: pull the first board from /api/v1/boards and
  // overwrite DB in place. If no boards exist or the API errors out we
  // silently fall back to the bundled Demo fixtures so the shell still
  // renders something. Skip the API call when the user explicitly
  // toggled Demo mode (localStorage.scrunDemoMode === "1").
  const demoMode = localStorage.getItem("scrunDemoMode") === "1";
  if (!demoMode && window.ScrunAPI) {
    try {
      const res = await window.ScrunAPI.loadFirstBoard();
      if (res && res.ok) {
        // Wipe demo-seeded contents and replace with API data, keeping
        // helper functions on DB so the rest of Scrun stays happy.
        Object.keys(DB.AGENTS).forEach(k => delete DB.AGENTS[k]);
        Object.assign(DB.AGENTS, res.agents);
        DB.WORKFLOW.length = 0;
        res.workflow.forEach(s => DB.WORKFLOW.push(s));
        DB.CARDS.length = 0;
        res.cards.forEach(c => DB.CARDS.push(c));
        if (DB.AGENT_STATS) {
          Object.keys(DB.AGENT_STATS).forEach(k => delete DB.AGENT_STATS[k]);
          Object.assign(DB.AGENT_STATS, res.stats || {});
        }
        if (res.boardName) {
          document.querySelectorAll("[data-boardname]").forEach(el => {
            el.textContent = res.boardName;
          });
        }
      }
    } catch (e) {
      console.warn("[scrun] live load threw; demo fixtures retained:", e);
    }
  }
  bootApp();
}
window.scrunMount=function scrunMount(){ return init(); };
