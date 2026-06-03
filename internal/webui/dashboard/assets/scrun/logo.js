/* ============================================================
   SCRUN — Spiral logo mark (iterative + incremental)
   Injects the spiral into every .logo tile and sets the favicon.
   ============================================================ */
(function(){
  const C=50, TURNS=1.35, STEPS=54, R_MAX=26, R_MIN=3, SW=6.4, HEAD_R=8;
  function points(){
    const out=[];
    for(let i=0;i<=STEPS;i++){
      const t=i/STEPS, r=R_MIN+t*(R_MAX-R_MIN), d=(-90+t*360*TURNS)*Math.PI/180;
      out.push((C+r*Math.cos(d)).toFixed(2)+" "+(C+r*Math.sin(d)).toFixed(2));
    }
    return out.join(" ");
  }
  function head(){
    const d=(-90+360*TURNS)*Math.PI/180;
    return [(C+R_MAX*Math.cos(d)).toFixed(2),(C+R_MAX*Math.sin(d)).toFixed(2)];
  }
  function inner(ink){
    const [hx,hy]=head();
    return `<polyline points="${points()}" fill="none" stroke="${ink}" stroke-width="${SW}" stroke-linecap="round" stroke-linejoin="round"/>`+
      `<circle cx="${hx}" cy="${hy}" r="${HEAD_R}" fill="${ink}"/>`;
  }
  const SCRUN_LOGO_SVG = ink => `<svg viewBox="0 0 100 100" width="100%" height="100%" aria-hidden="true">${inner(ink)}</svg>`;
  window.SCRUN_LOGO_SVG = SCRUN_LOGO_SVG;

  function injectAll(){
    document.querySelectorAll(".logo").forEach(el=>{ if(!el.querySelector("svg")) el.innerHTML=SCRUN_LOGO_SVG("#fff"); });
  }
  function setFavicon(){
    const fav=`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">`+
      `<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#2898da"/><stop offset="1" stop-color="#4db0ef"/></linearGradient></defs>`+
      `<rect width="100" height="100" rx="24" fill="url(#g)"/>`+
      `<g transform="translate(10,10) scale(0.8)">${inner("#ffffff")}</g></svg>`;
    let link=document.querySelector('link[rel="icon"]');
    if(!link){ link=document.createElement("link"); link.rel="icon"; document.head.appendChild(link); }
    link.type="image/svg+xml";
    link.href="data:image/svg+xml,"+encodeURIComponent(fav);
  }
  function run(){ injectAll(); setFavicon(); }
  if(document.readyState!=="loading") run(); else document.addEventListener("DOMContentLoaded",run);
  // re-inject if logos are re-rendered later
  window.injectScrunLogos = injectAll;
})();
