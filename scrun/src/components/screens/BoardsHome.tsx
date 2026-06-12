import { useState } from "react";
import { useStore } from "../../store";
import { boardMeta } from "../../api/kanban";
import Logo from "../Logo";
import s from "./BoardsHome.module.css";

/* Landing gallery: every configured board on the server, plus "create new".
   Opening a board enters the full Scrun workspace; creating one starts the
   setup wizard. */
export default function BoardsHome() {
  const boards = useStore((st) => st.boards);
  const apiMode = useStore((st) => st.apiMode);
  const openBoard = useStore((st) => st.openBoard);
  const startSetup = useStore((st) => st.startSetup);
  const [openingId, setOpeningId] = useState<string | null>(null);

  const open = async (id: string) => {
    if (openingId) return;
    setOpeningId(id);
    await openBoard(id);
    setOpeningId(null);
  };

  const newTile = (
    <button type="button" className={s.newCard} onClick={startSetup}>
      <span className={s.newIco}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round">
          <path d="M12 5v14M5 12h14" />
        </svg>
      </span>
      <span className={s.newLabel}>Create a new board</span>
      <span className={s.newHint}>Name, workflow, agents — about a minute</span>
    </button>
  );

  return (
    <div className={s.wrap}>
      <header className={s.top}>
        <div className={s.brand}>
          <Logo />
          <span>
            Scrun
            <div className={s.brandSub}>OpsIntelligence · Boards</div>
          </span>
        </div>
      </header>

      {apiMode === "loading" ? (
        <div className={s.loading}>
          <span className={s.spin} />
          loading boards
        </div>
      ) : boards.length === 0 ? (
        <div className={s.empty}>
          <div className={s.emptyArt}>
            <svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round">
              <rect x="3" y="3" width="6" height="18" rx="1.5" />
              <rect x="10.5" y="3" width="6" height="12" rx="1.5" />
              <rect x="18" y="3" width="3" height="9" rx="1.5" />
            </svg>
          </div>
          <div className={s.eyebrow}>Scrun · Autonomous boards</div>
          <h1 className={s.h1}>Create your first board</h1>
          <p className={s.sub} style={{ margin: "0 auto 26px" }}>
            Boards organise the work your agent workforce picks up — drop a card,
            an agent works it in an isolated worktree, and you approve at the gates.
          </p>
          <div className={s.grid} style={{ maxWidth: 320, margin: "0 auto" }}>{newTile}</div>
        </div>
      ) : (
        <>
          <div className={s.head}>
            <div className={s.eyebrow}>Scrun · Autonomous boards</div>
            <h1 className={s.h1}>Your boards</h1>
            <p className={s.sub}>
              Pick a board to enter its workspace, or spin up a new one.
            </p>
          </div>
          <div className={s.grid}>
            {boards.map((b) => {
              const meta = boardMeta(b);
              const color = meta.color;
              const key = meta.key.toUpperCase();
              return (
                <button
                  key={b.id}
                  type="button"
                  className={s.card}
                  style={{ "--bColor": color } as React.CSSProperties}
                  onClick={() => open(b.id)}
                >
                  <div className={s.cardTop}>
                    <span className={s.key}>{key}</span>
                    <span className={s.open}>
                      {openingId === b.id ? "opening" : "open"}
                      <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.2} strokeLinecap="round">
                        <path d="M5 12h14M13 6l6 6-6 6" />
                      </svg>
                    </span>
                  </div>
                  <div className={s.cardName}>{b.name}</div>
                  <div className={s.cardDesc}>{meta.desc || "No description yet."}</div>
                  <div className={s.cardFoot}>
                    <span className={s.dot} />
                    live · agents on call
                  </div>
                </button>
              );
            })}
            {newTile}
          </div>
        </>
      )}
    </div>
  );
}
