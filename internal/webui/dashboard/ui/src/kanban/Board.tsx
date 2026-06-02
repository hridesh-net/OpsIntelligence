import { useMemo, useState } from "react";
import {
  DndContext,
  DragOverlay,
  PointerSensor,
  KeyboardSensor,
  useSensor,
  useSensors,
  closestCorners,
  type DragEndEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { sortableKeyboardCoordinates } from "@dnd-kit/sortable";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getBoard, listBoards, moveCard } from "@/api/kanban";
import type { Board as BoardModel, Card as CardModel } from "@/api/types";
import { Column } from "./Column";
import { CardOverlay } from "./Card";
import { Topbar } from "@/chrome/Topbar";
import { CreateBoardModal } from "./CreateBoardModal";

export function Board() {
  const qc = useQueryClient();
  const boardsQ = useQuery({ queryKey: ["boards"], queryFn: listBoards });
  const [boardId, setBoardId] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const effectiveBoardId = boardId ?? boardsQ.data?.[0]?.id ?? null;

  const boardQ = useQuery({
    queryKey: ["board", effectiveBoardId],
    queryFn: () => getBoard(effectiveBoardId!),
    enabled: !!effectiveBoardId,
    refetchInterval: 8000,
  });

  const [search, setSearch] = useState("");
  const [activeCard, setActiveCard] = useState<CardModel | null>(null);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const board: BoardModel | undefined = boardQ.data;

  const cardsByColumn = useMemo(() => {
    if (!board) return new Map<string, CardModel[]>();
    const m = new Map<string, CardModel[]>();
    for (const col of board.columns) m.set(col.id, []);
    const q = search.trim().toLowerCase();
    for (const c of board.cards) {
      if (q && !`${c.title} ${c.desc ?? ""}`.toLowerCase().includes(q)) continue;
      const list = m.get(c.col);
      if (list) list.push(c);
      else m.set(c.col, [c]);
    }
    for (const list of m.values()) list.sort((a, b) => (a.order ?? 0) - (b.order ?? 0));
    return m;
  }, [board, search]);

  const moveMut = useMutation({
    mutationFn: ({ cardId, columnId }: { cardId: string; columnId: string }) =>
      moveCard(effectiveBoardId!, cardId, columnId),
    onMutate: async ({ cardId, columnId }) => {
      const key = ["board", effectiveBoardId];
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<BoardModel>(key);
      if (prev) {
        qc.setQueryData<BoardModel>(key, {
          ...prev,
          cards: prev.cards.map((c) => (c.id === cardId ? { ...c, col: columnId } : c)),
        });
      }
      return { prev };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) qc.setQueryData(["board", effectiveBoardId], ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["board", effectiveBoardId] });
    },
  });

  function onDragStart(e: DragStartEvent) {
    const data = e.active.data.current as { card?: CardModel } | undefined;
    if (data?.card) setActiveCard(data.card);
  }

  function onDragEnd(e: DragEndEvent) {
    setActiveCard(null);
    if (!board || !e.over) return;
    const cardId = String(e.active.id);
    const overId = String(e.over.id);
    const overData = e.over.data.current as { type?: string; columnId?: string } | undefined;

    const card = board.cards.find((c) => c.id === cardId);
    if (!card) return;

    let targetColumn = card.col;
    if (overData?.type === "column") {
      targetColumn = overData.columnId!;
    } else {
      const overCard = board.cards.find((c) => c.id === overId);
      if (overCard) targetColumn = overCard.col;
    }

    if (targetColumn === card.col) return;
    moveMut.mutate({ cardId, columnId: targetColumn });
  }

  const newBoardBtn = (
    <button className="btn primary" onClick={() => setShowCreate(true)}>+ New board</button>
  );

  if (boardsQ.isLoading) {
    return (
      <>
        <Topbar title="Boards" actions={newBoardBtn} />
        <div className="kanban-loading">Loading boards…</div>
        <CreateBoardModal open={showCreate} onClose={() => setShowCreate(false)} onCreated={setBoardId} />
      </>
    );
  }
  if (boardsQ.error) {
    return (
      <>
        <Topbar title="Boards" actions={newBoardBtn} />
        <div className="kanban-error">Failed to load boards: {(boardsQ.error as Error).message}</div>
        <CreateBoardModal open={showCreate} onClose={() => setShowCreate(false)} onCreated={setBoardId} />
      </>
    );
  }
  if (!boardsQ.data || boardsQ.data.length === 0) {
    return (
      <>
        <Topbar title="Boards" actions={newBoardBtn} />
        <div className="view" style={{ display: "grid", placeItems: "center" }}>
          <div style={{
            maxWidth: 380,
            textAlign: "center",
            background: "var(--bg-elev)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-lg)",
            padding: 32,
            boxShadow: "var(--shadow-card)",
          }}>
            <h2 style={{ marginBottom: 8 }}>No boards yet</h2>
            <p style={{ color: "var(--fg-muted)", marginBottom: 20 }}>
              Create your first board to start tracking work. Pick a workflow preset or wire it to a GitHub repo.
            </p>
            <button className="btn primary" onClick={() => setShowCreate(true)}>+ New board</button>
          </div>
        </div>
        <CreateBoardModal open={showCreate} onClose={() => setShowCreate(false)} onCreated={setBoardId} />
      </>
    );
  }

  return (
    <div className="kanban">
      <Topbar
        title="Boards"
        sub={board?.name}
        actions={newBoardBtn}
      />
      <div className="kanban-toolbar">
        <select
          className="board-pick"
          value={effectiveBoardId ?? ""}
          onChange={(e) => setBoardId(e.target.value)}
        >
          {boardsQ.data.map((b) => (
            <option key={b.id} value={b.id}>{b.name}</option>
          ))}
        </select>
        <input
          className="search"
          placeholder="Filter cards…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <div className="spacer" />
      </div>

      {boardQ.isLoading && <div className="kanban-loading">Loading board…</div>}
      {boardQ.error && <div className="kanban-error">Failed to load board: {(boardQ.error as Error).message}</div>}

      {board && (
        <DndContext
          sensors={sensors}
          collisionDetection={closestCorners}
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          onDragCancel={() => setActiveCard(null)}
        >
          <div className="board-scroll">
            <div className="board">
              {board.columns.map((col) => (
                <Column
                  key={col.id}
                  column={col}
                  cards={cardsByColumn.get(col.id) ?? []}
                />
              ))}
            </div>
          </div>
          <DragOverlay>{activeCard && <CardOverlay card={activeCard} />}</DragOverlay>
        </DndContext>
      )}

      <CreateBoardModal open={showCreate} onClose={() => setShowCreate(false)} onCreated={setBoardId} />
    </div>
  );
}
