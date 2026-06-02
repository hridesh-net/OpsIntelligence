import { useDroppable } from "@dnd-kit/core";
import { SortableContext, verticalListSortingStrategy } from "@dnd-kit/sortable";
import type { Card as CardModel, Column as ColumnModel } from "@/api/types";
import { Card } from "./Card";

interface ColumnProps {
  column: ColumnModel;
  cards: CardModel[];
  onOpenCard?: (id: string) => void;
  onAddCard?: (columnId: string) => void;
}

export function Column({ column, cards, onOpenCard, onAddCard }: ColumnProps) {
  const { setNodeRef, isOver } = useDroppable({
    id: column.id,
    data: { type: "column", columnId: column.id },
  });

  const overWip = column.wip != null && cards.length > column.wip;

  return (
    <div ref={setNodeRef} className={`col${isOver ? " dragging-over" : ""}`}>
      <div className="col-head">
        {column.dot && <span className="dot" style={{ background: column.dot }} />}
        <span className="name">{column.name}</span>
        <span className="count">{cards.length}</span>
        {column.wip != null && (
          <span className={`wip${overWip ? " over" : ""}`}>WIP {cards.length}/{column.wip}</span>
        )}
      </div>
      <div className="col-body">
        <SortableContext items={cards.map((c) => c.id)} strategy={verticalListSortingStrategy}>
          {cards.map((c) => (
            <Card key={c.id} card={c} onOpen={onOpenCard} />
          ))}
        </SortableContext>
      </div>
      <button className="col-add" onClick={() => onAddCard?.(column.id)}>+ New card</button>
    </div>
  );
}
