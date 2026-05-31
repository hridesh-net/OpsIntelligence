/* ============================================================
   SCRUN — Tweaks panel (React) bridged to the vanilla app
   ============================================================ */
const TWEAK_DEFAULTS = {
  "accent": "#2898da",
  "theme": "light",
  "density": "rich",
  "layout": "columns",
  "simSpeed": 2200,
  "rail": true
};

function ScrunTweaks(){
  const [t,setTweak]=useTweaks(TWEAK_DEFAULTS);

  React.useEffect(()=>{ if(window.applyAccent) applyAccent(t.accent); },[t.accent]);
  React.useEffect(()=>{ if(window.applyTheme && document.documentElement.dataset.theme!==t.theme) applyTheme(t.theme); },[t.theme]);
  React.useEffect(()=>{ if(window.applyDensity) applyDensity(t.density); },[t.density]);
  React.useEffect(()=>{ if(!window.STATE)return; STATE.layout=t.layout;
    document.querySelectorAll("#layoutSeg button").forEach(b=>b.classList.toggle("on",b.dataset.layout===t.layout));
    if(STATE.screen==="board") renderBoard(); },[t.layout]);
  React.useEffect(()=>{ if(!window.STATE)return; STATE.simSpeed=t.simSpeed; if(window.restartSim) restartSim(); },[t.simSpeed]);
  React.useEffect(()=>{ if(!window.STATE)return; STATE.showRail=t.rail;
    const lr=document.getElementById("liveRail"); const ov=document.getElementById("overlay");
    if(lr && !(ov&&ov.classList.contains("open"))) lr.classList.toggle("hidden",!t.rail);
    const rb=document.getElementById("railToggleBtn"); if(rb) rb.classList.toggle("on",t.rail); },[t.rail]);

  return (
    <TweaksPanel title="Tweaks">
      <TweakSection label="Brand" />
      <TweakColor label="Accent" value={t.accent}
        options={["#2898da","#2dd4bf","#a78bfa","#f5b042"]}
        onChange={v=>setTweak("accent",v)} />
      <TweakRadio label="Theme" value={t.theme} options={["dark","light"]}
        onChange={v=>setTweak("theme",v)} />

      <TweakSection label="Board" />
      <TweakRadio label="Layout" value={t.layout} options={["columns","compact","lanes"]}
        onChange={v=>setTweak("layout",v)} />
      <TweakRadio label="Card density" value={t.density} options={["rich","balanced","lean"]}
        onChange={v=>setTweak("density",v)} />
      <TweakToggle label="Live activity rail" value={t.rail}
        onChange={v=>setTweak("rail",v)} />

      <TweakSection label="Simulation" />
      <TweakSlider label="Tick speed" value={t.simSpeed} min={800} max={5000} step={200} unit="ms"
        onChange={v=>setTweak("simSpeed",v)} />
      <TweakButton label="Queue a task" onClick={()=>window.addTask && addTask("backlog")} />
    </TweaksPanel>
  );
}

(function mountTweaks(){
  const mount=()=>{ const root=document.getElementById("tweaks-root");
    if(root && window.ReactDOM) ReactDOM.createRoot(root).render(<ScrunTweaks/>); };
  if(document.readyState!=="loading") setTimeout(mount,60); else document.addEventListener("DOMContentLoaded",()=>setTimeout(mount,60));
})();
