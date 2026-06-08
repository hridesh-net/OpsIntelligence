import { useEffect, useState } from "react";
import { useStore } from "../store";

/** Bottom-centre toast; re-shows whenever toast.n increments. */
export default function Toast() {
  const toast = useStore((s) => s.toast);
  const [show, setShow] = useState(false);

  useEffect(() => {
    if (!toast.n) return;
    setShow(true);
    const t = setTimeout(() => setShow(false), 2200);
    return () => clearTimeout(t);
  }, [toast.n]);

  return (
    <div className={"toast" + (show ? " show" : "")}>
      <span className="ti" />
      <span className="tm">{toast.msg}</span>
    </div>
  );
}
