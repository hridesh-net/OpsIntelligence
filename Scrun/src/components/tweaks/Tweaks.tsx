import { useState } from "react";
import { useStore } from "../../store";
import type { Density, Layout, Theme } from "../../types";
import { Close } from "../Icons";
import s from "./Tweaks.module.css";

const ACCENTS = ["#2898da", "#2dd4bf", "#a78bfa", "#f5b042"];

function Radio<T extends string>({ value, options, onChange }: { value: T; options: T[]; onChange: (v: T) => void }) {
  return (
    <div className={s.radio}>
      {options.map((o) => (
        <button key={o} className={o === value ? s.on : ""} onClick={() => onChange(o)}>{o}</button>
      ))}
    </div>
  );
}

export default function Tweaks() {
  const [open, setOpen] = useState(false);
  const st = useStore();

  if (!open)
    return (
      <button className={s.fab} onClick={() => setOpen(true)}>
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
          <path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6" />
        </svg>
        Tweaks
      </button>
    );

  return (
    <div className={s.panel}>
      <div className={s.head}>
        <b>Tweaks</b>
        <span className={s.x} onClick={() => setOpen(false)}><Close size={16} /></span>
      </div>
      <div className={s.body}>
        <div className={s.section}>Brand</div>
        <div className={s.row}>
          <span className={s.lbl}>Accent</span>
          <div className={s.swatches}>
            {ACCENTS.map((c) => (
              <span key={c} className={s.swatch + (st.accent === c ? " " + s.on : "")} style={{ background: c }} onClick={() => st.setAccent(c)} />
            ))}
          </div>
        </div>
        <div className={s.row}>
          <span className={s.lbl}>Theme</span>
          <Radio<Theme> value={st.theme} options={["dark", "light"]} onChange={st.setTheme} />
        </div>

        <div className={s.section}>Board</div>
        <div className={s.row}>
          <span className={s.lbl}>Layout</span>
          <Radio<Layout> value={st.layout} options={["columns", "compact", "lanes"]} onChange={st.setLayout} />
        </div>
        <div className={s.row}>
          <span className={s.lbl}>Density</span>
          <Radio<Density> value={st.density} options={["rich", "balanced", "lean"]} onChange={st.setDensity} />
        </div>
        <div className={s.row}>
          <span className={s.lbl}>Activity rail</span>
          <span className={"sw" + (st.showRail ? " on" : "")} style={{ marginLeft: "auto" }} onClick={st.toggleRail} />
        </div>

        <div className={s.section}>Simulation</div>
        <div style={{ marginBottom: 12 }}>
          <div className={s.sliderRow}><span>Tick speed</span><b>{st.simSpeed}ms</b></div>
          <input type="range" className={"rng " + s.slider} min={800} max={5000} step={200} value={st.simSpeed} onChange={(e) => st.setSimSpeed(+e.target.value)} />
        </div>
        <button className="btn" style={{ width: "100%", justifyContent: "center" }} onClick={() => st.openTaskForm(null, "backlog")}>
          Queue a task
        </button>
      </div>
    </div>
  );
}
