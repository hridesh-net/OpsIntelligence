import { logoInner } from "../lib/logoSvg";

/** The Scrun spiral logo tile (gradient background set via the .logo class). */
export default function Logo({ className = "logo", ink = "#fff" }: { className?: string; ink?: string }) {
  return (
    <span className={className}>
      <svg viewBox="0 0 100 100" width="100%" height="100%" aria-hidden="true" dangerouslySetInnerHTML={{ __html: logoInner(ink) }} />
    </span>
  );
}
