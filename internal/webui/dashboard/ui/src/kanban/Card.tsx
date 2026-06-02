import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import type { Card as CardModel } from "@/api/types";

interface CardProps {
  card: CardModel;
  onOpen?: (id: string) => void;
}

export function Card({ card, onOpen }: CardProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: card.id,
    data: { type: "card", card },
  });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={`card${isDragging ? " dragging" : ""}`}
      {...attributes}
      {...listeners}
      onDoubleClick={() => onOpen?.(card.id)}
    >
      <div className="card-head">
        <span className={`prio ${card.prio}`}>{card.prio}</span>
        {card.type && <span className="type">{card.type}</span>}
        <span className={`status ${card.status}`}>{card.status}</span>
      </div>
      <div className="card-title">{card.title}</div>
      <div className="card-foot">
        <div className="agents">
          {(card.agents || []).slice(0, 3).map((a) => (
            <div key={a} className="agent" title={a}>
              {a.slice(0, 2).toUpperCase()}
            </div>
          ))}
        </div>
        {card.when && <span className="when">{card.when}</span>}
      </div>
    </div>
  );
}

export function CardOverlay({ card }: { card: CardModel }) {
  return (
    <div className="card overlay">
      <div className="card-head">
        <span className={`prio ${card.prio}`}>{card.prio}</span>
        {card.type && <span className="type">{card.type}</span>}
        <span className={`status ${card.status}`}>{card.status}</span>
      </div>
      <div className="card-title">{card.title}</div>
    </div>
  );
}
